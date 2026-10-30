package access

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
)

type Invite struct {
	Email string `json:"email" minLength:"3" maxLength:"254"`
	Role  string `json:"role" enum:"admin,member,reader"`
}

func (s *Service) Invite(ctx context.Context, c SpaceCommand, v Invite) (string, error) {
	email, err := canonicalEmail(v.Email)
	if err != nil {
		return "", err
	}
	v.Email = email
	c.Kind, c.Capability, c.Content = "invitation.create", "invite", v
	c.replay = func(_ context.Context, _ pgx.Tx, _ Actor, m accessdb.Membership) error {
		if !inviteRoleAllowed(m.Role, v.Role) {
			return ErrForbidden
		}
		return nil
	}
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		if !inviteRoleAllowed(m.Role, v.Role) {
			return "", ErrForbidden
		}
		if email == a.principal.Email {
			return "", ErrInvalid
		}
		if err := s.capability(ctx, tx, c.SpaceID, "members", 0); err != nil {
			return "", err
		}
		q := accessdb.New(tx)
		if err := q.ExpireInvitations(ctx, c.SpaceID); err != nil {
			return "", err
		}
		id, token := uuid.New().String(), newSecret()
		expiry := time.Now().Add(s.policy.InvitationTTL)
		if err := q.InsertInvitation(ctx, accessdb.InsertInvitationParams{ID: id, SpaceID: c.SpaceID, Inviter: m.ID, Email: email, Role: v.Role,
			Verifier: verifier(token), ExpiresAt: instant(expiry)}); err != nil {
			return "", err
		}
		if err := s.enqueueMail(ctx, tx, mailIntent{Kind: "invitation", Ref: id, Email: email, Secret: token, Expiry: expiry}); err != nil {
			return "", err
		}
		return id, s.audit(ctx, tx, a.ID(), c.SpaceID, c.Kind, id)
	})
}

func (s *Service) ResendInvitation(ctx context.Context, c SpaceCommand, id string) (string, error) {
	c.Kind, c.Capability, c.Content = "invitation.resend", "invite", id
	c.replay = s.invitationReplay(c.SpaceID, id)
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		q := accessdb.New(tx)
		i, err := q.GetInvitation(ctx, accessdb.GetInvitationParams{SpaceID: c.SpaceID, ID: id})
		if err != nil {
			return "", absent(err)
		}
		if i.State != "pending" || !time.Now().Before(i.ExpiresAt.Time) {
			return "", ErrInvalid
		}
		if !inviteRoleAllowed(m.Role, i.Role) {
			return "", ErrForbidden
		}
		if time.Since(i.SentAt.Time) < s.policy.ResendDelay {
			return "", ErrLimited
		}
		if err := s.capability(ctx, tx, c.SpaceID, "members", 0); err != nil {
			return "", err
		}
		// Preserve the original inviter; another administrator cannot resurrect an
		// invitation whose actual grantor has lost authorization.
		inviter, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: i.Inviter})
		if err != nil {
			return "", absent(err)
		}
		if !inviteRoleAllowed(inviter.Role, i.Role) {
			return "", ErrForbidden
		}
		token := newSecret()
		expiry := time.Now().Add(s.policy.InvitationTTL)
		if err := q.ResendInvitation(ctx, accessdb.ResendInvitationParams{ID: id, Verifier: verifier(token), ExpiresAt: instant(expiry)}); err != nil {
			return "", err
		}
		if err := s.enqueueMail(ctx, tx, mailIntent{Kind: "invitation", Ref: id, Email: i.Email, Secret: token, Expiry: expiry}); err != nil {
			return "", err
		}
		return id, s.audit(ctx, tx, a.ID(), c.SpaceID, c.Kind, id)
	})
}

