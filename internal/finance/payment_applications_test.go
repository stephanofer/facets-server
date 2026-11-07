package finance

import (
	"testing"
	"uuid"
)

func TestPaymentApplicationValidation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		change func(*PaymentApplicationCreate)
	}{
		{"missing confirmation", func(v *PaymentApplicationCreate) { v.Confirm = false }},
		{"missing operation", func(v *PaymentApplicationCreate) { v.OperationID = "" }},
		{"invalid effect", func(v *PaymentApplicationCreate) { v.Effect = 4 }},
		{"no allocations", func(v *PaymentApplicationCreate) { v.Allocations = nil }},
		{"duplicate due", func(v *PaymentApplicationCreate) { v.Allocations = append(v.Allocations, v.Allocations[0]) }},
		{"zero version", func(v *PaymentApplicationCreate) { v.Allocations[0].Version = 0 }},
		{"overflow", func(v *PaymentApplicationCreate) { v.Allocations[0].MovementMinor = "9223372036854775808" }},
		{"negative attribution", func(v *PaymentApplicationCreate) { v.Allocations[0].DueMinor = "-1" }},
		{"noncanonical attribution", func(v *PaymentApplicationCreate) { v.Allocations[0].DueMinor = "01" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			v := PaymentApplicationCreate{OperationID: uuid.New().String(), Revision: 1, Confirm: true, Allocations: []DueAllocation{{DueID: uuid.New().String(), Version: 1, MovementMinor: "100", DueMinor: "100"}}}
			if err := v.validate(); err != nil {
				t.Fatal("valid application rejected", err)
			}
			tt.change(&v)
			if err := v.validate(); err == nil {
				t.Fatal("invalid application accepted")
			}
		})
	}
}

func TestApplicableEffect(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"purchase", "income", "refund", "transfer_out", "transfer_in", "fee", "adjustment"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			e := PositionEffectView{Kind: kind, InTracking: true}
			if applicableEffect(e, "payment") != (kind == "purchase") || applicableEffect(e, "receipt") != (kind == "income" || kind == "refund") {
				t.Fatal("wrong financial meaning")
			}
			e.InTracking = false
			if applicableEffect(e, "payment") || applicableEffect(e, "receipt") {
				t.Fatal("antecedent satisfied a current obligation")
			}
		})
	}
}
