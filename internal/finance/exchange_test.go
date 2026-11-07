package finance

import "testing"

func TestExchangeRateValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, rate string
		valid      bool
	}{
		{name: "positive", rate: "3.75", valid: true},
		{name: "small", rate: "0.000001", valid: true},
		{name: "zero", rate: "0"},
		{name: "negative", rate: "-1"},
		{name: "not finite", rate: "NaN"},
		{name: "infinite", rate: "Infinity"},
		{name: "exponent deliberately unsupported", rate: "1e200"},
		{name: "multiple dots", rate: "1.2.3"},
		{name: "no integer part", rate: ".5"},
		{name: "empty fraction", rate: "5."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := validateRate(ExchangeCreate{FromCurrency: "USD", ToCurrency: "PEN", Rate: tc.rate, Date: "2026-11-01", Context: "historical", Source: "Manual reference"})
			if (err == nil) != tc.valid {
				t.Fatalf("rate validation=%v", err)
			}
		})
	}
}
