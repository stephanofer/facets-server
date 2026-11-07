package finance

import (
	"context"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

type OperationCorrection struct {
	Version     int64           `json:"version" minimum:"1"`
	Reason      string          `json:"reason" minLength:"1" maxLength:"500"`
	Replacement OperationCreate `json:"replacement"`
}

type OperationVoid struct {
	FinancialVersionConfirmation
	Reason string `json:"reason" minLength:"1" maxLength:"500"`
}

type AccountBalanceChange struct {
	AccountID                 string  `json:"account_id"`
	Currency                  string  `json:"currency"`
	BeforeMinor               *string `json:"before_minor" nullable:"true"`
	AfterMinor                *string `json:"after_minor" nullable:"true"`
	ReconciledHistoryAffected bool    `json:"reconciled_history_affected"`
}

type OperationConsequences struct {
	Version    int64                  `json:"version"`
	ChangeKind string                 `json:"change_kind"`
	BeforeDate string                 `json:"before_date"`
	AfterDate  string                 `json:"after_date"`
	Accounts   []AccountBalanceChange `json:"accounts"`
}

func retainedCatalogIDs(v OperationView) []string {
	ids := []string{v.MerchantID, v.CounterpartyID}
	for _, c := range v.Components {
		ids = append(ids, c.CategoryID)
	}
	for _, tag := range v.Tags {
		ids = append(ids, tag.ID)
	}
	return ids
}

func (v OperationCorrection) validate() error {
	if v.Version < 1 || !text(v.Reason, 500, false) {
		return access.ErrInvalid
	}
	return v.Replacement.validate()
}

// correctionKind compares financial meaning, not the number of category splits.
// A classification change can redistribute components without moving money.
func correctionKind(old OperationView, v OperationCreate, p operationPlan) string {
	if (old.Refund == nil) != (v.Refund == nil) || old.Refund != nil && *old.Refund != *v.Refund {
		return "financial"
	}
	if old.Date != v.Date || len(old.Effects) != len(p.effects) || (old.Original == nil) != (v.Original == nil) {
		return "financial"
	}
	if old.Original != nil && *old.Original != *v.Original {
		return "financial"
	}
	for i, e := range p.effects {
		prior := old.Effects[i]
		if prior.AccountID != e.account.ID || prior.Kind != e.kind || prior.AmountMinor != strconv.FormatInt(e.amount.Minor(), 10) || prior.InTracking != e.tracked {
			return "financial"
		}
	}
	for _, c := range p.components {
		if !slices.ContainsFunc(old.Components, func(prior EconomicComponentView) bool {
			return prior.EffectOrdinal == c.effect && prior.ExchangeReferenceID == c.exchange
		}) {
			return "financial"
		}
	}
	return "descriptive"
}

// correctedBalances uses exact intermediate arithmetic: reversing an old effect
// and applying its replacement must not overflow merely because of their order.
func correctedBalances(accounts map[string]AccountView, old []PositionEffectView, next []plannedEffect) ([]AccountBalanceChange, error) {
	ids := slices.Sorted(maps.Keys(accounts))
	out := make([]AccountBalanceChange, 0, len(ids))
	for _, id := range ids {
		a := accounts[id]
		change := AccountBalanceChange{AccountID: id, Currency: a.Currency, BeforeMinor: a.BalanceMinor}
		if a.BalanceMinor != nil {
			balance, ok := new(big.Int).SetString(*a.BalanceMinor, 10)
			if !ok {
				return nil, access.ErrInvalid
			}
			for _, e := range old {
				if e.AccountID == id && e.InTracking {
					n, ok := new(big.Int).SetString(e.AmountMinor, 10)
					if !ok {
						return nil, access.ErrInvalid
					}
					balance.Sub(balance, n)
				}
			}
			for _, e := range next {
				if e.account.ID == id && e.tracked {
					balance.Add(balance, big.NewInt(e.amount.Minor()))
				}
			}
			if !balance.IsInt64() {
				return nil, access.ErrInvalid
			}
			change.AfterMinor = new(balance.String())
		}
		out = append(out, change)
	}
	return out, nil
}

func (s *Service) prepareCorrection(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, space, id string, version int64, replacement *OperationCreate) (OperationView, operationPlan, OperationConsequences, error) {
	var p operationPlan
	var out OperationConsequences
	old, err := readOperation(ctx, tx, space, id, m.ID, 0)
	if err != nil {
		return old, p, out, err
	}
	if err := execution.CheckVersion(1, version); err != nil {
		return old, p, out, err
	}
	if old.Revision != version {
		return old, p, out, execution.ErrVersion
	}
	if old.Voided {
		return old, p, out, access.ErrInvalid
	}
	if old.Kind == "refund" && old.Refund == nil {
		linked, err := financedb.New(tx).HasRefundLink(ctx, financedb.HasRefundLinkParams{SpaceID: space, OperationID: id, Revision: old.Revision})
		if err != nil {
			return old, p, out, err
		}
		if linked {
			return old, p, out, access.ErrNotFound
		}
	}
	ids := make([]string, 0, len(old.Effects)+2)
	for _, e := range old.Effects {
		ids = append(ids, e.AccountID)
	}
	if replacement != nil {
		if replacement.Kind != old.Kind {
			return old, p, out, access.ErrInvalid
		}
		ids = append(ids, operationAccountIDs(*replacement)...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	accounts := make(map[string]AccountView, len(ids))
	for _, account := range ids {
		a, err := s.AuthorizeAccount(ctx, tx, actor, space, account, "operate")
		if err != nil {
			return old, p, out, err
		}
		accounts[account] = a
	}
	out = OperationConsequences{Version: version, ChangeKind: "void", BeforeDate: old.Date, AfterDate: old.Date}
	if replacement != nil {
		p, err = s.planOperation(ctx, tx, actor, space, *replacement, &old)
		if err != nil {
			return old, p, out, err
		}
		out.ChangeKind, out.AfterDate = correctionKind(old, *replacement, p), replacement.Date
	}
	if err := validateRefundedPurchase(ctx, tx, space, old, replacement, p); err != nil {
		return old, p, out, err
	}
	out.Accounts, err = correctedBalances(accounts, old.Effects, p.effects)
	if err == nil && out.ChangeKind != "descriptive" {
		beforeDate, _ := time.Parse(time.DateOnly, out.BeforeDate)
		afterDate, _ := time.Parse(time.DateOnly, out.AfterDate)
		for i := range out.Accounts {
			account := out.Accounts[i].AccountID
			before, after := new(big.Int), new(big.Int)
			for _, e := range old.Effects {
				if e.AccountID == account && e.InTracking {
					n, _ := new(big.Int).SetString(e.AmountMinor, 10)
					before.Add(before, n)
				}
			}
			for _, e := range p.effects {
				if e.account.ID == account && e.tracked {
					after.Add(after, big.NewInt(e.amount.Minor()))
				}
			}
			out.Accounts[i].ReconciledHistoryAffected, err = financedb.New(tx).ReconciledBalanceAffected(ctx, financedb.ReconciledBalanceAffectedParams{SpaceID: space, AccountID: account, BeforeDate: pgtype.Date{Time: beforeDate, Valid: true}, AfterDate: pgtype.Date{Time: afterDate, Valid: true}, BeforeMinor: before.String(), AfterMinor: after.String()})
			if err != nil {
				break
			}
		}
	}
	return old, p, out, err
}

func (s *Service) PreviewOperationCorrection(ctx context.Context, authorization, space, id string, v OperationCorrection) (OperationConsequences, error) {
	var out OperationConsequences
	if err := v.validate(); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) error {
		var err error
		_, _, out, err = s.prepareCorrection(ctx, tx, a, m, space, id, v.Version, &v.Replacement)
		return err
	})
	return out, err
}

func (s *Service) CorrectOperation(ctx context.Context, c access.SpaceCommand, id string, v OperationCorrection) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind = "operation.correct"
	return s.changeOperation(ctx, c, id, v.Version, v.Reason, &v.Replacement, v)
}