func (s *Service) CancelInvitation(ctx context.Context, c SpaceCommand, id string) (string, error) {
	c.Kind, c.Capability, c.Content = "invitation.cancel", "invite", id
	c.replay = s.invitationReplay(c.SpaceID, id)
	return s.spaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a Actor, m accessdb.Membership) (string, error) {
		q := accessdb.New(tx)
		i, err := q.GetInvitation(ctx, accessdb.GetInvitationParams{SpaceID: c.SpaceID, ID: id})
		if err != nil {
			return "", absent(err)
		}
		if !inviteRoleAllowed(m.Role, i.Role) {
			return "", ErrForbidden
		}
		if i.State != "pending" {
			return "", ErrInvalid
		}
		if err := q.InvitationState(ctx, accessdb.InvitationStateParams{ID: id, State: "cancelled"}); err != nil {
			return "", err
		}
		return id, s.audit(ctx, tx, a.ID(), c.SpaceID, c.Kind, id)
	})
}

type InvitationDecision struct {
	Token                     string `json:"token" minLength:"43" maxLength:"43"`
	Accept                    bool   `json:"accept"`
	AcknowledgeSharedAccounts bool   `json:"acknowledge_shared_accounts"`
}

func (s *Service) DecideInvitation(ctx context.Context, authorization, key string, v InvitationDecision) (string, error) {
	if !validActionKey(key) {
		return "", ErrInvalid
	}
	if len(v.Token) != 43 || (v.Accept && !v.AcknowledgeSharedAccounts) {
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
	var invitation accessdb.Invitation
	return s.store.Run(ctx, execution.Action{Actor: p.ID, Scope: "identity:" + p.ID, Kind: "invitation.decide", Key: key, Fingerprint: fp},
		func(ctx context.Context, tx pgx.Tx, ref string) error {
			var err error
			a, err = s.authenticate(ctx, tx, authorization, "customer", false)
			if err != nil {
				return err
			}
			q := accessdb.New(tx)
			initial, err := q.FindInvitation(ctx, verifier(v.Token))
			if err != nil {
				return absent(err)
			}
			if _, err := q.GetSpace(ctx, initial.SpaceID); err != nil {
				return absent(err)
			}
			invitation, err = q.GetInvitation(ctx, accessdb.GetInvitationParams{SpaceID: initial.SpaceID, ID: initial.ID})
			if err != nil {
				return absent(err)
			}
			if invitation.Email != a.principal.Email {
				return ErrNotFound
			}
			if invitation.State == "accepted" {
				m, err := q.Membership(ctx, accessdb.MembershipParams{SpaceID: invitation.SpaceID, PrincipalID: a.ID()})
				if err != nil {
					return absent(err)
				}
				if m.ID != invitation.AcceptedMember.String {
					return ErrNotFound
				}
				return s.capability(ctx, tx, invitation.SpaceID, "read", 0)
			}
			return nil
		}, func(ctx context.Context, tx pgx.Tx) (string, error) {
			q := accessdb.New(tx)
			i := invitation
			if i.State == "accepted" {
				if !v.Accept {
					return "", ErrInvalid
				}
				m, err := q.Membership(ctx, accessdb.MembershipParams{SpaceID: i.SpaceID, PrincipalID: a.ID()})
				if err != nil {
					return "", absent(err)
				}
				if m.ID != i.AcceptedMember.String {
					return "", ErrNotFound
				}
				return m.ID, nil
			}
			if i.State != "pending" || !time.Now().Before(i.ExpiresAt.Time) {
				return "", ErrInvalid
			}
			if !v.Accept {
				if err := q.InvitationState(ctx, accessdb.InvitationStateParams{ID: i.ID, State: "rejected"}); err != nil {
					return "", err
				}
				return i.ID, s.audit(ctx, tx, a.ID(), i.SpaceID, "invitation.rejected", i.ID)
			}
			inviter, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: i.SpaceID, ID: i.Inviter})
			if err != nil {
				return "", absent(err)
			}
			// The sender's membership and identity must both remain effective.
			sender, err := q.ReadPrincipal(ctx, inviter.PrincipalID)
			if err != nil {
				return "", err
			}
			if sender.State != "active" || !inviteRoleAllowed(inviter.Role, i.Role) {
				return "", ErrForbidden
			}
			if _, err := q.Membership(ctx, accessdb.MembershipParams{SpaceID: i.SpaceID, PrincipalID: a.ID()}); err == nil {
				return "", execution.ErrConflict
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return "", err
			}
			if err := s.capability(ctx, tx, i.SpaceID, "invite", 0); err != nil {
				return "", err
			}
			if err := s.capability(ctx, tx, i.SpaceID, "members", 1); err != nil {
				return "", err
			}
			member := uuid.New().String()
			if err := q.InsertMembership(ctx, accessdb.InsertMembershipParams{ID: member, SpaceID: i.SpaceID, PrincipalID: a.ID(), Role: i.Role}); err != nil {
				return "", err
			}
			if err := q.InvitationState(ctx, accessdb.InvitationStateParams{ID: i.ID, State: "accepted", AcceptedMember: nullable(member)}); err != nil {
				return "", err
			}
			return member, s.audit(ctx, tx, a.ID(), i.SpaceID, "invitation.accepted", i.ID)
		})
}

