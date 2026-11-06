package access

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
)

type Profile struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Locale        string `json:"locale"`
	Zone          string `json:"zone"`
	Notifications bool   `json:"notifications"`
	MFA           bool   `json:"mfa_enabled"`
	Version       int64  `json:"version"`
}
type Preferences struct {
	Name          string `json:"name" minLength:"1" maxLength:"100"`
	Locale        string `json:"locale" minLength:"2" maxLength:"50"`
	Zone          string `json:"zone" minLength:"1" maxLength:"100"`
	Notifications bool   `json:"notifications"`
	Version       int64  `json:"version" minimum:"1"`
}
type SpaceView struct {
	ID           string        `json:"id"`
	Settings     SpaceSettings `json:"settings"`
	Role         string        `json:"role"`
	MembershipID string        `json:"membership_id"`
	Version      int64         `json:"version"`
	AccessEpoch  int64         `json:"access_epoch"`
}

func spaceView(v accessdb.Space, m accessdb.Membership) SpaceView {
	return SpaceView{ID: v.ID, Settings: SpaceSettings{Name: v.Name, Purpose: v.Purpose, Currency: v.Currency, Zone: v.Zone,
		Locale: v.Locale, PeriodDay: v.PeriodDay, NewAccountVisibility: v.NewAccountVisibility}, Role: m.Role, MembershipID: m.ID,
		Version: v.Version, AccessEpoch: v.AccessEpoch}
}

type CapabilityView struct {
	Key     string `json:"key"`
	Ceiling string `json:"ceiling"`
	Used    string `json:"used"`
}
type AccessView struct {
	Source        string           `json:"source"`
	Profile       string           `json:"profile"`
	State         string           `json:"state"`
	ValidFrom     time.Time        `json:"valid_from"`
	ValidUntil    time.Time        `json:"valid_until"`
	BillingMember string           `json:"billing_member"`
	Version       int64            `json:"version"`
	Capabilities  []CapabilityView `json:"capabilities"`
}
type SessionView struct {
	ID        string    `json:"id"`
	Device    string    `json:"device"`
	CreatedAt time.Time `json:"created_at"`
	SeenAt    time.Time `json:"seen_at"`
	ExpiresAt time.Time `json:"expires_at"`
}
type MemberView struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Name string `json:"name"`
}
type InvitationView struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	Role       string    `json:"role"`
	State      string    `json:"state"`
	ExpiresAt  time.Time `json:"expires_at"`
	Generation int32     `json:"generation"`
}
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

type DeparturePreview struct {
	MembershipID              string `json:"membership_id"`
	RequiresOwnershipTransfer bool   `json:"requires_ownership_transfer"`
	RequiresBillingResolution bool   `json:"requires_billing_resolution"`
	RevokesSpaceAccess        bool   `json:"revokes_space_access"`
	PreservesOtherMemberships bool   `json:"preserves_other_memberships"`
	PreservesHistory          bool   `json:"preserves_history"`
}

func (s *Service) Departure(ctx context.Context, authorization, id string) (DeparturePreview, error) {
	var result DeparturePreview
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		m, err := s.AuthorizeSpace(ctx, tx, a, id, "read")
		if err != nil {
			return err
		}
		rights, err := accessdb.New(tx).GetSpaceAccess(ctx, id)
		if err != nil {
			return err
		}
		result = DeparturePreview{MembershipID: m.ID, RequiresOwnershipTransfer: m.Role == "owner", RequiresBillingResolution: rights.BillingMember == m.ID,
			RevokesSpaceAccess: true, PreservesOtherMemberships: true, PreservesHistory: true}
		return nil
	})
	return result, err
}

func profile(a Actor) Profile {
	p := a.principal
	return Profile{ID: p.ID, Email: p.Email, Name: p.DisplayName, Locale: p.Locale, Zone: p.Zone, Notifications: p.Notifications,
		MFA: len(p.MfaSecret) > 0, Version: p.Version}
}

