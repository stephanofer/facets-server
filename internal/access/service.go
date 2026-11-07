package access

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution"
	"golang.org/x/text/language"
)

type Policy struct {
	Key                                                       []byte
	Password                                                  PasswordCost
	PasswordDenylist                                          []string
	SessionIdle, SessionAbsolute, StaffIdle, StaffAbsolute    time.Duration
	TokenTTL, InvitationTTL, ResendDelay, ReauthenticationTTL time.Duration
	AttemptWindow, PublicResponseFloor                        time.Duration
	ActivityInterval                                          time.Duration
	IdentityAttempts, OriginAttempts                          int
	MaxSessions                                               int32
	DevelopmentProfile                                        string
	DevelopmentValidity                                       time.Duration
	DevelopmentMembers                                        int64
}

func DefaultPolicy(key []byte) Policy {
	return Policy{Key: key, Password: PasswordCost{Memory: 19456, Iterations: 2, Parallelism: 1, Concurrency: 2},
		PasswordDenylist: []string{"passwordpassword", "123456789012345", "1234567890123456", "qwertyuiopasdfgh", "iloveyouiloveyou", "correct horse battery staple", "this is a password", "password123456789"},
		SessionIdle:      24 * time.Hour, SessionAbsolute: 30 * 24 * time.Hour, StaffIdle: 15 * time.Minute, StaffAbsolute: 8 * time.Hour,
		TokenTTL: 30 * time.Minute, InvitationTTL: 7 * 24 * time.Hour, ResendDelay: time.Minute, ReauthenticationTTL: 5 * time.Minute,
		AttemptWindow: 5 * time.Minute, IdentityAttempts: 10, OriginAttempts: 100, MaxSessions: 20,
		PublicResponseFloor: 200 * time.Millisecond,
		ActivityInterval:    time.Minute,
		DevelopmentProfile:  "development", DevelopmentValidity: 30 * 24 * time.Hour, DevelopmentMembers: 10}
}

type Service struct {
	store           *execution.Store
	policy          Policy
	crypto          *cryptography
	initializeSpace func(context.Context, pgx.Tx, string) error
}

func New(store *execution.Store, p Policy, initializeSpace ...func(context.Context, pgx.Tx, string) error) (*Service, error) {
	if store == nil || store.DB == nil {
		return nil, ErrInvalid
	}
	if err := p.ValidateRuntime(); err != nil {
		return nil, err
	}
	deny := passwordDenylist(p.PasswordDenylist)
	c, err := newCryptography(p.Key, p.Password, deny)
	if err != nil {
		return nil, err
	}
	if len(initializeSpace) > 1 {
		return nil, ErrInvalid
	}
	s := &Service{store: store, policy: p, crypto: c}
	if len(initializeSpace) == 1 {
		s.initializeSpace = initializeSpace[0]
	}
	return s, nil
}

func (p Policy) ValidateRuntime() error {
	if len(p.Key) != 32 {
		return ErrInvalid
	}
	return p.validate()
}

func (p Policy) validate() error {
	invalidAttempts := p.IdentityAttempts < 1 || p.IdentityAttempts > 100 || p.OriginAttempts < 1 || p.OriginAttempts > 10000
	invalidSessions := p.MaxSessions < 1 || p.MaxSessions > 100
	invalidProfile := len(p.DevelopmentProfile) > 100 || p.DevelopmentMembers < 1 || p.DevelopmentMembers > 10000
	if invalidAttempts || invalidSessions || invalidProfile {
		return ErrInvalid
	}
	for _, d := range []time.Duration{p.SessionIdle, p.SessionAbsolute, p.StaffIdle, p.StaffAbsolute, p.TokenTTL, p.InvitationTTL, p.ResendDelay, p.ReauthenticationTTL, p.AttemptWindow, p.DevelopmentValidity} {
		if d < time.Second || d > 366*24*time.Hour {
			return ErrInvalid
		}
	}
	if p.SessionIdle > p.SessionAbsolute || p.StaffIdle > p.StaffAbsolute || p.ResendDelay >= p.TokenTTL || p.ReauthenticationTTL > p.StaffAbsolute {
		return ErrInvalid
	}
	longCustomerSessions := p.SessionAbsolute > 90*24*time.Hour || p.SessionIdle > 30*24*time.Hour
	longStaffSessions := p.StaffAbsolute > 24*time.Hour || p.StaffIdle > time.Hour
	longChallenges := p.TokenTTL > time.Hour || p.InvitationTTL > 30*24*time.Hour || p.ReauthenticationTTL > 15*time.Minute
	unsafeAttempts := p.ResendDelay < 30*time.Second || p.AttemptWindow < 30*time.Second || p.AttemptWindow > time.Hour
	if longCustomerSessions || longStaffSessions || longChallenges || unsafeAttempts {
		return ErrInvalid
	}
	if !p.Password.valid() || p.PublicResponseFloor < 50*time.Millisecond || p.PublicResponseFloor > time.Second {
		return ErrInvalid
	}
	if p.ActivityInterval < time.Second || p.ActivityInterval >= p.SessionIdle || p.ActivityInterval >= p.StaffIdle {
		return ErrInvalid
	}
	return nil
}

