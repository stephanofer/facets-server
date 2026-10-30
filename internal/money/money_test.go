package money_test

import (
	"encoding/json/v2"
	"github.com/stephanofer/facets-server/internal/money"
	"math"
	"testing"
)

func TestAmounts(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, currency, input string
		want                  int64
		invalid               bool
	}{
		{"PEN", "PEN", "120.00", 12000, false}, {"JPY", "JPY", "120", 120, false}, {"KWD", "KWD", "1.234", 1234, false},
		{"extra precision", "PEN", "1.000", 0, true}, {"zero scale precision", "JPY", "1.0", 0, true},
		{"unknown currency", "ZZZ", "1", 0, true}, {"overflow", "USD", "92233720368547758.08", 0, true},
		{"minimum", "USD", "-92233720368547758.08", math.MinInt64, false}, {"float syntax", "USD", "1e2", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := money.ParseCurrency(tc.currency)
			if err != nil {
				if !tc.invalid {
					t.Fatal(err)
				}
				return
			}
			a, err := money.ParseMajor(tc.input, c)
			if (err != nil) != tc.invalid || err == nil && a.Minor() != tc.want {
				t.Fatalf("minor=%d err=%v", a.Minor(), err)
			}
		})
	}
	c, _ := money.ParseCurrency("PEN")
	max, _ := money.New(math.MaxInt64, c)
	one, _ := money.New(1, c)
	min, _ := money.New(math.MinInt64, c)
	if _, err := money.Add(max, one); err == nil {
		t.Fatal("sum overflow accepted")
	}
	if _, err := money.Sub(min, one); err == nil {
		t.Fatal("subtraction overflow accepted")
	}
	if _, err := money.Multiply(min, -1); err == nil {
		t.Fatal("multiplication overflow accepted")
	}
	if a, err := money.Sub(min, min); err != nil || a.Minor() != 0 {
		t.Fatal(a, err)
	}
	for _, n := range []int64{10, -10, math.MinInt64, math.MaxInt64} {
		a, _ := money.New(n, c)
		parts, err := money.Split(a, 3)
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, p := range parts {
			total += p.Minor()
		}
		if total != n {
			t.Fatal("split lost minor units")
		}
	}
}

func TestConversionAndWire(t *testing.T) {
	t.Parallel()
	usd, _ := money.ParseCurrency("USD")
	jpy, _ := money.ParseCurrency("JPY")
	for _, tc := range []struct {
		name, rate  string
		minor, want int64
		invalid     bool
	}{
		{"half even down", "1", 250, 2, false}, {"half even up", "1", 350, 4, false}, {"negative half", "1", -250, -2, false},
		{"zero rate", "0", 100, 0, true}, {"negative rate", "-1", 100, 0, true}, {"nonfinite", "NaN", 100, 0, true}, {"missing", "", 100, 0, true}, {"exponent bounds", "1e9999", 100, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := money.New(tc.minor, usd)
			got, err := money.ConvertInformative(a, jpy, tc.rate)
			if (err != nil) != tc.invalid || err == nil && got.Minor() != tc.want {
				t.Fatalf("minor=%d err=%v", got.Minor(), err)
			}
		})
	}
	a, _ := money.New(math.MaxInt64, usd)
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var decoded money.Amount
	if err := json.Unmarshal(b, &decoded); err != nil || decoded.Minor() != a.Minor() {
		t.Fatal(string(b), err)
	}
	for _, bad := range []string{`{"amount_minor":12,"currency":"USD"}`, `{"amount_minor":"+12","currency":"USD"}`, `{"amount_minor":"12","currency":"USD","extra":true}`, `{"amount_minor":"0","currency":"XXX"}`} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func FuzzMinorRoundTrip(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(math.MinInt64))
	f.Fuzz(func(t *testing.T, n int64) {
		c, _ := money.ParseCurrency("PEN")
		a, _ := money.New(n, c)
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var got money.Amount
		if err := json.Unmarshal(b, &got); err != nil || got.Minor() != n {
			t.Fatal("roundtrip failed", err)
		}
	})
}
