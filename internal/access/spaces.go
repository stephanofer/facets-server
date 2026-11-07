package access

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/money"
)

type SpaceSettings struct {
	Name                 string `json:"name" minLength:"1" maxLength:"100"`
	Purpose              string `json:"purpose" enum:"personal,family,group"`
	Currency             string `json:"currency" minLength:"3" maxLength:"3"`
	Zone                 string `json:"zone" minLength:"1" maxLength:"100"`
	Locale               string `json:"locale" minLength:"2" maxLength:"50"`
	PeriodDay            int32  `json:"period_day" minimum:"1" maximum:"31"`
	NewAccountVisibility string `json:"new_account_visibility" enum:"private,space,propose_shared"`
}

func (v SpaceSettings) validate() error {
	if !validText(v.Name, 100) || !validLocale(v.Locale) || v.PeriodDay < 1 || v.PeriodDay > 31 {
		return ErrInvalid
	}
	if v.Purpose != "personal" && v.Purpose != "family" && v.Purpose != "group" {
		return ErrInvalid
	}
	if v.NewAccountVisibility != "private" && v.NewAccountVisibility != "space" && v.NewAccountVisibility != "propose_shared" {
		return ErrInvalid
	}
	if _, err := calendar.ParseZone(v.Zone); err != nil {
		return ErrInvalid
	}
	if _, err := money.ParseCurrency(v.Currency); err != nil {
		return ErrInvalid
	}
	return nil
}

func (s *Service) createSpace(ctx context.Context, tx pgx.Tx, p accessdb.Principal, v SpaceSettings, personal bool) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	if s.policy.DevelopmentProfile == "" {
		return "", ErrCapacity
	}
	q := accessdb.New(tx)
	id, member := uuid.New().String(), uuid.New().String()
	personalFor := ""
	if personal {
		personalFor = p.ID
	}
	if err := q.InsertSpace(ctx, accessdb.InsertSpaceParams{ID: id, Name: v.Name, Purpose: v.Purpose, Currency: v.Currency,
		Zone: v.Zone, Locale: v.Locale, PeriodDay: v.PeriodDay, NewAccountVisibility: v.NewAccountVisibility, PersonalFor: nullable(personalFor)}); err != nil {
		return "", err
	}
	if err := q.InsertMembership(ctx, accessdb.InsertMembershipParams{ID: member, SpaceID: id, PrincipalID: p.ID, Role: "owner"}); err != nil {
		return "", err
	}
	now := time.Now()
	if err := q.InsertSpaceAccess(ctx, accessdb.InsertSpaceAccessParams{SpaceID: id, Profile: s.policy.DevelopmentProfile,
		ValidFrom: instant(now), ValidUntil: instant(now.Add(s.policy.DevelopmentValidity)), BillingMember: member}); err != nil {
		return "", err
	}
	for _, capability := range []string{"read", "manage", "invite", "members", "finance.write"} {
		ceiling, used := int64(1), int64(0)
		if capability == "members" {
			ceiling, used = s.policy.DevelopmentMembers, 1
		}
		if err := q.InsertCapability(ctx, accessdb.InsertCapabilityParams{SpaceID: id, Capability: capability, Ceiling: ceiling, Used: used}); err != nil {
			return "", err
		}
	}
	if s.initializeSpace != nil {
		if err := s.initializeSpace(ctx, tx, id); err != nil {
			return "", err
		}
	}
	return id, s.audit(ctx, tx, p.ID, id, "space.created", id)
}

// AuthorizeSpace must remain in the caller's transaction through commit. The
// space lock serializes authorization, revocations, admissions and entitlement
// changes across processes. Membership IDs identify an episode, never a user ACL.
func (s *Service) AuthorizeSpace(ctx context.Context, tx pgx.Tx, a Actor, spaceID, capability string) (accessdb.Membership, error) {
	if a.principal.Realm != "customer" {
		return accessdb.Membership{}, ErrForbidden
	}
	q := accessdb.New(tx)
	if _, err := q.GetSpace(ctx, spaceID); err != nil {
		return accessdb.Membership{}, absent(err)
	}
	m, err := q.Membership(ctx, accessdb.MembershipParams{SpaceID: spaceID, PrincipalID: a.principal.ID})
	if err != nil {
		return m, absent(err)
	}
	if err := s.capability(ctx, tx, spaceID, capability, 0); err != nil {
		return m, err
	}
	if (capability == "manage" || capability == "invite") && m.Role != "owner" && m.Role != "admin" {
		return m, ErrForbidden
	}
	return m, nil
}

func (s *Service) capability(ctx context.Context, tx pgx.Tx, spaceID, key string, units int64) error {
	q := accessdb.New(tx)
	entitlement, err := q.GetSpaceAccess(ctx, spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCapacity
	}
	if err != nil {
		return err
	}
	now := time.Now()
	if entitlement.State != "active" || now.Before(entitlement.ValidFrom.Time) || !now.Before(entitlement.ValidUntil.Time) {
		return ErrCapacity
	}
	c, err := q.Capability(ctx, accessdb.CapabilityParams{SpaceID: spaceID, Capability: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCapacity
	}
	if err != nil {
		return err
	}
	if units < 0 || c.Ceiling == 0 || units > c.Ceiling-c.Used {
		return ErrCapacity
	}
	if key == "members" && units == 0 && c.Used >= c.Ceiling {
		return ErrCapacity
	}
	if units == 0 {
		return nil
	}
	n, err := q.ConsumeCapability(ctx, accessdb.ConsumeCapabilityParams{SpaceID: spaceID, Capability: key, Used: units})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrCapacity
	}
	return nil
}

