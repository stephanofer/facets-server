package finance

import (
	"context"
	"math/big"
	"strconv"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/money"
)

type ReconciliationCreate struct {
	Date                    string  `json:"date" format:"date"`
	ObservedMinor           string  `json:"observed_minor" maxLength:"20"`
	ExpectedCalculatedMinor *string `json:"expected_calculated_minor" nullable:"true" maxLength:"20"`
	Adjust                  bool    `json:"adjust"`
	Reason                  string  `json:"reason" minLength:"1" maxLength:"500"`
	Confirm                 bool    `json:"confirm"`
}

type ReconciliationObservation struct {
	Date          string `json:"date" format:"date"`
	ObservedMinor string `json:"observed_minor" maxLength:"20"`
}

type ReconciliationPreview struct {
	Date            string  `json:"date"`
	CalculatedMinor *string `json:"calculated_minor" nullable:"true"`
	ObservedMinor   string  `json:"observed_minor"`
	DifferenceMinor *string `json:"difference_minor" nullable:"true"`
}

func compareReconciliation(ctx context.Context, tx pgx.Tx, space string, acc AccountView, date pgtype.Date, observed int64) (pgtype.Int8, string, error) {
	settings, err := accessdb.New(tx).GetSpace(ctx, space)
	if err != nil {
		return pgtype.Int8{}, "", err
	}
	zone, err := time.LoadLocation(settings.Zone)
	if err != nil {
		return pgtype.Int8{}, "", err
	}
	value := date.Time.Format(time.DateOnly)
	if value < acc.Opening.Date || value > time.Now().In(zone).Format(time.DateOnly) {
		return pgtype.Int8{}, "", access.ErrInvalid
	}
	calculated, err := financedb.New(tx).BalanceAtDate(ctx, financedb.BalanceAtDateParams{SpaceID: space, AccountID: acc.ID, EffectiveDate: date})
	if err != nil || calculated == "" {
		return pgtype.Int8{}, "", err
	}
	n, err := strconv.ParseInt(calculated, 10, 64)
	if err != nil {
		return pgtype.Int8{}, "", access.ErrInvalid
	}
	return pgtype.Int8{Int64: n, Valid: true}, new(big.Int).Sub(big.NewInt(observed), big.NewInt(n)).String(), nil
}

func (s *Service) PreviewReconciliation(ctx context.Context, authorization, space, account string, v ReconciliationObservation) (ReconciliationPreview, error) {
	out := ReconciliationPreview{Date: v.Date, ObservedMinor: v.ObservedMinor}
	d, err := queryDate(v.Date, "")
	if err != nil {
		return out, err
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) error {
		acc, err := s.AuthorizeAccount(ctx, tx, a, space, account, "operate")
		if err != nil {
			return err
		}
		currency, err := money.ParseCurrency(acc.Currency)
		if err != nil {
			return err
		}
		observed, err := money.ParseMinor(v.ObservedMinor, currency)
		if err != nil {
			return access.ErrInvalid
		}
		prior, difference, err := compareReconciliation(ctx, tx, space, acc, d, observed.Minor())
		if err != nil {
			return err
		}
		out.CalculatedMinor, out.DifferenceMinor = nil, nil
		if prior.Valid {
			out.CalculatedMinor = new(strconv.FormatInt(prior.Int64, 10))
			out.DifferenceMinor = new(difference)
		}
		return nil
	})
	return out, err
}

type ReconciliationView struct {
	ID              string    `json:"id"`
	Date            string    `json:"date"`
	CalculatedMinor *string   `json:"calculated_minor" nullable:"true"`
	ObservedMinor   string    `json:"observed_minor"`
	DifferenceMinor *string   `json:"difference_minor" nullable:"true"`
	AdjustmentID    string    `json:"adjustment_id,omitempty"`
	NeedsReview     bool      `json:"needs_review"`
	Author          string    `json:"author"`
	Reason          string    `json:"reason"`
	RecordedAt      time.Time `json:"recorded_at"`
}

