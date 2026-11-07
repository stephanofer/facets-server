package finance

import (
	"github.com/stephanofer/facets-server/internal/calendar"
	"testing"
)

func TestSubscriptionEquivalents(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, frequency, amount, monthly, annual, status string
		interval                                         int
	}{
		{name: "monthly", frequency: "monthly", amount: "100", monthly: "100", annual: "1200", status: "available"},
		{name: "weekly", frequency: "weekly", amount: "100", monthly: "433", annual: "5200", status: "available"},
		{name: "biweekly", frequency: "biweekly", amount: "100", monthly: "217", annual: "2600", status: "available"},
		{name: "twice monthly", frequency: "twice_monthly", amount: "100", monthly: "200", annual: "2400", status: "available"},
		{name: "even tie down", frequency: "yearly", amount: "6", monthly: "0", annual: "6", status: "available"},
		{name: "odd tie up", frequency: "yearly", amount: "18", monthly: "2", annual: "18", status: "available"},
		{name: "every three months", frequency: "every_months", interval: 3, amount: "100", monthly: "33", annual: "400", status: "available"},
		{name: "every four weeks", frequency: "every_weeks", interval: 4, amount: "100", monthly: "108", annual: "1300", status: "available"},
		{name: "annual overflow", frequency: "monthly", amount: "9223372036854775807", monthly: "9223372036854775807", status: "out_of_range"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := SeriesValue{Amount: DueAmount{Certainty: "confirmed", AmountMinor: new(tt.amount)}, Recurrence: calendar.Recurrence{Frequency: tt.frequency, Interval: tt.interval}}
			got := subscriptionEquivalents(v)
			if got.Status != tt.status || !got.ComparativeOnly || got.Monthly.AmountMinor == nil || *got.Monthly.AmountMinor != tt.monthly {
				t.Fatal(got)
			}
			if tt.annual == "" {
				if got.Annual.AmountMinor != nil {
					t.Fatal("overflow not explicit", got)
				}
			} else if got.Annual.AmountMinor == nil || *got.Annual.AmountMinor != tt.annual {
				t.Fatal(got)
			}
		})
	}
	unknown := subscriptionEquivalents(SeriesValue{Amount: DueAmount{Certainty: "unknown"}})
	if unknown.Status != "unknown" || unknown.Monthly.AmountMinor != nil {
		t.Fatal("unknown represented as zero", unknown)
	}
	ranged := subscriptionEquivalents(SeriesValue{Amount: DueAmount{Certainty: "range", AmountMinor: new("100"), MaximumMinor: new("150")}, Recurrence: calendar.Recurrence{Frequency: "monthly"}})
	if ranged.Annual.MaximumMinor == nil || *ranged.Annual.MaximumMinor != "1800" {
		t.Fatal(ranged)
	}
}

func TestFinancialSubscriptionValidation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		v     FinancialSubscription
		valid bool
	}{
		{name: "minimal", v: FinancialSubscription{Plan: "Synthetic plan"}, valid: true},
		{name: "no plan", v: FinancialSubscription{}},
		{name: "invalid date", v: FinancialSubscription{Plan: "Test", TrialUntil: "2027-02-29"}},
		{name: "trial before start", v: FinancialSubscription{Plan: "Test", TrialUntil: "2026-01-01"}},
		{name: "late cancellation deadline", v: FinancialSubscription{Plan: "Test", RenewalOn: "2027-01-01", CancelBy: "2027-01-02"}},
		{name: "recorded cancellation", v: FinancialSubscription{Plan: "Test", CancellationOn: "2026-11-05"}, valid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.v.validate("2026-11-01") == nil) != tt.valid {
				t.Fatal(tt.v)
			}
		})
	}
}

func TestPlanningTargetValidation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		v     PlanningTarget
		valid bool
	}{
		{name: "unassigned", v: PlanningTarget{Currency: "PEN"}, valid: true},
		{name: "unknown currency", v: PlanningTarget{Currency: "ZZZ"}},
		{name: "missing currency", v: PlanningTarget{}},
		{name: "account", v: PlanningTarget{AccountID: "c169c88c-e24b-4e97-8d2c-45aff8fd4000"}, valid: true},
		{name: "ambiguous currency", v: PlanningTarget{AccountID: "c169c88c-e24b-4e97-8d2c-45aff8fd4000", Currency: "PEN"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if (tt.v.validate() == nil) != tt.valid {
				t.Fatal(tt.v)
			}
		})
	}
}
