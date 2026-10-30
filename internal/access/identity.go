package access

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
)

type Registration struct {
	Email    string `json:"email" minLength:"3" maxLength:"254"`
	Password string `json:"password" minLength:"15" maxLength:"1024"`
	Name     string `json:"name" minLength:"1" maxLength:"100"`
	Currency string `json:"currency" minLength:"3" maxLength:"3"`
	Zone     string `json:"zone" minLength:"1" maxLength:"100"`
	Locale   string `json:"locale" minLength:"2" maxLength:"50"`
}

func newSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Service) Register(ctx context.Context, v Registration, origin string) error {
	defer s.publicDelay(ctx, time.Now())
	email, err := canonicalEmail(v.Email)
	if err != nil {
		return err
	}
	settings := SpaceSettings{Name: v.Name, Purpose: "personal", Currency: v.Currency, Zone: v.Zone, Locale: v.Locale, PeriodDay: 1, NewAccountVisibility: "propose_shared"}
	if err := settings.validate(); err != nil {
		return err
	}
	if !s.crypto.passwordAllowed(v.Password) {
		return ErrInvalid
	}
	if err := s.limit(ctx, "registration", email, origin); err != nil {
		return err
	}
	hash, err := s.crypto.hash(ctx, v.Password)
	if err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		id := uuid.New().String()
		n, err := q.InsertPrincipal(ctx, accessdb.InsertPrincipalParams{ID: id, Realm: "customer", Email: email, PasswordHash: hash,
			DisplayName: v.Name, State: "pending", Locale: v.Locale, Zone: v.Zone, Currency: v.Currency})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		if err := s.issueToken(ctx, tx, id, email, "verify"); err != nil {
			return err
		}
		return s.audit(ctx, tx, id, "identity:"+id, "identity.registered", id)
	})
}

func (s *Service) issueToken(ctx context.Context, tx pgx.Tx, principal, email, purpose string) error {
	q := accessdb.New(tx)
	last, err := q.LatestToken(ctx, accessdb.LatestTokenParams{PrincipalID: principal, Purpose: purpose})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil && time.Since(last.Time) < s.policy.ResendDelay {
		return nil
	}
	if err := q.InvalidateTokens(ctx, accessdb.InvalidateTokensParams{PrincipalID: principal, Purpose: purpose}); err != nil {
		return err
	}
	token, id := newSecret(), uuid.New().String()
	expiry := time.Now().Add(s.policy.TokenTTL)
	if err := q.InsertToken(ctx, accessdb.InsertTokenParams{ID: id, PrincipalID: principal, Purpose: purpose, Verifier: verifier(token), TargetEmail: email, ExpiresAt: instant(expiry)}); err != nil {
		return err
	}
	return s.enqueueMail(ctx, tx, mailIntent{Kind: purpose, Ref: id, Principal: principal, Email: email, Secret: token, Expiry: expiry})
}

// Public requests have the same response whether the mailbox is absent, pending,
// active, suspended or in another realm. Recovery never resets an existing factor.
func (s *Service) RequestToken(ctx context.Context, email, purpose, origin string) error {
	defer s.publicDelay(ctx, time.Now())
	email, err := canonicalEmail(email)
	if err != nil {
		return err
	}
	if purpose != "verify" && purpose != "recover" {
		return ErrInvalid
	}
	if err := s.limit(ctx, "token."+purpose, email, origin); err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		p, err := q.FindPrincipal(ctx, accessdb.FindPrincipalParams{Realm: "customer", Email: email})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p, err = q.GetPrincipal(ctx, p.ID)
		if err != nil {
			return err
		}
		if (purpose == "verify" && p.State != "pending") || (purpose == "recover" && p.State != "active") {
			return nil
		}
		return s.issueToken(ctx, tx, p.ID, p.Email, purpose)
	})
}

