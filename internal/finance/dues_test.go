package finance

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

func TestDueAmount(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		value DueAmount
		valid bool
	}{
		{"unknown is not zero", DueAmount{Certainty: "unknown"}, true},
		{"confirmed", DueAmount{Certainty: "confirmed", AmountMinor: new("200000")}, true},
		{"estimated", DueAmount{Certainty: "estimated", AmountMinor: new("200000")}, true},
		{"range", DueAmount{Certainty: "range", AmountMinor: new("100"), MaximumMinor: new("200")}, true},
		{"unknown with amount", DueAmount{Certainty: "unknown", AmountMinor: new("1")}, false},
		{"missing amount", DueAmount{Certainty: "confirmed"}, false},
		{"inverted range", DueAmount{Certainty: "range", AmountMinor: new("200"), MaximumMinor: new("100")}, false},
		{"missing range maximum", DueAmount{Certainty: "range", AmountMinor: new("100")}, false},
		{"zero", DueAmount{Certainty: "confirmed", AmountMinor: new("0")}, false},
		{"overflow", DueAmount{Certainty: "confirmed", AmountMinor: new("9223372036854775808")}, false},
		{"noncanonical", DueAmount{Certainty: "confirmed", AmountMinor: new("01")}, false},
		{"unexpected maximum", DueAmount{Certainty: "estimated", AmountMinor: new("100"), MaximumMinor: new("200")}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := tt.value.parse()
			if (err == nil) != tt.valid {
				t.Fatalf("valid = %t, error = %v", tt.valid, err)
			}
		})
	}
}

func TestDueView(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, certainty, applied, date, state, status, pending, excess string
		overdue                                                        bool
	}{
		{"insufficient", "confirmed", "185000", "2026-11-01", "open", "partial", "15000", "0", true},
		{"exact", "confirmed", "200000", "2026-11-01", "open", "paid", "0", "0", false},
		{"excess visible", "confirmed", "200001", "2026-11-01", "open", "paid", "0", "1", false},
		{"estimate never closes", "estimated", "200000", "2026-11-01", "open", "partial", "", "", true},
		{"range never closes", "range", "200000", "2026-11-01", "open", "partial", "", "", true},
		{"unknown never closes", "unknown", "200000", "2026-11-01", "open", "partial", "", "", true},
		{"overdue", "confirmed", "0", "2026-11-01", "open", "overdue", "200000", "0", true},
		{"upcoming", "confirmed", "0", "2026-11-08", "open", "upcoming", "200000", "0", false},
		{"pending", "confirmed", "0", "2026-12-01", "open", "pending", "200000", "0", false},
		{"omission is not payment", "confirmed", "0", "2026-11-01", "skipped", "skipped", "200000", "0", false},
		{"cancellation is not payment", "confirmed", "0", "2026-11-01", "cancelled", "cancelled", "200000", "0", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			date, _ := time.Parse(time.DateOnly, tt.date)
			row := financedb.AuthorizedDueItemsRow{DueItem: financedb.DueItem{ID: "due", DueDate: pgtype.Date{Time: date, Valid: true}, Certainty: tt.certainty, AmountMinor: pgtype.Int8{Int64: 200000, Valid: tt.certainty != "unknown"}, State: tt.state}, Currency: "PEN", Scale: 2, AppliedMinor: tt.applied}
			view, err := dueView(row, "2026-11-07")
			if err != nil || view.Status != tt.status || view.Overdue != tt.overdue {
				t.Fatalf("view = %+v, error = %v", view, err)
			}
			for _, pair := range []struct {
				got  *string
				want string
			}{{view.PendingMinor, tt.pending}, {view.ExcessMinor, tt.excess}} {
				if pair.want == "" && pair.got != nil || pair.want != "" && (pair.got == nil || *pair.got != pair.want) {
					t.Fatalf("amount = %v, want %q", pair.got, pair.want)
				}
			}
		})
	}
}