type SpaceCommand struct {
	Authorization, SpaceID, Key, Kind, Capability string
	Content                                       any
	replay                                        func(context.Context, pgx.Tx, Actor, accessdb.Membership) error
	authorize                                     func(context.Context, pgx.Tx, Actor, accessdb.Membership, string) error
}

func (s *Service) spaceCommand(ctx context.Context, c SpaceCommand, apply func(context.Context, pgx.Tx, Actor, accessdb.Membership) (string, error)) (string, error) {
	if !validActionKey(c.Key) {
		return "", ErrInvalid
	}
	fp, err := execution.Fingerprint(c.Content)
	if err != nil {
		return "", err
	}
	var actor Actor
	var member accessdb.Membership
	// The actor is resolved from the token, not a client-supplied user identifier.
	p, err := s.sessionOwner(ctx, c.Authorization, "customer")
	if err != nil {
		return "", err
	}
	return s.store.Run(ctx, execution.Action{Actor: p.ID, Scope: c.SpaceID, Kind: c.Kind, Key: c.Key, Fingerprint: fp},
		func(ctx context.Context, tx pgx.Tx, ref string) error {
			var err error
			actor, err = s.authenticate(ctx, tx, c.Authorization, "customer", false)
			if err != nil {
				return err
			}
			member, err = s.AuthorizeSpace(ctx, tx, actor, c.SpaceID, c.Capability)
			if err != nil {
				return err
			}
			if c.authorize != nil {
				if err := c.authorize(ctx, tx, actor, member, ref); err != nil {
					return err
				}
			}
			if ref != "" && c.replay != nil {
				return c.replay(ctx, tx, actor, member)
			}
			return nil
		}, func(ctx context.Context, tx pgx.Tx) (string, error) { return apply(ctx, tx, actor, member) })
}

func (s *Service) CreateSpace(ctx context.Context, authorization, key string, v SpaceSettings) (string, error) {
	if !validActionKey(key) {
		return "", ErrInvalid
	}
	p, err := s.sessionOwner(ctx, authorization, "customer")
	if err != nil {
		return "", err
	}
	fp, err := execution.Fingerprint(v)
	if err != nil {
		return "", err
	}
	var a Actor
	return s.store.Run(ctx, execution.Action{Actor: p.ID, Scope: "identity:" + p.ID, Kind: "space.create", Key: key, Fingerprint: fp},
		func(ctx context.Context, tx pgx.Tx, ref string) error {
			var err error
			a, err = s.authenticate(ctx, tx, authorization, "customer", false)
			if err != nil {
				return err
			}
			if ref != "" {
				_, err = s.AuthorizeSpace(ctx, tx, a, ref, "read")
			}
			return err
		}, func(ctx context.Context, tx pgx.Tx) (string, error) {
			return s.createSpace(ctx, tx, a.principal, v, false)
		})
}

func (s *Service) EditSpace(ctx context.Context, c SpaceCommand, v SpaceSettings, version int64) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability = "space.edit", "manage"
	c.Content = struct {
		Settings SpaceSettings
		Version  int64
	}{Settings: v, Version: version}
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, _ accessdb.Membership) (string, error) {
		n, err := accessdb.New(tx).EditSpace(ctx, accessdb.EditSpaceParams{ID: c.SpaceID, Version: version, Name: v.Name, Purpose: v.Purpose,
			Currency: v.Currency, Zone: v.Zone, Locale: v.Locale, PeriodDay: v.PeriodDay, NewAccountVisibility: v.NewAccountVisibility})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, version); err != nil {
			return "", err
		}
		return c.SpaceID, s.audit(ctx, tx, a.principal.ID, c.SpaceID, c.Kind, c.SpaceID)
	})
}

// ResourcePermission is a deterministic policy shared by future resource query
// predicates. Space administration is deliberately NOT a financial permission.
func ResourcePermission(role, permission string, explicitRead, explicitOperate, explicitAdmin bool) bool {
	if role != "owner" && role != "admin" && role != "member" && role != "reader" {
		return false
	}
	switch permission {
	case "read":
		return explicitRead
	case "operate":
		return role != "reader" && explicitRead && explicitOperate
	case "administer":
		return role != "reader" && explicitRead && explicitAdmin
	default:
		return false
	}
}

func inviteRoleAllowed(inviter, role string) bool {
	if role != "admin" && role != "member" && role != "reader" {
		return false
	}
	return inviter == "owner" || (inviter == "admin" && role != "admin")
}

