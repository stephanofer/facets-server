package access

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
)

type CapabilityGrant struct {
	Key     string
	Ceiling int64
}
type EntitlementChange struct {
	SpaceID, Source, Profile, SourceRef, State, Operator string
	ExpectedVersion                                      int64
	ValidFrom, ValidUntil                                time.Time
	Grants                                               []CapabilityGrant
}

// ReplaceEntitlements is an internal transaction participant, not a customer API
// or a payment confirmation. Block 13 must verify the provider event and authorize
// the operator before invoking it in its idempotent transaction. Usage, billing
// responsibility, membership and financial data are not replaced by a plan change.
func (s *Service) ReplaceEntitlements(ctx context.Context, tx pgx.Tx, v EntitlementChange) error {
	if !validText(v.Operator, 128) || !validText(v.Profile, 100) || len(v.Grants) > 100 {
		return ErrInvalid
	}
	if v.Source != "development" && v.Source != "commercial" {
		return ErrInvalid
	}
	if v.Source == "commercial" && !validText(v.SourceRef, 256) {
		return ErrInvalid
	}
	if v.State != "active" && v.State != "restricted" {
		return ErrInvalid
	}
	if !v.ValidUntil.After(v.ValidFrom) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, g := range v.Grants {
		if !validText(g.Key, 80) || g.Ceiling < 0 || seen[g.Key] {
			return ErrInvalid
		}
		seen[g.Key] = true
	}
	q := accessdb.New(tx)
	if _, err := q.GetSpace(ctx, v.SpaceID); err != nil {
		return absent(err)
	}
	n, err := q.ReplaceSpaceAccess(ctx, accessdb.ReplaceSpaceAccessParams{SpaceID: v.SpaceID, Version: v.ExpectedVersion,
		Source: v.Source, Profile: v.Profile, SourceRef: v.SourceRef, State: v.State, ValidFrom: instant(v.ValidFrom), ValidUntil: instant(v.ValidUntil)})
	if err != nil {
		return err
	}
	if err := execution.CheckVersion(n, v.ExpectedVersion); err != nil {
		return err
	}
	if err := q.DisableCapabilities(ctx, v.SpaceID); err != nil {
		return err
	}
	for _, g := range v.Grants {
		if err := q.UpsertCapability(ctx, accessdb.UpsertCapabilityParams{SpaceID: v.SpaceID, Capability: g.Key, Ceiling: g.Ceiling}); err != nil {
			return err
		}
	}
	return s.audit(ctx, tx, v.Operator, v.SpaceID, "entitlements.replaced", v.SourceRef)
}

// ConsumeSpaceCapability shares the caller's authorized action receipt and
// transaction. A rollback or replay must never reserve another unit.
func (s *Service) ConsumeSpaceCapability(ctx context.Context, tx pgx.Tx, a Actor, spaceID, key string, units int64) error {
	if units < 1 {
		return ErrInvalid
	}
	if _, err := s.AuthorizeSpace(ctx, tx, a, spaceID, key); err != nil {
		return err
	}
	return s.capability(ctx, tx, spaceID, key, units)
}
