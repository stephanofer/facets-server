package finance

import (
	"testing"

	"github.com/stephanofer/facets-server/internal/money"
)

func TestCorrectedBalances(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, before, old, next, want string
		oldTracked, nextTracked       bool
		invalid                       bool
	}{
		{name: "replace expense", before: "80", old: "-20", next: "-30", oldTracked: true, nextTracked: true, want: "70"},
		{name: "void expense", before: "80", old: "-20", oldTracked: true, want: "100"},
		{name: "move into tracking", before: "100", old: "-20", next: "-30", nextTracked: true, want: "70"},
		{name: "move before tracking", before: "80", old: "-20", next: "-30", oldTracked: true, want: "100"},
		{name: "no intermediate overflow", before: "9223372036854775807", old: "-1", next: "-1", oldTracked: true, nextTracked: true, want: "9223372036854775807"},
		{name: "true overflow", before: "9223372036854775807", old: "-1", oldTracked: true, invalid: true},
		{name: "unknown remains unknown", old: "10", next: "20", oldTracked: true, nextTracked: true},
		{name: "negative retained", before: "0", next: "-1", nextTracked: true, want: "-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a := AccountView{ID: "account", Currency: "PEN"}
			if tc.before != "" {
				a.BalanceMinor = new(tc.before)
			}
			var prior []PositionEffectView
			if tc.old != "" {
				prior = []PositionEffectView{{AccountID: a.ID, AmountMinor: tc.old, InTracking: tc.oldTracked}}
			}
			var next []plannedEffect
			if tc.next != "" {
				c, _ := money.ParseCurrency("PEN")
				n, err := money.ParseMinor(tc.next, c)
				if err != nil {
					t.Fatal(err)
				}
				next = []plannedEffect{{account: a, amount: n, tracked: tc.nextTracked}}
			}
			got, err := correctedBalances(map[string]AccountView{a.ID: a}, prior, next)
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v", err)
			}
			if tc.invalid {
				return
			}
			if len(got) != 1 {
				t.Fatal("missing account consequence")
			}
			if tc.want == "" {
				if got[0].AfterMinor != nil {
					t.Fatal("invented an opening reference")
				}
			} else if got[0].AfterMinor == nil || *got[0].AfterMinor != tc.want {
				t.Fatalf("after = %v, want %s", got[0].AfterMinor, tc.want)
			}
		})
	}
}
