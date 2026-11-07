package finance

import (
	"context"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

// FinancialSubscription describes a user's provider contract, never the space's
// commercial entitlement. Dates are manually recorded, not external confirmations.
type FinancialSubscription struct {
	Plan           string `json:"plan" minLength:"1" maxLength:"100"`
	TrialUntil     string `json:"trial_until,omitempty" format:"date"`
	RenewalOn      string `json:"renewal_on,omitempty" format:"date"`
	CancelBy       string `json:"cancel_by,omitempty" format:"date"`
	CancellationOn string `json:"cancellation_on,omitempty" format:"date" doc:"User-recorded provider cancellation date; stopping tracking does not cancel a contract."`
}

type SubscriptionCharge struct {
	OperationID string `json:"operation_id"`
	Date        string `json:"date"`
	AmountMinor string `json:"amount_minor"`
	Currency    string `json:"currency"`
}

func (v FinancialSubscription) validate(start string) error {
	if !text(v.Plan, 100, false) {
		return access.ErrInvalid
	}
	for _, date := range []string{v.TrialUntil, v.RenewalOn, v.CancelBy, v.CancellationOn} {
		if date != "" {
			if _, err := calendar.Parse(date); err != nil {
				return access.ErrInvalid
			}
		}
	}
	if v.TrialUntil != "" && v.TrialUntil < start || v.RenewalOn != "" && v.RenewalOn < start || v.CancelBy != "" && v.RenewalOn != "" && v.CancelBy > v.RenewalOn {
		return access.ErrInvalid
	}
	return nil
}

func optionalSeriesDate(v pgtype.Date) string {
	if !v.Valid {
		return ""
	}
	return v.Time.Format(time.DateOnly)
}

func validateSeriesCatalogs(ctx context.Context, tx pgx.Tx, space, id string, v SeriesValue) error {
	ids := []string{}
	if v.CounterpartyID != "" {
		ids = append(ids, v.CounterpartyID)
	}
	if v.SuggestedCategoryID != "" {
		ids = append(ids, v.SuggestedCategoryID)
	}
	refs, err := resolveCatalogReferences(ctx, tx, space, ids)
	if err != nil {
		return err
	}
	if v.CounterpartyID != "" {
		r := refs[v.CounterpartyID]
		if r.Kind != "counterparty" || r.Archived {
			return access.ErrInvalid
		}
	}
	if v.SuggestedCategoryID == "" && v.Subscription == nil {
		return nil
	}
	series, err := financedb.New(tx).LockRecurringSeries(ctx, financedb.LockRecurringSeriesParams{SpaceID: space, ID: id})
	if err != nil {
		return absent(err)
	}
	if v.Subscription != nil && series.Direction != "payment" {
		return access.ErrInvalid
	}
	if v.SuggestedCategoryID != "" {
		direction := "expense"
		if series.Direction == "receipt" {
			direction = "income"
		}
		r := refs[v.SuggestedCategoryID]
		if r.Kind != "category" || r.Archived || r.Value.Direction != direction {
			return access.ErrInvalid
		}
	}
	return nil
}

type EquivalentAmount struct {
	AmountMinor  *string `json:"amount_minor" nullable:"true"`
	MaximumMinor *string `json:"maximum_minor" nullable:"true"`
}

type SubscriptionEquivalents struct {
	Monthly         EquivalentAmount `json:"monthly"`
	Annual          EquivalentAmount `json:"annual"`
	Rule            string           `json:"rule"`
	Status          string           `json:"status" enum:"available,unknown,out_of_range"`
	ComparativeOnly bool             `json:"comparative_only"`
}

// Comparison convention: 52 weeks per year, 12 months per year. Round once per
// output, to even minor units; never round annual before deriving monthly.
func subscriptionEquivalents(v SeriesValue) *SubscriptionEquivalents {
	out := &SubscriptionEquivalents{Rule: "52_weeks_12_months_round_half_even", Status: "available", ComparativeOnly: true}
	amount, maximum, err := v.Amount.parse()
	if err != nil || !amount.Valid {
		out.Status = "unknown"
		return out
	}
	numerator, denominator := int64(1), int64(1)
	switch v.Recurrence.Frequency {
	case "weekly":
		numerator = 52
	case "biweekly":
		numerator = 26
	case "monthly":
		numerator = 12
	case "twice_monthly":
		numerator = 24
	case "every_weeks":
		numerator, denominator = 52, int64(v.Recurrence.Interval)
	case "every_months":
		numerator, denominator = 12, int64(v.Recurrence.Interval)
	case "yearly":
	default:
		out.Status = "unknown"
		return out
	}
	if denominator <= 0 {
		out.Status = "unknown"
		return out
	}
	convert := func(n int64, monthly bool) *string {
		d := denominator
		if monthly {
			d *= 12
		}
		value := new(big.Int).Mul(big.NewInt(n), big.NewInt(numerator))
		q, remainder := new(big.Int), new(big.Int)
		q.QuoRem(value, big.NewInt(d), remainder)
		cmp := remainder.Lsh(remainder, 1).Cmp(big.NewInt(d))
		if cmp > 0 || cmp == 0 && q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
		if !q.IsInt64() {
			out.Status = "out_of_range"
			return nil
		}
		return new(q.String())
	}
	out.Monthly.AmountMinor, out.Annual.AmountMinor = convert(amount.Int64, true), convert(amount.Int64, false)
	if maximum.Valid {
		out.Monthly.MaximumMinor, out.Annual.MaximumMinor = convert(maximum.Int64, true), convert(maximum.Int64, false)
	}
	return out
}
