package finance

import (
	"context"
	"errors"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/money"
)

// PlanningTarget supplies currency only when no financial account is assigned.
// Such resources always start private; sharing is a separate confirmed action.
type PlanningTarget struct {
	AccountID string `json:"account_id,omitempty" format:"uuid"`
	Currency  string `json:"currency,omitempty" minLength:"3" maxLength:"3"`
}

func (v PlanningTarget) validate() error {
	if v.AccountID != "" {
		if _, err := uuid.Parse(v.AccountID); err != nil || v.Currency != "" {
			return access.ErrInvalid
		}
		return nil
	}
	if _, err := money.ParseCurrency(v.Currency); err != nil {
		return access.ErrInvalid
	}
	return nil
}

func (s *Service) planningCurrency(ctx context.Context, tx pgx.Tx, actor access.Actor, space string, v PlanningTarget) (string, int32, error) {
	if v.AccountID != "" {
		a, err := s.AuthorizeAccount(ctx, tx, actor, space, v.AccountID, "operate")
		return a.Currency, a.Scale, err
	}
	currency, err := money.ParseCurrency(v.Currency)
	if err != nil {
		return "", 0, access.ErrInvalid
	}
	return v.Currency, currency.Scale(), nil
}

func createPlanningResource(ctx context.Context, tx pgx.Tx, space, id, member, author string) error {
	q := financedb.New(tx)
	if err := q.InsertPlanningResource(ctx, financedb.InsertPlanningResourceParams{ID: id, SpaceID: space, ResponsibleMember: member}); err != nil {
		return err
	}
	return q.CapturePlanningAccess(ctx, financedb.CapturePlanningAccessParams{SpaceID: space, ID: id, Author: author, Reason: "Created privately"})
}

// Financial-account permissions remain authoritative for assigned resources.
// Accountless resources distinguish consultation from administration: only the
// current responsible member may edit, generate or apply payments.
func (s *Service) authorizePlanningWrite(ctx context.Context, tx pgx.Tx, actor access.Actor, member accessdb.Membership, space, account, resource string) error {
	if account != "" {
		_, err := s.AuthorizeAccount(ctx, tx, actor, space, account, "operate")
		return err
	}
	p, err := financedb.New(tx).PlanningResource(ctx, financedb.PlanningResourceParams{SpaceID: space, ID: resource, MemberID: member.ID})
	if err != nil {
		return absent(err)
	}
	if member.Role == "reader" || !p.ResponsibleActive || p.PlanningResource.ResponsibleMember != member.ID {
		return access.ErrForbidden
	}
	return nil
}

type PlanningAccessView struct {
	ID                  string   `json:"id"`
	ResponsibleMemberID string   `json:"responsible_member_id"`
	Visibility          string   `json:"visibility" enum:"private,space,selected"`
	Readers             []string `json:"readers"`
	Version             int64    `json:"version"`
}

type PlanningAccessEdit struct {
	FinancialVersionConfirmation
	ResponsibleMemberID string   `json:"responsible_member_id" format:"uuid"`
	Visibility          string   `json:"visibility" enum:"private,space,selected"`
	Readers             []string `json:"readers" maxItems:"100"`
	Reason              string   `json:"reason" minLength:"1" maxLength:"500"`
}

func (s *Service) PlanningAccess(ctx context.Context, authorization, space, id string) (PlanningAccessView, error) {
	var out PlanningAccessView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		p, err := financedb.New(tx).PlanningResource(ctx, financedb.PlanningResourceParams{SpaceID: space, ID: id, MemberID: m.ID})
		if err != nil {
			return absent(err)
		}
		if p.PlanningResource.ResponsibleMember != m.ID {
			return access.ErrForbidden
		}
		readers, err := financedb.New(tx).PlanningReaders(ctx, financedb.PlanningReadersParams{SpaceID: space, ResourceID: id})
		out = PlanningAccessView{ID: id, ResponsibleMemberID: m.ID, Visibility: p.PlanningResource.Visibility, Readers: readers, Version: p.PlanningResource.Version}
		return err
	})
	return out, err
}

