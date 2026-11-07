package finance

import (
	"errors"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
)

func TestMovementCapacity(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, kind, amount, used, capacity, available, direction string
		tracked, voided, invalid                                 bool
	}{
		{name: "partial purchase", kind: "purchase", amount: "-100", used: "60", capacity: "100", available: "40", direction: "payment", tracked: true},
		{name: "fully applied", kind: "purchase", amount: "100", used: "100", capacity: "100", available: "0", direction: "payment", tracked: true},
		{name: "income", kind: "income", amount: "100", used: "0", capacity: "100", available: "100", direction: "receipt", tracked: true},
		{name: "refund", kind: "refund", amount: "-100", used: "1", capacity: "100", available: "99", direction: "receipt", tracked: true},
		{name: "void", kind: "purchase", amount: "-100", used: "0", capacity: "0", available: "0", tracked: true, voided: true},
		{name: "antecedent", kind: "purchase", amount: "-100", used: "0", capacity: "0", available: "0"},
		{name: "transfer", kind: "transfer_out", amount: "-100", used: "0", capacity: "0", available: "0", tracked: true},
		{name: "adjustment", kind: "adjustment", amount: "100", used: "0", capacity: "0", available: "0", tracked: true},
		{name: "minimum int64 absolute", kind: "purchase", amount: "-9223372036854775808", used: "9223372036854775807", capacity: "9223372036854775808", available: "1", direction: "payment", tracked: true},
		{name: "overused", kind: "purchase", amount: "-100", used: "101", tracked: true, invalid: true},
		{name: "negative used", kind: "income", amount: "100", used: "-1", tracked: true, invalid: true},
		{name: "malformed used", kind: "income", amount: "100", used: "invalid", tracked: true, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			op := OperationView{ID: "source", Revision: 3, Voided: tt.voided, Effects: []PositionEffectView{{AccountID: "account", Currency: "PEN", Scale: 2, Kind: tt.kind, AmountMinor: tt.amount, InTracking: tt.tracked}}}
			out, err := movementCapacity(op, 0, tt.used)
			if tt.invalid {
				if !errors.Is(err, access.ErrInvalid) {
					t.Fatalf("invalid capacity accepted: %+v, %v", out, err)
				}
				return
			}
			if err != nil || out.OperationID != op.ID || out.Revision != 3 || out.Currency != "PEN" || out.Scale != 2 || out.AccountID != "account" || out.CapacityMinor != tt.capacity || out.AppliedMinor != tt.used || out.AvailableMinor != tt.available || out.Direction != tt.direction || out.Eligible != (tt.direction != "") {
				t.Fatalf("capacity: %+v, %v", out, err)
			}
		})
	}
	for _, effect := range []int32{-1, 0, 3} {
		if _, err := movementCapacity(OperationView{}, effect, "0"); !errors.Is(err, access.ErrInvalid) {
			t.Fatalf("missing effect %d accepted: %v", effect, err)
		}
	}
}

func TestPaymentOperationValidation(t *testing.T) {
	t.Parallel()
	valid := func() PaymentOperationCreate {
		return PaymentOperationCreate{
			Operation:   OperationCreate{Kind: "expense", Date: "2026-11-01", Applied: AppliedAmount{AccountID: uuid.New().String(), AmountMinor: "100"}, Confirm: true},
			Allocations: []DueAllocation{{DueID: uuid.New().String(), Version: 1, MovementMinor: "100", DueMinor: "100"}},
		}
	}
	for _, tt := range []struct {
		name   string
		change func(*PaymentOperationCreate)
	}{
		{"unconfirmed", func(v *PaymentOperationCreate) { v.Operation.Confirm = false }},
		{"no targets", func(v *PaymentOperationCreate) { v.Allocations = nil }},
		{"duplicate target", func(v *PaymentOperationCreate) { v.Allocations = append(v.Allocations, v.Allocations[0]) }},
		{"zero allocation", func(v *PaymentOperationCreate) { v.Allocations[0].MovementMinor = "0" }},
		{"overflow", func(v *PaymentOperationCreate) { v.Allocations[0].DueMinor = "9223372036854775808" }},
		{"missing version", func(v *PaymentOperationCreate) { v.Allocations[0].Version = 0 }},
		{"invalid date", func(v *PaymentOperationCreate) { v.Operation.Date = "2026-02-30" }},
		{"transfer", func(v *PaymentOperationCreate) {
			v.Operation.Kind = "transfer"
			v.Operation.Received = &AppliedAmount{AccountID: uuid.New().String(), AmountMinor: "100"}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := valid()
			if err := v.validate(); err != nil {
				t.Fatal("valid input rejected", err)
			}
			tt.change(&v)
			if !errors.Is(v.validate(), access.ErrInvalid) {
				t.Fatal("invalid composed command accepted")
			}
		})
	}
}