func (s *Service) withActor(ctx context.Context, authorization, realm string, exclusive bool, fn func(context.Context, pgx.Tx, Actor) error) error {
	return s.store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := s.authenticate(ctx, tx, authorization, realm, exclusive)
		if err != nil {
			return err
		}
		return fn(ctx, tx, a)
	})
}
func (s *Service) Profile(ctx context.Context, authorization, realm string) (Profile, error) {
	var v Profile
	err := s.withActor(ctx, authorization, realm, false, func(_ context.Context, _ pgx.Tx, a Actor) error { v = profile(a); return nil })
	return v, err
}
func (s *Service) Preferences(ctx context.Context, authorization string, v Preferences) error {
	settings := SpaceSettings{Name: v.Name, Purpose: "personal", Currency: "USD", Zone: v.Zone, Locale: v.Locale, PeriodDay: 1, NewAccountVisibility: "private"}
	if err := settings.validate(); err != nil {
		return err
	}
	return s.withActor(ctx, authorization, "customer", true, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		n, err := accessdb.New(tx).Preferences(ctx, accessdb.PreferencesParams{ID: a.ID(), Version: v.Version,
			DisplayName: v.Name, Locale: v.Locale, Zone: v.Zone, Notifications: v.Notifications})
		if err != nil {
			return err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return err
		}
		return s.audit(ctx, tx, a.ID(), "identity:"+a.ID(), "preferences.updated", a.ID())
	})
}
func (s *Service) RevokeSession(ctx context.Context, authorization, realm, id string, all bool) error {
	return s.withActor(ctx, authorization, realm, true, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		if all || (id != "" && id != a.session.ID) {
			if err := s.recent(a); err != nil {
				return err
			}
		}
		if id == "" {
			id = a.session.ID
		}
		q := accessdb.New(tx)
		if all {
			if err := q.RevokeSessions(ctx, a.ID()); err != nil {
				return err
			}
		} else {
			if _, err := q.RevokeSession(ctx, accessdb.RevokeSessionParams{PrincipalID: a.ID(), ID: id}); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, a.ID(), "identity:"+a.ID(), "session.revoked", id)
	})
}
func (s *Service) Sessions(ctx context.Context, authorization, realm string) ([]SessionView, error) {
	items := []SessionView{}
	err := s.withActor(ctx, authorization, realm, false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		idle, _ := s.lifetimes(realm)
		now := time.Now()
		rows, err := accessdb.New(tx).ListSessions(ctx, accessdb.ListSessionsParams{PrincipalID: a.ID(), ExpiresAt: instant(now),
			SeenAt: instant(now.Add(-idle)), Limit: s.policy.MaxSessions})
		if err != nil {
			return err
		}
		for _, v := range rows {
			items = append(items, SessionView{ID: v.ID, Device: v.Device, CreatedAt: v.CreatedAt.Time, SeenAt: v.SeenAt.Time, ExpiresAt: v.ExpiresAt.Time})
		}
		return nil
	})
	return items, err
}
func (s *Service) RegenerateRecoveryCodes(ctx context.Context, authorization, realm string) ([]string, error) {
	codes := []string{}
	err := s.withActor(ctx, authorization, realm, true, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		if err := s.recent(a); err != nil {
			return err
		}
		if len(a.principal.MfaSecret) == 0 {
			return ErrInvalid
		}
		var err error
		codes, err = s.recoveryCodes(ctx, tx, a.ID())
		if err != nil {
			return err
		}
		return s.securityNotice(ctx, tx, a.principal, "mfa.recovery_regenerated")
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}
func (s *Service) Spaces(ctx context.Context, authorization, cursor string, limit int32) (Page[SpaceView], error) {
	result := Page[SpaceView]{Items: []SpaceView{}}
	if err := validateCursor(cursor, limit); err != nil {
		return result, err
	}
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		// Use the application clock, as creation and capability authorization do.
		rows, err := accessdb.New(tx).ListSpaces(ctx, accessdb.ListSpacesParams{PrincipalID: a.ID(), ID: cursor, AsOf: instant(time.Now()), Limit: limit + 1})
		if err != nil {
			return err
		}
		for i, v := range rows {
			if i == int(limit) {
				result.NextCursor = result.Items[i-1].ID
				break
			}
			result.Items = append(result.Items, spaceView(accessdb.Space{ID: v.ID, Name: v.Name, Purpose: v.Purpose, Currency: v.Currency, Zone: v.Zone,
				Locale: v.Locale, PeriodDay: v.PeriodDay, NewAccountVisibility: v.NewAccountVisibility, Version: v.Version, AccessEpoch: v.AccessEpoch},
				accessdb.Membership{ID: v.MembershipID, Role: v.Role}))
		}
		return nil
	})
	return result, err
}
func (s *Service) Space(ctx context.Context, authorization, id string) (SpaceView, error) {
	var result SpaceView
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		m, err := s.AuthorizeSpace(ctx, tx, a, id, "read")
		if err != nil {
			return err
		}
		v, err := accessdb.New(tx).GetSpace(ctx, id)
		if err != nil {
			return err
		}
		result = spaceView(v, m)
		return nil
	})
	return result, err
}
