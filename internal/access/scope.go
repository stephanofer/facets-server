package access

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
)

// AuthorizeNotificationRecipient validates a persisted membership episode without
// a session. It grants no financial write authority and holds current identity,
// preferences and space permissions until the caller commits its durable inbox.
func (s *Service) AuthorizeNotificationRecipient(ctx context.Context, tx pgx.Tx, space, member string) (accessdb.Membership, error) {
	q := accessdb.New(tx)
	m, err := q.GetMember(ctx, accessdb.GetMemberParams{SpaceID: space, ID: member})
	if err != nil {
		return m, absent(err)
	}
	p, err := q.ReadPrincipal(ctx, m.PrincipalID)
	if err != nil {
		return m, absent(err)
	}
	if p.State != "active" || p.Realm != "customer" || !p.Notifications {
		return m, ErrForbidden
	}
	current, err := s.AuthorizeSpace(ctx, tx, Actor{principal: p}, space, "read")
	if err != nil {
		return m, err
	}
	if current.ID != member {
		return m, ErrForbidden
	}
	return current, nil
}

// AuthenticateCustomer is a transaction participant for subsequent modules.
// Retain this transaction through authorization, writes and commit; an Actor
// copied from a previous request is not evidence of current identity or access.
func (s *Service) AuthenticateCustomer(ctx context.Context, tx pgx.Tx, authorization string) (Actor, error) {
	return s.authenticate(ctx, tx, authorization, "customer", false)
}

// RunSpaceCommand keeps identity, capabilities, resource authorization and the
// action receipt in the same transaction. Resource checks run again on replay.
func (s *Service) RunSpaceCommand(ctx context.Context, c SpaceCommand,
	authorize func(context.Context, pgx.Tx, Actor, accessdb.Membership, string) error,
	apply func(context.Context, pgx.Tx, Actor, accessdb.Membership) (string, error),
) (string, error) {
	c.authorize = authorize
	return s.spaceCommand(ctx, c, apply)
}

// ReadSpace checks current identity and membership under the same space lock used
// by revocations. Callers must apply resource predicates before pagination.
func (s *Service) ReadSpace(ctx context.Context, authorization, spaceID string,
	fn func(context.Context, pgx.Tx, Actor, accessdb.Membership) error,
) error {
	return s.withActor(ctx, authorization, "customer", false, func(ctx context.Context, tx pgx.Tx, a Actor) error {
		m, err := s.AuthorizeSpace(ctx, tx, a, spaceID, "read")
		if err != nil {
			return err
		}
		return fn(ctx, tx, a, m)
	})
}

// RequireRecent verifies the authenticated transaction participant, not a client timestamp.
func (s *Service) RequireRecent(a Actor) error { return s.recent(a) }

type ScopeSnapshot struct {
	SpaceID, MembershipID string
	Epoch                 int64
}

// CaptureScope complements resource-specific dependency identities. It never
// substitutes for the account/resource predicates applied BEFORE SQL aggregation.
func (s *Service) CaptureScope(ctx context.Context, tx pgx.Tx, a Actor, spaceID string) (ScopeSnapshot, error) {
	m, err := s.AuthorizeSpace(ctx, tx, a, spaceID, "read")
	if err != nil {
		return ScopeSnapshot{}, err
	}
	v, err := accessdb.New(tx).GetSpace(ctx, spaceID)
	if err != nil {
		return ScopeSnapshot{}, err
	}
	return ScopeSnapshot{SpaceID: spaceID, MembershipID: m.ID, Epoch: v.AccessEpoch}, nil
}

// RevalidateScope blocks an old artifact after a scope change, even if its owner
// subsequently rejoins. Regeneration must also recheck all sensitive resources.
func (s *Service) RevalidateScope(ctx context.Context, tx pgx.Tx, a Actor, snapshot ScopeSnapshot) error {
	current, err := s.CaptureScope(ctx, tx, a, snapshot.SpaceID)
	if err != nil {
		return err
	}
	if current.MembershipID != snapshot.MembershipID || current.Epoch != snapshot.Epoch {
		return ErrNotFound
	}
	return nil
}
