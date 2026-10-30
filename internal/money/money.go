// Package money keeps effective amounts exact and conversion rounding explicit.
package money

import (
	"encoding/json/v2"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/cockroachdb/apd/v3"
	"golang.org/x/text/currency"
)

type Currency struct {
	code  string
	scale int32
}

func ParseCurrency(code string) (Currency, error) {
	if len(code) != 3 || code != strings.ToUpper(code) {
		return Currency{}, errors.New("invalid currency")
	}
	unit, err := currency.ParseISO(code)
	if err != nil || unit == currency.XXX {
		return Currency{}, errors.New("unsupported currency")
	}
	scale, _ := currency.Standard.Rounding(unit)
	if scale < 0 || scale > 9 {
		return Currency{}, errors.New("unsupported currency scale")
	}
	return Currency{code: code, scale: int32(scale)}, nil
}
func (c Currency) Code() string { return c.code }
func (c Currency) Scale() int32 { return c.scale }

type Amount struct {
	minor    int64
	currency Currency
}

func New(minor int64, c Currency) (Amount, error) {
	if c.code == "" {
		return Amount{}, errors.New("currency required")
	}
	return Amount{minor: minor, currency: c}, nil
}
func (a Amount) Minor() int64       { return a.minor }
func (a Amount) Currency() Currency { return a.currency }

func (a Amount) MarshalJSON() ([]byte, error) {
	if a.currency.code == "" {
		return nil, errors.New("invalid monetary amount")
	}
	return json.Marshal(struct {
		AmountMinor string `json:"amount_minor"`
		Currency    string `json:"currency"`
	}{AmountMinor: strconv.FormatInt(a.minor, 10), Currency: a.currency.code})
}
func (a *Amount) UnmarshalJSON(data []byte) error {
	var wire struct {
		AmountMinor string `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if err := json.Unmarshal(data, &wire, json.RejectUnknownMembers(true)); err != nil {
		return errors.New("invalid monetary representation")
	}
	c, err := ParseCurrency(wire.Currency)
	if err != nil {
		return err
	}
	value, err := ParseMinor(wire.AmountMinor, c)
	if err == nil {
		*a = value
	}
	return err
}

// ParseMinor accepts canonical integer strings, never JSON numbers or floats.
func ParseMinor(s string, c Currency) (Amount, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != s {
		return Amount{}, errors.New("invalid minor amount")
	}
	return New(n, c)
}

// ParseMajor rejects excess precision even when the extra digits are zero.
func ParseMajor(s string, c Currency) (Amount, error) {
	if c.code == "" || len(s) > 32 || s == "" {
		return Amount{}, errors.New("invalid amount")
	}
	negative := strings.HasPrefix(s, "-")
	unsigned := strings.TrimPrefix(s, "-")
	whole, fraction, found := strings.Cut(unsigned, ".")
	if whole == "" || (len(whole) > 1 && whole[0] == '0') || (found && fraction == "") || len(fraction) > int(c.scale) {
		return Amount{}, errors.New("invalid precision")
	}
	for _, ch := range whole + fraction {
		if ch < '0' || ch > '9' {
			return Amount{}, errors.New("invalid amount")
		}
	}
	digits := whole + fraction + strings.Repeat("0", int(c.scale)-len(fraction))
	if negative {
		digits = "-" + digits
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return Amount{}, errors.New("amount out of range")
	}
	return New(n, c)
}

func Add(a, b Amount) (Amount, error) {
	if a.currency.code == "" || a.currency != b.currency {
		return Amount{}, errors.New("currency mismatch")
	}
	if (b.minor > 0 && a.minor > math.MaxInt64-b.minor) || (b.minor < 0 && a.minor < math.MinInt64-b.minor) {
		return Amount{}, errors.New("amount overflow")
	}
	return New(a.minor+b.minor, a.currency)
}
func Sub(a, b Amount) (Amount, error) {
	if a.currency.code == "" || a.currency != b.currency {
		return Amount{}, errors.New("currency mismatch")
	}
	if (b.minor < 0 && a.minor > math.MaxInt64+b.minor) || (b.minor > 0 && a.minor < math.MinInt64+b.minor) {
		return Amount{}, errors.New("amount overflow")
	}
	return New(a.minor-b.minor, a.currency)
}
func Multiply(a Amount, n int64) (Amount, error) {
	x := new(big.Int).Mul(big.NewInt(a.minor), big.NewInt(n))
	if !x.IsInt64() {
		return Amount{}, errors.New("amount overflow")
	}
	return New(x.Int64(), a.currency)
}

// Split assigns residual minor units to the first parts, including negative totals.
func Split(a Amount, parts int) ([]Amount, error) {
	if parts < 1 || parts > 10000 || a.currency.code == "" {
		return nil, errors.New("invalid split")
	}
	result := make([]Amount, parts)
	q, r := a.minor/int64(parts), a.minor%int64(parts)
	for i := range parts {
		n := q
		if r > 0 {
			n++
			r--
		} else if r < 0 {
			n--
			r++
		}
		result[i] = Amount{minor: n, currency: a.currency}
	}
	return result, nil
}

// ConvertInformative accepts target-major/source-major rates. Currency/date/source
// provenance belongs to the exchange reference introduced with accounts in block 4.
// No effective account amount is inferred by this presentation-only calculation.
func ConvertInformative(a Amount, target Currency, rate string) (Amount, error) {
	if a.currency.code == "" || target.code == "" || len(rate) > 80 {
		return Amount{}, errors.New("invalid conversion")
	}
	ctx := apd.Context{Precision: 80, MaxExponent: 100, MinExponent: -100, Traps: apd.DefaultTraps | apd.Inexact, Rounding: apd.RoundHalfEven}
	r, _, err := ctx.NewFromString(rate)
	if err != nil || r.Form != apd.Finite || r.Negative || r.IsZero() {
		return Amount{}, errors.New("invalid exchange rate")
	}
	var product apd.Decimal
	if _, err := ctx.Mul(&product, apd.New(a.minor, -a.currency.scale), r); err != nil {
		return Amount{}, errors.New("conversion precision exceeded")
	}
	// Rounding is permitted only once, at the final target minor unit.
	ctx.Traps = apd.DefaultTraps
	var rounded apd.Decimal
	if _, err := ctx.Quantize(&rounded, &product, -target.scale); err != nil {
		return Amount{}, errors.New("conversion out of range")
	}
	rounded.Exponent = 0
	n, err := rounded.Int64()
	if err != nil {
		return Amount{}, errors.New("conversion overflow")
	}
	return New(n, target)
}