// A membership's identity never resurrects on rejoining. Epochs invalidate
// derived artifacts; modules added later also record their sensitive dependencies.
func (s *Service) RemoveMember(ctx context.Context, c SpaceCommand, target string) (string, error) {
	c.Kind, c.Capability, c.Content = "member.remove", "read", target
	c.replay = func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) error {
		if err := s.recent(a); err != nil {
			return err
		}
		other, err := accessdb.New(tx).MembershipEpisode(ctx, accessdb.MembershipEpisodeParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return absent(err)
		}
		if other.ID == m.ID {
			return nil
		}
		if _, err := s.AuthorizeSpace(ctx, tx, a, c.SpaceID, "manage"); err != nil {
			return err
		}
		if m.Role != "owner" && (m.Role != "admin" || other.Role == "admin") {
			return ErrForbidden
		}
		return nil
	}
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		if err := s.recent(a); err != nil {
			return "", err
		}
		q := accessdb.New(tx)
		other, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return "", absent(err)
		}
		if other.Role == "owner" {
			return "", ErrResponsibility
		}
		self := other.ID == m.ID
		if !self {
			if err := s.capability(ctx, tx, c.SpaceID, "manage", 0); err != nil {
				return "", err
			}
		}
		if !self && (m.Role != "owner" && (m.Role != "admin" || other.Role == "admin")) {
			return "", ErrForbidden
		}
		rights, err := q.GetSpaceAccess(ctx, c.SpaceID)
		if err != nil {
			return "", err
		}
		if rights.BillingMember == other.ID {
			return "", ErrResponsibility
		}
		if err := q.EndMembership(ctx, other.ID); err != nil {
			return "", err
		}
		n, err := q.ReleaseMember(ctx, c.SpaceID)
		if err != nil {
			return "", err
		}
		if n != 1 {
			return "", errors.New("membership consumption inconsistent")
		}
		if err := q.RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return target, s.audit(ctx, tx, a.principal.ID, c.SpaceID, c.Kind, target)
	})
}

func (s *Service) TransferOwnership(ctx context.Context, c SpaceCommand, target string) (string, error) {
	c.Kind, c.Capability, c.Content = "space.transfer", "manage", target
	c.replay = func(_ context.Context, _ pgx.Tx, a Actor, _ accessdb.Membership) error { return s.recent(a) }
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		if err := s.recent(a); err != nil {
			return "", err
		}
		if m.Role != "owner" || target == m.ID {
			return "", ErrForbidden
		}
		q := accessdb.New(tx)
		other, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return "", absent(err)
		}
		if err := q.Role(ctx, accessdb.RoleParams{ID: m.ID, Role: "admin"}); err != nil {
			return "", err
		}
		if err := q.Role(ctx, accessdb.RoleParams{ID: other.ID, Role: "owner"}); err != nil {
			return "", err
		}
		if err := q.RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return target, s.audit(ctx, tx, a.principal.ID, c.SpaceID, c.Kind, target)
	})
}

func (s *Service) ClaimBilling(ctx context.Context, c SpaceCommand) (string, error) {
	c.Kind, c.Capability, c.Content = "billing.claim", "manage", struct{ Confirm bool }{Confirm: true}
	c.replay = func(_ context.Context, _ pgx.Tx, a Actor, _ accessdb.Membership) error { return s.recent(a) }
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		if err := s.recent(a); err != nil {
			return "", err
		}
		// This claim does not inherit another person's payment mandate or payment data.
		if err := accessdb.New(tx).ClaimBilling(ctx, accessdb.ClaimBillingParams{SpaceID: c.SpaceID, BillingMember: m.ID}); err != nil {
			return "", err
		}
		return m.ID, s.audit(ctx, tx, a.principal.ID, c.SpaceID, c.Kind, m.ID)
	})
}

func validateCursor(cursor string, limit int32) error {
	if limit < 1 || limit > 100 {
		return ErrInvalid
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return ErrInvalid
		}
	}
	return nil
}

func (s *Service) ChangeRole(ctx context.Context, c SpaceCommand, target, role string) (string, error) {
	c.Kind, c.Capability, c.Content = "member.role", "manage", struct{ Member, Role string }{Member: target, Role: role}
	c.replay = func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) error {
		if err := s.recent(a); err != nil {
			return err
		}
		if !inviteRoleAllowed(m.Role, role) {
			return ErrForbidden
		}
		other, err := accessdb.New(tx).GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return absent(err)
		}
		if other.Role == "owner" || (m.Role != "owner" && other.Role == "admin") {
			return ErrForbidden
		}
		return nil
	}
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		if err := s.recent(a); err != nil {
			return "", err
		}
		if !inviteRoleAllowed(m.Role, role) {
			return "", ErrForbidden
		}
		q := accessdb.New(tx)
		other, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: target})
		if err != nil {
			return "", absent(err)
		}
		if other.Role == "owner" || (m.Role != "owner" && other.Role == "admin") {
			return "", ErrForbidden
		}
		if err := q.Role(ctx, accessdb.RoleParams{ID: target, Role: role}); err != nil {
			return "", err
		}
		if err := q.RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return target, s.audit(ctx, tx, a.ID(), c.SpaceID, c.Kind, target)
	})
}