func instant(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func nullable(s string) pgtype.Text          { return pgtype.Text{String: s, Valid: s != ""} }
func canonicalEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 254 {
		return "", ErrInvalid
	}
	for _, r := range value {
		if r < 33 || r > 126 {
			return "", ErrInvalid
		}
	}
	return value, nil
}
func validText(s string, max int) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= max && !strings.ContainsAny(s, "\r\n\x00")
}

func validActionKey(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		upper := c >= 'A' && c <= 'Z'
		lower := c >= 'a' && c <= 'z'
		digit := c >= '0' && c <= '9'
		if !upper && !lower && !digit && !strings.ContainsRune("._:-", c) {
			return false
		}
	}
	return true
}
func validLocale(s string) bool {
	_, err := language.Parse(s)
	return err == nil && len(s) >= 2 && len(s) <= 50
}
func absent(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Rate counters commit independently of the business transaction so failed
// passwords/factors cannot roll back their own abuse budget. No user is suspended.
func (s *Service) limit(ctx context.Context, kind, identity, origin string) error {
	if origin == "" || len(origin) > 128 {
		return ErrInvalid
	}
	now := time.Now()
	exhausted := false
	err := s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		for _, b := range []struct {
			key   string
			limit int
		}{{key: "origin:" + kind + ":" + origin, limit: s.policy.OriginAttempts}, {key: "identity:" + kind + ":" + identity, limit: s.policy.IdentityAttempts}} {
			n, err := q.TakeLimit(ctx, accessdb.TakeLimitParams{Bucket: s.crypto.bucket(b.key), ExpiresAt: instant(now.Add(s.policy.AttemptWindow)), ExpiresAt_2: instant(now)})
			if err != nil {
				return err
			}
			if int(n) > b.limit {
				exhausted = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if exhausted {
		return ErrLimited
	}
	return nil
}

func (s *Service) audit(ctx context.Context, tx pgx.Tx, actor, scope, event, ref string) error {
	return execution.Audit(ctx, tx, actor, scope, event, ref, requestID(ctx))
}

type auditContextKey struct{}

func requestID(ctx context.Context) string { id, _ := ctx.Value(auditContextKey{}).(string); return id }

// MaintainSecrets is a bounded privileged maintenance operation. Receipt and
// membership history remain intact; only expired transient secrets/counters purge.
func (s *Service) MaintainSecrets(ctx context.Context) error {
	return MaintainSecrets(ctx, s.store.DB)
}
func MaintainSecrets(ctx context.Context, db *database.DB) error {
	return db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := accessdb.New(tx)
		if _, err := q.PurgeDeliverySecrets(ctx); err != nil {
			return err
		}
		if _, err := q.PurgePendingMFA(ctx); err != nil {
			return err
		}
		if _, err := q.ScrubTokenRecipients(ctx); err != nil {
			return err
		}
		if _, err := q.ScrubDeliveredRecipients(ctx); err != nil {
			return err
		}
		_, err := q.PurgeLimits(ctx)
		return err
	})
}

func (s *Service) publicDelay(ctx context.Context, start time.Time) {
	remaining := s.policy.PublicResponseFloor - time.Since(start)
	if remaining <= 0 {
		return
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