func (s *Service) ReconcileAccount(ctx context.Context, c access.SpaceCommand, account string, v ReconciliationCreate) (string, error) {
	d, err := time.Parse(time.DateOnly, v.Date)
	if err != nil || d.Year() < 1 || d.Year() > 9999 || !v.Confirm || !text(v.Reason, 500, false) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "account.reconcile", "finance.write", struct {
		Account string
		Value   ReconciliationCreate
	}{account, v}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership, _ string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		_, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, account, "read")
		return err
	}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		acc, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, account, "operate")
		if err != nil {
			return "", err
		}
		currency, err := money.ParseCurrency(acc.Currency)
		if err != nil {
			return "", err
		}
		observed, err := money.ParseMinor(v.ObservedMinor, currency)
		if err != nil {
			return "", access.ErrInvalid
		}
		q := financedb.New(tx)
		date := pgtype.Date{Time: d, Valid: true}
		prior, difference, err := compareReconciliation(ctx, tx, c.SpaceID, acc, date, observed.Minor())
		if err != nil {
			return "", err
		}
		calculated := ""
		if prior.Valid {
			calculated = strconv.FormatInt(prior.Int64, 10)
		}
		if (v.ExpectedCalculatedMinor == nil) != (calculated == "") || v.ExpectedCalculatedMinor != nil && *v.ExpectedCalculatedMinor != calculated {
			return "", execution.ErrVersion
		}
		adjustment := ""
		if v.Adjust {
			if difference == "" {
				return "", access.ErrInvalid
			}
			if difference != "0" {
				delta, err := strconv.ParseInt(difference, 10, 64)
				if err != nil {
					return "", access.ErrInvalid
				}
				amount, err := money.New(delta, currency)
				if err != nil {
					return "", err
				}
				adjustment = uuid.New().String()
				plan := operationPlan{date: date, effects: []plannedEffect{{account: acc, kind: "adjustment", amount: amount, tracked: true}}, accounts: map[string]AccountView{account: acc}}
				if err := q.InsertOperation(ctx, financedb.InsertOperationParams{ID: adjustment, SpaceID: c.SpaceID, Kind: "adjustment", Author: a.ID()}); err != nil {
					return "", err
				}
				if err := insertPlannedRevision(ctx, tx, c.SpaceID, adjustment, 1, a.ID(), v.Reason, "initial", OperationCreate{Date: v.Date, Note: v.Reason}, plan); err != nil {
					return "", err
				}
				changes, err := correctedBalances(plan.accounts, nil, plan.effects)
				if err != nil {
					return "", err
				}
				if err := setAccountProjections(ctx, tx, c.SpaceID, changes); err != nil {
					return "", err
				}
				if err := q.MarkAffectedReconciliations(ctx, financedb.MarkAffectedReconciliationsParams{SpaceID: c.SpaceID, OperationID: adjustment, NewRevision: 1}); err != nil {
					return "", err
				}
			}
		}
		id := uuid.New().String()
		if err := q.InsertReconciliation(ctx, financedb.InsertReconciliationParams{ID: id, SpaceID: c.SpaceID, AccountID: account, EffectiveDate: date, CalculatedMinor: prior, ObservedMinor: observed.Minor(), Difference: difference, AdjustmentID: nullable(adjustment), Author: a.ID(), Reason: v.Reason}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) Reconciliations(ctx context.Context, authorization, space, account, cursor string, limit int32) (access.Page[ReconciliationView], error) {
	out := access.Page[ReconciliationView]{Items: []ReconciliationView{}}
	if err := validatePage(cursor, limit); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) error {
		if _, err := s.AuthorizeAccount(ctx, tx, a, space, account, "read"); err != nil {
			return err
		}
		rows, err := financedb.New(tx).AccountReconciliations(ctx, financedb.AccountReconciliationsParams{SpaceID: space, AccountID: account, ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []ReconciliationView{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, r := range rows {
			v := ReconciliationView{ID: r.ID, Date: r.EffectiveDate.Time.Format(time.DateOnly), ObservedMinor: strconv.FormatInt(r.ObservedMinor, 10), AdjustmentID: r.AdjustmentID.String, NeedsReview: r.NeedsReview, Author: r.Author, Reason: r.Reason, RecordedAt: r.RecordedAt.Time}
			if r.CalculatedMinor.Valid {
				v.CalculatedMinor = new(strconv.FormatInt(r.CalculatedMinor.Int64, 10))
				v.DifferenceMinor = new(r.Difference)
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return out, err
}

type ConsistencyView struct {
	ID                 string    `json:"id"`
	ProjectedMinor     *string   `json:"projected_minor" nullable:"true"`
	ReconstructedMinor *string   `json:"reconstructed_minor" nullable:"true"`
	Discrepancy        bool      `json:"discrepancy"`
	Author             string    `json:"author"`
	RecordedAt         time.Time `json:"recorded_at"`
}

func (s *Service) CheckAccountConsistency(ctx context.Context, c access.SpaceCommand, account string, confirm bool) (string, error) {
	if !confirm {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "account.check_consistency", "finance.write", account
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership, _ string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		_, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, account, "read")
		return err
	}, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		// Read authorization intentionally permits checks on archived/immobilized accounts.
		acc, err := s.AuthorizeAccount(ctx, tx, a, c.SpaceID, account, "read")
		if err != nil {
			return "", err
		}
		q := financedb.New(tx)
		rebuilt, err := q.ReconstructedBalance(ctx, financedb.ReconstructedBalanceParams{SpaceID: c.SpaceID, ID: account})
		if err != nil {
			return "", err
		}
		var projected pgtype.Int8
		if acc.BalanceMinor != nil {
			n, err := strconv.ParseInt(*acc.BalanceMinor, 10, 64)
			if err != nil {
				return "", err
			}
			projected = pgtype.Int8{Int64: n, Valid: true}
		}
		discrepancy := (rebuilt == "") != (acc.BalanceMinor == nil) || acc.BalanceMinor != nil && *acc.BalanceMinor != rebuilt
		id := uuid.New().String()
		if err := q.InsertConsistencyCheck(ctx, financedb.InsertConsistencyCheckParams{ID: id, SpaceID: c.SpaceID, AccountID: account, ProjectedMinor: projected, Reconstructed: rebuilt, Discrepancy: discrepancy, Author: a.ID()}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) ConsistencyChecks(ctx context.Context, authorization, space, account, cursor string, limit int32) (access.Page[ConsistencyView], error) {
	out := access.Page[ConsistencyView]{Items: []ConsistencyView{}}
	if err := validatePage(cursor, limit); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) error {
		if _, err := s.AuthorizeAccount(ctx, tx, a, space, account, "read"); err != nil {
			return err
		}
		rows, err := financedb.New(tx).AccountConsistencyChecks(ctx, financedb.AccountConsistencyChecksParams{SpaceID: space, AccountID: account, ID: cursor, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []ConsistencyView{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, r := range rows {
			v := ConsistencyView{ID: r.ID, Discrepancy: r.Discrepancy, Author: r.Author, RecordedAt: r.RecordedAt.Time}
			if r.ProjectedMinor.Valid {
				v.ProjectedMinor = new(strconv.FormatInt(r.ProjectedMinor.Int64, 10))
			}
			if r.Reconstructed != "" {
				v.ReconstructedMinor = new(r.Reconstructed)
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return out, err
}

func validatePage(cursor string, limit int32) error {
	if limit < 1 || limit > 100 {
		return access.ErrInvalid
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return access.ErrInvalid
		}
	}
	return nil
}
