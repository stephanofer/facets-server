package finance

import (
	"errors"
	"testing"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/money"
)

func TestOpeningReference(t *testing.T) {
	t.Parallel()
	currency, err := money.ParseCurrency("PEN")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		value        OpeningReference
		valid, known bool
	}{
		{name: "unknown is not zero", value: OpeningReference{Date: "2026-11-01", Quality: "unknown"}, valid: true},
		{name: "confirmed zero", value: OpeningReference{Date: "2026-11-01", Quality: "confirmed", AmountMinor: new("0")}, valid: true, known: true},
		{name: "negative reference", value: OpeningReference{Date: "2026-11-01", Quality: "confirmed", AmountMinor: new("-9223372036854775808")}, valid: true, known: true},
		{name: "estimated", value: OpeningReference{Date: "2026-11-01", Quality: "estimated", AmountMinor: new("12000")}, valid: true, known: true},
		{name: "unknown with zero", value: OpeningReference{Date: "2026-11-01", Quality: "unknown", AmountMinor: new("0")}},
		{name: "confirmed without amount", value: OpeningReference{Date: "2026-11-01", Quality: "confirmed"}},
		{name: "overflow", value: OpeningReference{Date: "2026-11-01", Quality: "confirmed", AmountMinor: new("9223372036854775808")}},
		{name: "fractional minor", value: OpeningReference{Date: "2026-11-01", Quality: "confirmed", AmountMinor: new("1.5")}},
		{name: "invalid date", value: OpeningReference{Date: "2026-02-29", Quality: "unknown"}},
		{name: "invalid year", value: OpeningReference{Date: "0000-01-01", Quality: "unknown"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, n, err := parseOpening(tc.value, currency)
			if tc.valid {
				if err != nil || n.Valid != tc.known {
					t.Fatalf("known=%v err=%v", n.Valid, err)
				}
			} else if !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("accepted invalid reference: %v", err)
			}
		})
	}
}

func TestSharingValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		value Sharing
		valid bool
	}{
		{name: "private", value: Sharing{Visibility: "private", SpacePermission: "read"}, valid: true},
		{name: "space", value: Sharing{Visibility: "space", SpacePermission: "operate"}, valid: true},
		{name: "selected", value: Sharing{Visibility: "selected", SpacePermission: "read", Grants: []Grant{{MemberID: "00000000-0000-0000-0000-000000000001", Permission: "read"}}}, valid: true},
		{name: "empty selected", value: Sharing{Visibility: "selected", SpacePermission: "read"}},
		{name: "unrecognized visibility", value: Sharing{Visibility: "all", SpacePermission: "read"}},
		{name: "private with grants", value: Sharing{Visibility: "private", SpacePermission: "read", Grants: []Grant{{MemberID: "00000000-0000-0000-0000-000000000001", Permission: "read"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.value.validate(); (err == nil) != tc.valid {
				t.Fatalf("validation=%v", err)
			}
		})
	}
}