func (s *Service) tokenPrincipal(ctx context.Context, tx pgx.Tx, token, purpose string) (accessdb.Principal, accessdb.IdentityToken, error) {
	q := accessdb.New(tx)
	t, err := q.FindToken(ctx, accessdb.FindTokenParams{Verifier: verifier(token), Purpose: purpose})
	if errors.Is(err, pgx.ErrNoRows) {
		return accessdb.Principal{}, t, ErrUnauthorized
	}
	if err != nil {
		return accessdb.Principal{}, t, err
	}
	p, err := q.GetPrincipal(ctx, t.PrincipalID)
	if err != nil {
		return p, t, err
	}
	t, err = q.LockToken(ctx, t.ID)
	if err != nil {
		return p, t, err
	}
	if !time.Now().Before(t.ExpiresAt.Time) || t.ConsumedAt.Valid || t.InvalidatedAt.Valid || p.State == "suspended" {
		return p, t, ErrUnauthorized
	}
	return p, t, nil
}

func (s *Service) Verify(ctx context.Context, token, origin string) error {
	if len(token) != 43 {
		return ErrInvalid
	}
	if err := s.limit(ctx, "verify", token, origin); err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, t, err := s.tokenPrincipal(ctx, tx, token, "verify")
		// Replaying an already consumed verification is a harmless acknowledgement,
		// never a session or another personal space.
		if errors.Is(err, ErrUnauthorized) && p.State == "active" && t.ConsumedAt.Valid && !t.InvalidatedAt.Valid {
			return nil
		}
		if err != nil {
			return err
		}
		if p.State != "pending" {
			return ErrUnauthorized
		}
		q := accessdb.New(tx)
		if err := q.ActivatePrincipal(ctx, p.ID); err != nil {
			return err
		}
		settings := SpaceSettings{Name: "Personal", Purpose: "personal", Currency: p.Currency, Zone: p.Zone, Locale: p.Locale,
			PeriodDay: 1, NewAccountVisibility: "propose_shared"}
		if _, err := s.createSpace(ctx, tx, p, settings, true); err != nil {
			return err
		}
		if err := q.ConsumeToken(ctx, t.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, p.ID, "identity:"+p.ID, "identity.verified", p.ID)
	})
}

