package access

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
)

// AuthenticateCustomer is a transaction participant for subsequent modules.
// Retain this transaction through authorization, writes and commit; an Actor
// copied from a previous request is not evidence of current identity or access.
func (s *Service) AuthenticateCustomer(ctx context.Context, tx pgx.Tx, authorization string) (Actor, error) {
	return s.authenticate(ctx, tx, authorization, "customer", false)
}

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