func (s *Service) VoidOperation(ctx context.Context, c access.SpaceCommand, id string, v OperationVoid) (string, error) {
	if v.Version < 1 || !v.Confirm || !text(v.Reason, 500, false) {
		return "", access.ErrInvalid
	}
	c.Kind = "operation.void"
	return s.changeOperation(ctx, c, id, v.Version, v.Reason, nil, v)
}

func (s *Service) changeOperation(ctx context.Context, c access.SpaceCommand, id string, version int64, reason string, replacement *OperationCreate, content any) (string, error) {
	c.Capability, c.Content = "finance.write", struct {
		ID    string
		Value any
	}{ID: id, Value: content}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership, _ string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		_, err := financedb.New(tx).Operation(ctx, financedb.OperationParams{SpaceID: c.SpaceID, ID: id, MemberID: m.ID})
		return absent(err)
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		old, p, consequences, err := s.prepareCorrection(ctx, tx, actor, m, c.SpaceID, id, version, replacement)
		if err != nil {
			return "", err
		}
		if replacement == nil {
			if err := copyVoidedRevision(ctx, tx, c.SpaceID, old, actor.ID(), reason); err != nil {
				return "", err
			}
		} else if err := insertPlannedRevision(ctx, tx, c.SpaceID, id, version+1, actor.ID(), reason, consequences.ChangeKind, *replacement, p); err != nil {
			return "", err
		}
		q := financedb.New(tx)
		rows, err := q.AdvanceOperationRevision(ctx, financedb.AdvanceOperationRevisionParams{SpaceID: c.SpaceID, ID: id, CurrentRevision: version})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(rows, version); err != nil {
			return "", err
		}
		if err := setAccountProjections(ctx, tx, c.SpaceID, consequences.Accounts); err != nil {
			return "", err
		}
		if err := q.MarkAffectedReconciliations(ctx, financedb.MarkAffectedReconciliationsParams{SpaceID: c.SpaceID, OperationID: id, OldRevision: version, NewRevision: version + 1}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func setAccountProjections(ctx context.Context, tx pgx.Tx, space string, changes []AccountBalanceChange) error {
	for _, change := range changes {
		var amount pgtype.Int8
		if change.AfterMinor != nil {
			n, err := strconv.ParseInt(*change.AfterMinor, 10, 64)
			if err != nil {
				return err
			}
			amount = pgtype.Int8{Int64: n, Valid: true}
		}
		rows, err := financedb.New(tx).SetAccountProjection(ctx, financedb.SetAccountProjectionParams{SpaceID: space, ID: change.AccountID, BalanceMinor: amount})
		if err != nil {
			return err
		}
		if err := execution.CheckVersion(rows, 1); err != nil {
			return err
		}
	}
	return nil
}

func insertPlannedRevision(ctx context.Context, tx pgx.Tx, space, id string, revision int64, author, reason, kind string, v OperationCreate, p operationPlan) error {
	q := financedb.New(tx)
	currency := ""
	if v.Original != nil {
		currency = v.Original.Currency
	}
	if err := q.InsertOperationRevision(ctx, financedb.InsertOperationRevisionParams{SpaceID: space, OperationID: id, Revision: revision, EffectiveDate: p.date, Author: author, Note: v.Note, Exceptional: v.Exceptional, MerchantID: nullable(v.MerchantID), CounterpartyID: nullable(v.CounterpartyID), OriginalMinor: p.original, OriginalCurrency: nullable(currency), ChangeKind: kind, Reason: reason}); err != nil {
		return err
	}
	for i, e := range p.effects {
		if err := q.InsertPositionEffect(ctx, financedb.InsertPositionEffectParams{SpaceID: space, OperationID: id, Revision: revision, Ordinal: int32(i), AccountID: e.account.ID, Kind: e.kind, AmountMinor: e.amount.Minor(), InTracking: e.tracked}); err != nil {
			return err
		}
	}
	for i, c := range p.components {
		if err := q.InsertEconomicComponent(ctx, financedb.InsertEconomicComponentParams{SpaceID: space, OperationID: id, Revision: revision, Ordinal: int32(i), EffectOrdinal: c.effect, Direction: c.direction, AmountMinor: c.amount, CategoryID: nullable(c.category), ExchangeReferenceID: nullable(c.exchange)}); err != nil {
			return err
		}
	}
	if v.Refund != nil {
		amount, err := strconv.ParseInt(v.Refund.AmountMinor, 10, 64)
		if err != nil {
			return err
		}
		if err := q.InsertRefundLink(ctx, financedb.InsertRefundLinkParams{SpaceID: space, OperationID: id, Revision: revision, PurchaseID: v.Refund.OperationID, PurchaseRevision: p.refundRevision, PurchaseMinor: amount}); err != nil {
			return err
		}
	}
	return q.InsertOperationTags(ctx, financedb.InsertOperationTagsParams{SpaceID: space, OperationID: id, Revision: revision, TagIds: v.TagIDs})
}

func copyVoidedRevision(ctx context.Context, tx pgx.Tx, space string, old OperationView, author, reason string) error {
	q := financedb.New(tx)
	if err := q.CopyVoidedRevision(ctx, financedb.CopyVoidedRevisionParams{SpaceID: space, OperationID: old.ID, Revision: old.Revision, Author: author, Reason: reason}); err != nil {
		return err
	}
	return q.CopyVoidedRefundLink(ctx, financedb.CopyVoidedRefundLinkParams{SpaceID: space, OperationID: old.ID, Revision: old.Revision})
}

type OperationRevisionSummary struct {
	Revision   int64     `json:"revision"`
	Date       string    `json:"date"`
	Author     string    `json:"author"`
	RecordedAt time.Time `json:"recorded_at"`
	ChangeKind string    `json:"change_kind"`
	Reason     string    `json:"reason"`
	Voided     bool      `json:"voided"`
}

func (s *Service) OperationRevisions(ctx context.Context, authorization, space, id, cursor string, limit int32) (access.Page[OperationRevisionSummary], error) {
	out := access.Page[OperationRevisionSummary]{Items: []OperationRevisionSummary{}}
	var after int64
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 1 || strconv.FormatInt(after, 10) != cursor {
			return out, access.ErrInvalid
		}
	}
	if limit < 1 || limit > 100 {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		q := financedb.New(tx)
		if _, err := q.Operation(ctx, financedb.OperationParams{SpaceID: space, ID: id, MemberID: m.ID}); err != nil {
			return absent(err)
		}
		rows, err := q.OperationRevisions(ctx, financedb.OperationRevisionsParams{SpaceID: space, OperationID: id, MemberID: m.ID, Cursor: after, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []OperationRevisionSummary{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = strconv.FormatInt(rows[len(rows)-1].Revision, 10)
		}
		for _, r := range rows {
			out.Items = append(out.Items, OperationRevisionSummary{Revision: r.Revision, Date: r.EffectiveDate.Time.Format(time.DateOnly), Author: r.Author, RecordedAt: r.RecordedAt.Time, ChangeKind: r.ChangeKind, Reason: r.Reason, Voided: r.Voided})
		}
		return nil
	})
	return out, err
}