func (s *Service) invitationReplay(spaceID, id string) func(context.Context, pgx.Tx, Actor, accessdb.Membership) error {
	return func(ctx context.Context, tx pgx.Tx, _ Actor, m accessdb.Membership) error {
		i, err := accessdb.New(tx).GetInvitation(ctx, accessdb.GetInvitationParams{SpaceID: spaceID, ID: id})
		if err != nil {
			return absent(err)
		}
		if !inviteRoleAllowed(m.Role, i.Role) {
			return ErrForbidden
		}
		return nil
	}
}

type InvitationPreview struct {
	SpaceID                       string    `json:"space_id"`
	SpaceName                     string    `json:"space_name"`
	Role                          string    `json:"role"`
	State                         string    `json:"state"`
	ExpiresAt                     time.Time `json:"expires_at"`
	GrantsSpaceSharedAccounts     bool      `json:"grants_space_shared_accounts"`
	SharesExistingPrivateAccounts bool      `json:"shares_existing_private_accounts"`
}

func (s *Service) PreviewInvitation(ctx context.Context, authorization, token string) (InvitationPreview, error) {
	var result InvitationPreview
	if len(token) != 43 {
		return result, ErrInvalid
	}
	err := s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		q := accessdb.New(tx)
		i, err := q.FindInvitation(ctx, verifier(token))
		if err != nil {
			return absent(err)
		}
		if i.Email != a.principal.Email {
			return ErrNotFound
		}
		space, err := q.GetSpace(ctx, i.SpaceID)
		if err != nil {
			return absent(err)
		}
		if i.State == "accepted" {
			m, err := s.AuthorizeSpace(ctx, tx, a, i.SpaceID, "read")
			if err != nil {
				return err
			}
			if m.ID != i.AcceptedMember.String {
				return ErrNotFound
			}
		} else {
			if i.State != "pending" || !time.Now().Before(i.ExpiresAt.Time) {
				return ErrNotFound
			}
			inviter, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: i.SpaceID, ID: i.Inviter})
			if err != nil {
				return absent(err)
			}
			sender, err := q.ReadPrincipal(ctx, inviter.PrincipalID)
			if err != nil {
				return err
			}
			if sender.State != "active" || !inviteRoleAllowed(inviter.Role, i.Role) {
				return ErrNotFound
			}
			if err := s.capability(ctx, tx, i.SpaceID, "invite", 0); err != nil {
				return err
			}
		}
		result = InvitationPreview{SpaceID: space.ID, SpaceName: space.Name, Role: i.Role, State: i.State, ExpiresAt: i.ExpiresAt.Time, GrantsSpaceSharedAccounts: true}
		return nil
	})
	return result, err
}
