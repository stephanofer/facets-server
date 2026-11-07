package finance

import (
	"errors"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
)

func TestOperationCreateValidation(t *testing.T) {
	t.Parallel()
	a, b := uuid.New().String(), uuid.New().String()
	for _, tc := range []struct {
		name string
		edit func(*OperationCreate)
		ok   bool
	}{
		{name: "confirmed expense", edit: func(*OperationCreate) {}, ok: true},
		{name: "unconfirmed", edit: func(v *OperationCreate) { v.Confirm = false }},
		{name: "invalid type", edit: func(v *OperationCreate) { v.Kind = "payment" }},
		{name: "invalid date", edit: func(v *OperationCreate) { v.Date = "2026-02-30" }},
		{name: "missing account", edit: func(v *OperationCreate) { v.Applied.AccountID = "" }},
		{name: "foreign payload field", edit: func(v *OperationCreate) { v.Received = &AppliedAmount{AccountID: b, AmountMinor: "100"} }},
		{name: "transfer without destination", edit: func(v *OperationCreate) { v.Kind = "transfer" }},
		{name: "same endpoint", edit: func(v *OperationCreate) {
			v.Kind, v.Received = "transfer", &AppliedAmount{AccountID: a, AmountMinor: "100"}
		}},
		{name: "transfer", edit: func(v *OperationCreate) {
			v.Kind, v.Received = "transfer", &AppliedAmount{AccountID: b, AmountMinor: "100"}
		}, ok: true},
		{name: "invalid tag", edit: func(v *OperationCreate) { v.TagIDs = []string{""} }},
		{name: "duplicate tags", edit: func(v *OperationCreate) { v.TagIDs = []string{a, a} }},
		{name: "too many parts", edit: func(v *OperationCreate) { v.Parts = make([]CategoryPart, 101) }},
		{name: "fee without category", edit: func(v *OperationCreate) {
			v.Kind, v.Received = "transfer", &AppliedAmount{AccountID: b, AmountMinor: "100"}
			v.Fees = []OperationFee{{AppliedAmount: AppliedAmount{AccountID: a, AmountMinor: "1"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := OperationCreate{Kind: "expense", Date: "2026-11-01", Applied: AppliedAmount{AccountID: a, AmountMinor: "100"}, Confirm: true}
			tc.edit(&v)
			err := v.validate()
			if (err == nil) != tc.ok || err != nil && !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("validate() = %v, want valid=%v", err, tc.ok)
			}
		})
	}
}

func TestPositiveMinor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, currency string
		ok                    bool
	}{
		{name: "minor units", value: "12000", currency: "PEN", ok: true},
		{name: "zero scale", value: "1", currency: "JPY", ok: true},
		{name: "three decimals", value: "1001", currency: "KWD", ok: true},
		{name: "int64 max", value: "9223372036854775807", currency: "PEN", ok: true},
		{name: "overflow", value: "9223372036854775808", currency: "PEN"},
		{name: "negative", value: "-1", currency: "PEN"},
		{name: "zero", value: "0", currency: "PEN"},
		{name: "noncanonical", value: "01", currency: "PEN"},
		{name: "float", value: "1.0", currency: "PEN"},
		{name: "unknown currency", value: "1", currency: "XXX"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := positiveMinor(tc.value, tc.currency)
			if (err == nil) != tc.ok {
				t.Fatalf("positiveMinor() = %v, want valid=%v", err, tc.ok)
			}
		})
	}
}