type Credentials struct {
	Email    string `json:"email" minLength:"3" maxLength:"254"`
	Password string `json:"password" minLength:"1" maxLength:"1024"`
	Code     string `json:"code,omitempty" maxLength:"100"`
	Device   string `json:"device" minLength:"1" maxLength:"100"`
}
type SessionResult struct {
	Token     string    `json:"token"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s *Service) Login(ctx context.Context, v Credentials, realm, origin string) (SessionResult, error) {
	email, err := canonicalEmail(v.Email)
	if err != nil {
		return SessionResult{}, err
	}
	if realm != "customer" && realm != "staff" {
		return SessionResult{}, ErrInvalid
	}
	if !validText(v.Device, 100) || len(v.Password) > 1024 || len(v.Code) > 100 {
		return SessionResult{}, ErrInvalid
	}
	if err := s.limit(ctx, "login."+realm, email, origin); err != nil {
		return SessionResult{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, s.store.DB.Timeout)
	snapshot, findErr := accessdb.New(s.store.DB.Pool).FindPrincipal(readCtx, accessdb.FindPrincipalParams{Realm: realm, Email: email})
	cancel()
	if findErr != nil && !errors.Is(findErr, pgx.ErrNoRows) {
		return SessionResult{}, findErr
	}
	encoded := snapshot.PasswordHash
	if findErr != nil {
		encoded = s.crypto.dummy
	}
	ok, err := s.crypto.verify(ctx, v.Password, encoded)
	if err != nil {
		return SessionResult{}, err
	}
	if !ok || findErr != nil {
		return SessionResult{}, ErrUnauthorized
	}
	var result SessionResult
	err = s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, err := accessdb.New(tx).GetPrincipal(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		if p.State != "active" || p.PasswordHash != snapshot.PasswordHash || p.Email != email {
			return ErrUnauthorized
		}
		if err := s.factor(ctx, tx, p, v.Code); err != nil {
			return err
		}
		result, err = s.newSession(ctx, tx, p, v.Device)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, p.ID, "identity:"+p.ID, "session.created", result.SessionID)
	})
	if err != nil {
		return SessionResult{}, err
	}
	return result, nil
}

func (s *Service) lifetimes(realm string) (time.Duration, time.Duration) {
	if realm == "staff" {
		return s.policy.StaffIdle, s.policy.StaffAbsolute
	}
	return s.policy.SessionIdle, s.policy.SessionAbsolute
}
func (s *Service) newSession(ctx context.Context, tx pgx.Tx, p accessdb.Principal, device string) (SessionResult, error) {
	q := accessdb.New(tx)
	now := time.Now()
	idle, absolute := s.lifetimes(p.Realm)
	count, err := q.CountSessions(ctx, accessdb.CountSessionsParams{PrincipalID: p.ID, ExpiresAt: instant(now), SeenAt: instant(now.Add(-idle))})
	if err != nil {
		return SessionResult{}, err
	}
	if count >= int64(s.policy.MaxSessions) {
		return SessionResult{}, ErrLimited
	}
	prefix := "c_"
	if p.Realm == "staff" {
		prefix = "s_"
	}
	result := SessionResult{Token: prefix + newSecret(), SessionID: uuid.New().String(), ExpiresAt: now.Add(absolute)}
	err = q.InsertSession(ctx, accessdb.InsertSessionParams{ID: result.SessionID, PrincipalID: p.ID, Verifier: verifier(result.Token),
		Device: device, CreatedAt: instant(now), ExpiresAt: instant(result.ExpiresAt)})
	return result, err
}

type Actor struct {
	principal accessdb.Principal
	session   accessdb.Session
}

func (a Actor) ID() string { return a.principal.ID }

func bearer(authorization, realm string) (string, error) {
	token, ok := strings.CutPrefix(authorization, "Bearer ")
	prefix := "c_"
	if realm == "staff" {
		prefix = "s_"
	}
	if !ok || len(token) != 45 || !strings.HasPrefix(token, prefix) {
		return "", ErrUnauthorized
	}
	return token, nil
}
func (s *Service) sessionOwner(ctx context.Context, authorization, realm string) (accessdb.Principal, error) {
	token, err := bearer(authorization, realm)
	if err != nil {
		return accessdb.Principal{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.store.DB.Timeout)
	defer cancel()
	q := accessdb.New(s.store.DB.Pool)
	session, err := q.FindSession(ctx, verifier(token))
	if err != nil {
		return accessdb.Principal{}, sessionError(err)
	}
	// Reading an owner here does not authorize. All protected operations revalidate
	// principal and session under locks inside their committing transaction.
	return accessdb.Principal{ID: session.PrincipalID}, nil
}
func sessionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnauthorized
	}
	return err
}

func (s *Service) authenticate(ctx context.Context, tx pgx.Tx, authorization, realm string, exclusive bool) (Actor, error) {
	token, err := bearer(authorization, realm)
	if err != nil {
		return Actor{}, err
	}
	q := accessdb.New(tx)
	initial, err := q.FindSession(ctx, verifier(token))
	if err != nil {
		return Actor{}, sessionError(err)
	}
	var p accessdb.Principal
	if exclusive {
		p, err = q.GetPrincipal(ctx, initial.PrincipalID)
	} else {
		p, err = q.ReadPrincipal(ctx, initial.PrincipalID)
	}
	if err != nil {
		return Actor{}, sessionError(err)
	}
	session, err := q.LockSession(ctx, initial.ID)
	if err != nil {
		return Actor{}, sessionError(err)
	}
	idle, _ := s.lifetimes(realm)
	now := time.Now()
	expired := !now.Before(session.ExpiresAt.Time) || !now.Before(session.SeenAt.Time.Add(idle))
	invalid := p.State != "active" || p.Realm != realm || session.RevokedAt.Valid || expired
	if invalid || (realm == "staff" && len(p.MfaSecret) == 0) {
		return Actor{}, ErrUnauthorized
	}
	if now.Sub(session.SeenAt.Time) >= s.policy.ActivityInterval {
		if err := q.TouchSession(ctx, accessdb.TouchSessionParams{ID: session.ID, SeenAt: instant(now)}); err != nil {
			return Actor{}, err
		}
	}
	return Actor{principal: p, session: session}, nil
}
func (s *Service) recent(a Actor) error {
	if time.Since(a.session.ReauthenticatedAt.Time) > s.policy.ReauthenticationTTL {
		return ErrReauthenticate
	}
	return nil
}

func (s *Service) factor(ctx context.Context, tx pgx.Tx, p accessdb.Principal, code string) error {
	if len(p.MfaSecret) == 0 {
		if p.Realm == "staff" {
			return ErrUnauthorized
		}
		return nil
	}
	secret, err := s.crypto.open("mfa:"+p.ID, p.MfaSecret)
	if err != nil {
		return err
	}
	defer clear(secret)
	step, err := totpStep(string(secret), code, time.Now(), p.MfaStep)
	q := accessdb.New(tx)
	if err == nil {
		return q.SetMFAStep(ctx, accessdb.SetMFAStepParams{ID: p.ID, MfaStep: step})
	}
	n, err := q.UseRecoveryCode(ctx, accessdb.UseRecoveryCodeParams{PrincipalID: p.ID, Verifier: verifier(code)})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrUnauthorized
	}
	return s.audit(ctx, tx, p.ID, "identity:"+p.ID, "mfa.recovery_used", p.ID)
}

func totpStep(secret, code string, now time.Time, last int64) (int64, error) {
	if len(code) != 6 {
		return 0, ErrUnauthorized
	}
	current := now.Unix() / 30
	for _, step := range []int64{current, current - 1, current + 1} {
		expected, err := totp.GenerateCode(secret, time.Unix(step*30, 0))
		if err != nil {
			return 0, err
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 && step > last {
			return step, nil
		}
	}
	return 0, ErrUnauthorized
}

func (s *Service) Reauthenticate(ctx context.Context, authorization, password, code, realm, origin string) (SessionResult, error) {
	if len(password) > 1024 || len(code) > 100 {
		return SessionResult{}, ErrInvalid
	}
	p, err := s.sessionOwner(ctx, authorization, realm)
	if err != nil {
		return SessionResult{}, err
	}
	if err := s.limit(ctx, "reauthenticate", p.ID, origin); err != nil {
		return SessionResult{}, err
	}
	var result SessionResult
	err = s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, realm, true)
		if err != nil {
			return err
		}
		ok, err := s.crypto.verify(ctx, password, a.principal.PasswordHash)
		if err != nil {
			return err
		}
		if !ok {
			return ErrUnauthorized
		}
		if err := s.factor(ctx, tx, a.principal, code); err != nil {
			return err
		}
		q := accessdb.New(tx)
		if _, err := q.RevokeSession(ctx, accessdb.RevokeSessionParams{PrincipalID: a.principal.ID, ID: a.session.ID}); err != nil {
			return err
		}
		result, err = s.newSession(ctx, tx, a.principal, a.session.Device)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, a.principal.ID, "identity:"+a.principal.ID, "session.rotated", result.SessionID)
	})
	if err != nil {
		return SessionResult{}, err
	}
	return result, nil
}

type ResetPassword struct{ Token, Password, Code string }

func (s *Service) ResetPassword(ctx context.Context, v ResetPassword, origin string) error {
	if len(v.Token) != 43 || !s.crypto.passwordAllowed(v.Password) || len(v.Code) > 100 {
		return ErrInvalid
	}
	if err := s.limit(ctx, "reset", v.Token, origin); err != nil {
		return err
	}
	hash, err := s.crypto.hash(ctx, v.Password)
	if err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		p, t, err := s.tokenPrincipal(ctx, tx, v.Token, "recover")
		if err != nil {
			return err
		}
		if p.State != "active" {
			return ErrUnauthorized
		}
		if err := s.factor(ctx, tx, p, v.Code); err != nil {
			return err
		}
		q := accessdb.New(tx)
		if err := q.ChangePassword(ctx, accessdb.ChangePasswordParams{ID: p.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.ConsumeToken(ctx, t.ID); err != nil {
			return err
		}
		if err := q.InvalidateTokens(ctx, accessdb.InvalidateTokensParams{PrincipalID: p.ID, Purpose: "recover"}); err != nil {
			return err
		}
		if err := q.RevokeSessions(ctx, p.ID); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, p, "password.reset")
	})
}

func (s *Service) ChangePassword(ctx context.Context, authorization, password, realm string) error {
	if !s.crypto.passwordAllowed(password) {
		return ErrInvalid
	}
	if _, err := s.Profile(ctx, authorization, realm); err != nil {
		return err
	}
	hash, err := s.crypto.hash(ctx, password)
	if err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, realm, true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		q := accessdb.New(tx)
		if err := q.ChangePassword(ctx, accessdb.ChangePasswordParams{ID: a.ID(), PasswordHash: hash}); err != nil {
			return err
		}
		if err := q.RevokeSessions(ctx, a.ID()); err != nil {
			return err
		}
		if err := q.InvalidateTokens(ctx, accessdb.InvalidateTokensParams{PrincipalID: a.ID(), Purpose: "recover"}); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "password.changed")
	})
}

func (s *Service) BeginMFA(ctx context.Context, authorization, realm string) (string, error) {
	var uri string
	err := s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, realm, true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "Facets " + realm, AccountName: a.principal.Email})
		if err != nil {
			return err
		}
		uri = key.URL()
		return accessdb.New(tx).PendingMFA(ctx, accessdb.PendingMFAParams{ID: a.ID(), MfaPending: s.crypto.seal("mfa:"+a.ID(), []byte(key.Secret())), MfaPendingUntil: instant(time.Now().Add(s.policy.TokenTTL))})
	})
	if err != nil {
		return "", err
	}
	return uri, nil
}

func (s *Service) recoveryCodes(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	q := accessdb.New(tx)
	if err := q.InvalidateRecoveryCodes(ctx, id); err != nil {
		return nil, err
	}
	codes := make([]string, 10)
	for i := range codes {
		codes[i] = newSecret()
		if err := q.InsertRecoveryCode(ctx, accessdb.InsertRecoveryCodeParams{PrincipalID: id, Verifier: verifier(codes[i])}); err != nil {
			return nil, err
		}
	}
	return codes, nil
}

func (s *Service) ConfirmMFA(ctx context.Context, authorization, code, realm, origin string) ([]string, error) {
	p, err := s.sessionOwner(ctx, authorization, realm)
	if err != nil {
		return nil, err
	}
	if err := s.limit(ctx, "mfa.confirm", p.ID, origin); err != nil {
		return nil, err
	}
	codes := []string{}
	err = s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, realm, true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		if len(a.principal.MfaPending) == 0 || !time.Now().Before(a.principal.MfaPendingUntil.Time) {
			return ErrInvalid
		}
		secret, err := s.crypto.open("mfa:"+a.ID(), a.principal.MfaPending)
		if err != nil {
			return err
		}
		defer clear(secret)
		step, err := totpStep(string(secret), code, time.Now(), -1)
		if err != nil {
			return err
		}
		q := accessdb.New(tx)
		if err := q.SetMFA(ctx, accessdb.SetMFAParams{ID: a.ID(), MfaSecret: a.principal.MfaPending, MfaStep: step}); err != nil {
			return err
		}
		codes, err = s.recoveryCodes(ctx, tx, a.ID())
		if err != nil {
			return err
		}
		if err := q.RevokeSessions(ctx, a.ID()); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "mfa.changed")
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func (s *Service) DisableMFA(ctx context.Context, authorization string) error {
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, "customer", true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		q := accessdb.New(tx)
		if err := q.SetMFA(ctx, accessdb.SetMFAParams{ID: a.ID(), MfaStep: -1}); err != nil {
			return err
		}
		if err := q.InvalidateRecoveryCodes(ctx, a.ID()); err != nil {
			return err
		}
		if err := q.RevokeSessions(ctx, a.ID()); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "mfa.disabled")
	})
}

func (s *Service) ChangeEmailRequest(ctx context.Context, authorization, email string) error {
	email, err := canonicalEmail(email)
	if err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, "customer", true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		last, err := accessdb.New(tx).LatestToken(ctx, accessdb.LatestTokenParams{PrincipalID: a.ID(), Purpose: "email"})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && time.Since(last.Time) < s.policy.ResendDelay {
			return nil
		}
		if err := s.issueToken(ctx, tx, a.ID(), email, "email"); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "email.change_requested")
	})
}

func (s *Service) ConfirmEmail(ctx context.Context, authorization, token, origin string) error {
	if err := s.limit(ctx, "email.confirm", token, origin); err != nil {
		return err
	}
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, "customer", true)
		if err != nil {
			return err
		}
		if err := s.recent(a); err != nil {
			return err
		}
		q := accessdb.New(tx)
		t, err := q.FindToken(ctx, accessdb.FindTokenParams{Verifier: verifier(token), Purpose: "email"})
		if err != nil {
			return sessionError(err)
		}
		if t.PrincipalID != a.ID() || t.ConsumedAt.Valid || t.InvalidatedAt.Valid || !time.Now().Before(t.ExpiresAt.Time) {
			return ErrUnauthorized
		}
		if err := q.ChangeEmail(ctx, accessdb.ChangeEmailParams{ID: a.ID(), Email: t.TargetEmail}); err != nil {
			return err
		}
		if err := q.ConsumeToken(ctx, t.ID); err != nil {
			return err
		}
		if err := q.InvalidateTokens(ctx, accessdb.InvalidateTokensParams{PrincipalID: a.ID(), Purpose: "recover"}); err != nil {
			return err
		}
		if err := q.RevokeSessions(ctx, a.ID()); err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "email.changed")
	})
}

// ProvisionStaff is reachable only from the privileged offline command, never
// from customer routes. The operator supplies and proves possession of TOTP.
func (s *Service) ProvisionStaff(ctx context.Context, v Credentials, secret, operator string) ([]string, error) {
	if !validText(operator, 128) || !s.crypto.passwordAllowed(v.Password) {
		return nil, ErrInvalid
	}
	if !validEnrollmentSecret(secret) {
		return nil, ErrInvalid
	}
	email, err := canonicalEmail(v.Email)
	if err != nil {
		return nil, err
	}
	step, err := totpStep(secret, v.Code, time.Now(), -1)
	if err != nil {
		return nil, err
	}
	hash, err := s.crypto.hash(ctx, v.Password)
	if err != nil {
		return nil, err
	}
	codes := []string{}
	err = s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		id := uuid.New().String()
		n, err := q.InsertPrincipal(ctx, accessdb.InsertPrincipalParams{ID: id, Realm: "staff", Email: email, PasswordHash: hash,
			DisplayName: "Staff", State: "active", Locale: "en", Zone: "UTC", Currency: "USD"})
		if err != nil {
			return err
		}
		if n != 1 {
			return execution.ErrConflict
		}
		if err := q.SetMFA(ctx, accessdb.SetMFAParams{ID: id, MfaSecret: s.crypto.seal("mfa:"+id, []byte(secret)), MfaStep: step}); err != nil {
			return err
		}
		codes, err = s.recoveryCodes(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.audit(ctx, tx, operator, "platform", "staff.provisioned", id)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

func validEnrollmentSecret(secret string) bool {
	if len(secret) < 32 || len(secret) > 103 || strings.ContainsAny(secret, "\r\n") {
		return false
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	defer clear(decoded)
	return err == nil && len(decoded) >= 20 && len(decoded) <= 64
}