func (s *Service) EditPlanningAccess(ctx context.Context, c access.SpaceCommand, id string, v PlanningAccessEdit) (string, error) {
	sharing := Sharing{Visibility: v.Visibility, SpacePermission: "read", Grants: []Grant{}}
	for _, member := range v.Readers {
		sharing.Grants = append(sharing.Grants, Grant{MemberID: member, Permission: "read"})
	}
	if err := sharing.validate(); err != nil {
		return "", err
	}
	if _, err := uuid.Parse(v.ResponsibleMemberID); err != nil || v.Version < 1 || !v.Confirm || !text(v.Reason, 500, false) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "planning.access", "finance.write", struct {
		ID    string
		Value PlanningAccessEdit
	}{id, v}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, _ string) error {
		if err := s.access.RequireRecent(actor); err != nil {
			return err
		}
		return s.authorizePlanningWrite(ctx, tx, actor, m, c.SpaceID, "", id)
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		if err := s.validateGrants(ctx, tx, c.SpaceID, v.ResponsibleMemberID, sharing); err != nil {
			return "", err
		}
		target, err := accessdb.New(tx).GetMember(ctx, accessdb.GetMemberParams{SpaceID: c.SpaceID, ID: v.ResponsibleMemberID})
		if err != nil {
			return "", absent(err)
		}
		if target.Role == "reader" {
			return "", access.ErrInvalid
		}
		if target.ID != m.ID {
			// Responsibility may only move to a current reader of the resource.
			if _, err := q.PlanningResource(ctx, financedb.PlanningResourceParams{SpaceID: c.SpaceID, ID: id, MemberID: target.ID}); err != nil {
				return "", absent(err)
			}
		}
		n, err := q.UpdatePlanningAccess(ctx, financedb.UpdatePlanningAccessParams{SpaceID: c.SpaceID, ID: id, Version: v.Version, ResponsibleMember: target.ID, Visibility: v.Visibility})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		if err := q.ClearPlanningReaders(ctx, financedb.ClearPlanningReadersParams{SpaceID: c.SpaceID, ResourceID: id}); err != nil {
			return "", err
		}
		if err := q.PutPlanningReaders(ctx, financedb.PutPlanningReadersParams{SpaceID: c.SpaceID, ResourceID: id, MemberIds: v.Readers}); err != nil {
			return "", err
		}
		if err := q.CapturePlanningAccess(ctx, financedb.CapturePlanningAccessParams{SpaceID: c.SpaceID, ID: id, Author: actor.ID(), Reason: v.Reason}); err != nil {
			return "", err
		}
		if err := accessdb.New(tx).RevokeSpaceArtifacts(ctx, c.SpaceID); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) RecoverPlanningResource(ctx context.Context, c access.SpaceCommand, id string, v FinancialVersionConfirmation) (string, error) {
	if v.Version < 1 || !v.Confirm {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "planning.recover", "finance.write", struct {
		ID    string
		Value FinancialVersionConfirmation
	}{id, v}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, _ string) error {
		if err := s.access.RequireRecent(actor); err != nil {
			return err
		}
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		p, err := financedb.New(tx).PlanningResource(ctx, financedb.PlanningResourceParams{SpaceID: c.SpaceID, ID: id, MemberID: m.ID})
		if err == nil && p.PlanningResource.ResponsibleMember == m.ID {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = financedb.New(tx).RecoverablePlanningResource(ctx, financedb.RecoverablePlanningResourceParams{SpaceID: c.SpaceID, ID: id, PrincipalID: actor.ID()})
		return absent(err)
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		old, err := q.RecoverablePlanningResource(ctx, financedb.RecoverablePlanningResourceParams{SpaceID: c.SpaceID, ID: id, PrincipalID: actor.ID()})
		if err != nil {
			return "", absent(err)
		}
		n, err := q.UpdatePlanningAccess(ctx, financedb.UpdatePlanningAccessParams{SpaceID: c.SpaceID, ID: id, Version: v.Version, ResponsibleMember: m.ID, Visibility: old.Visibility})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		if err := q.CapturePlanningAccess(ctx, financedb.CapturePlanningAccessParams{SpaceID: c.SpaceID, ID: id, Author: actor.ID(), Reason: "Responsibility explicitly recovered by the same identity"}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) RecoverablePlanning(ctx context.Context, authorization, space, cursor string, limit int32) (access.Page[RecoveryReference], error) {
	out := access.Page[RecoveryReference]{Items: []RecoveryReference{}}
	if err := validPage(cursor, limit); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, actor access.Actor, _ accessdb.Membership) error {
		if err := s.access.RequireRecent(actor); err != nil {
			return err
		}
		rows, err := financedb.New(tx).RecoverablePlanningResources(ctx, financedb.RecoverablePlanningResourcesParams{SpaceID: space, PrincipalID: actor.ID(), ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []RecoveryReference{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, row := range rows {
			out.Items = append(out.Items, RecoveryReference{ID: row.ID, Version: row.Version})
		}
		return nil
	})
	return out, err
}
