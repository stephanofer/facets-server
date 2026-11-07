package finance

import (
	"context"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/money"
)

type ExchangeCreate struct {
	FromCurrency string `json:"from_currency" minLength:"3" maxLength:"3"`
	ToCurrency   string `json:"to_currency" minLength:"3" maxLength:"3"`
	Rate         string `json:"rate" minLength:"1" maxLength:"80" pattern:"^[0-9]+(\\.[0-9]+)?$"`
	Date         string `json:"date" format:"date"`
	Context      string `json:"context" enum:"historical,current"`
	Source       string `json:"source" minLength:"1" maxLength:"200"`
	Replaces     string `json:"replaces,omitempty" format:"uuid"`
}
type ExchangeView struct {
	ID        string         `json:"id"`
	Reference ExchangeCreate `json:"reference"`
	Precision int32          `json:"precision_digits"`
	Rounding  string         `json:"rounding"`
	Author    string         `json:"author_id"`
	CreatedAt time.Time      `json:"created_at"`
}

func validateRate(v ExchangeCreate) (pgtype.Date, int32, error) {
	from, err := money.ParseCurrency(v.FromCurrency)
	if err != nil {
		return pgtype.Date{}, 0, access.ErrInvalid
	}
	to, err := money.ParseCurrency(v.ToCurrency)
	if err != nil || from == to || v.Context != "historical" && v.Context != "current" || !text(v.Source, 200, false) || len(v.Rate) < 1 || len(v.Rate) > 80 {
		return pgtype.Date{}, 0, access.ErrInvalid
	}
	d, err := time.Parse(time.DateOnly, v.Date)
	if err != nil || d.Year() < 1 || d.Year() > 9999 {
		return pgtype.Date{}, 0, access.ErrInvalid
	}
	dots, digits, nonzero := 0, 0, false
	for _, ch := range v.Rate {
		if ch == '.' {
			dots++
			continue
		}
		if ch < '0' || ch > '9' {
			return pgtype.Date{}, 0, access.ErrInvalid
		}
		digits++
		if ch != '0' {
			nonzero = true
		}
	}
	if dots > 1 || !nonzero || strings.HasPrefix(v.Rate, ".") || strings.HasSuffix(v.Rate, ".") {
		return pgtype.Date{}, 0, access.ErrInvalid
	}
	// Validate representation/precision without making a rounded amount effective.
	zero, _ := money.New(0, from)
	if _, err := money.ConvertInformative(zero, to, v.Rate); err != nil {
		return pgtype.Date{}, 0, access.ErrInvalid
	}
	return pgtype.Date{Time: d, Valid: true}, int32(digits), nil
}

func exchangeView(v financedb.ExchangeReference) ExchangeView {
	return ExchangeView{ID: v.ID, Reference: ExchangeCreate{FromCurrency: v.FromCurrency, ToCurrency: v.ToCurrency, Rate: v.Rate, Date: v.EffectiveDate.Time.Format(time.DateOnly), Context: v.Context, Source: v.Source, Replaces: v.Replaces.String}, Precision: v.PrecisionDigits, Rounding: v.Rounding, Author: v.Author, CreatedAt: v.CreatedAt.Time}
}

func (s *Service) CreateExchange(ctx context.Context, c access.SpaceCommand, v ExchangeCreate) (string, error) {
	d, precision, err := validateRate(v)
	if err != nil {
		return "", err
	}
	c.Kind = "exchange.create"
	return s.catalogCommand(ctx, c, v, func(ctx context.Context, tx pgx.Tx, a access.Actor, _ accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		if v.Replaces != "" {
			old, err := q.ExchangeReference(ctx, financedb.ExchangeReferenceParams{SpaceID: c.SpaceID, ID: v.Replaces})
			if err != nil {
				return "", absent(err)
			}
			if old.FromCurrency != v.FromCurrency || old.ToCurrency != v.ToCurrency || old.Context != v.Context || old.EffectiveDate.Time != d.Time {
				return "", access.ErrInvalid
			}
		}
		id := uuid.New().String()
		if err := q.InsertExchangeReference(ctx, financedb.InsertExchangeReferenceParams{ID: id, SpaceID: c.SpaceID, FromCurrency: v.FromCurrency, ToCurrency: v.ToCurrency, Rate: v.Rate, EffectiveDate: d, Context: v.Context, Source: v.Source, PrecisionDigits: precision, Replaces: nullable(v.Replaces), Author: a.ID()}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, a, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) Exchange(ctx context.Context, authorization, space, id string) (ExchangeView, error) {
	var out ExchangeView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, _ accessdb.Membership) error {
		v, err := financedb.New(tx).ExchangeReference(ctx, financedb.ExchangeReferenceParams{SpaceID: space, ID: id})
		if err != nil {
			return absent(err)
		}
		out = exchangeView(v)
		return nil
	})
	return out, err
}

func (s *Service) Exchanges(ctx context.Context, authorization, space, cursor, from, to, contextKind string, limit int32) (access.Page[ExchangeView], error) {
	out := access.Page[ExchangeView]{Items: []ExchangeView{}}
	if err := validPage(cursor, limit); err != nil {
		return out, err
	}
	for _, code := range []string{from, to} {
		if code != "" {
			if _, err := money.ParseCurrency(code); err != nil {
				return out, access.ErrInvalid
			}
		}
	}
	if contextKind != "" && contextKind != "historical" && contextKind != "current" {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, _ accessdb.Membership) error {
		rows, err := financedb.New(tx).ExchangeReferences(ctx, financedb.ExchangeReferencesParams{SpaceID: space, Cursor: cursor, FromCurrency: from, ToCurrency: to, Context: contextKind, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items = []ExchangeView{}
		out.NextCursor = ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].ID
		}
		for _, row := range rows {
			out.Items = append(out.Items, exchangeView(row))
		}
		return nil
	})
	return out, err
}

type ExchangeConversion struct {
	AmountMinor    string `json:"amount_minor" maxLength:"20"`
	Currency       string `json:"currency" minLength:"3" maxLength:"3"`
	TargetCurrency string `json:"target_currency" minLength:"3" maxLength:"3"`
	ReferenceID    string `json:"reference_id,omitempty" format:"uuid"`
	Date           string `json:"date" format:"date"`
	Context        string `json:"context" enum:"historical,current"`
}
type ConversionView struct {
	AmountMinor     *string `json:"amount_minor" nullable:"true"`
	Currency        string  `json:"currency"`
	ReferenceID     string  `json:"reference_id,omitempty"`
	Complete        bool    `json:"complete"`
	Reason          string  `json:"reason,omitempty"`
	InformativeOnly bool    `json:"informative_only"`
}

func (s *Service) Convert(ctx context.Context, authorization, space string, v ExchangeConversion) (ConversionView, error) {
	out := ConversionView{Currency: v.TargetCurrency, InformativeOnly: true}
	from, err := money.ParseCurrency(v.Currency)
	if err != nil {
		return out, access.ErrInvalid
	}
	to, err := money.ParseCurrency(v.TargetCurrency)
	if err != nil {
		return out, access.ErrInvalid
	}
	amount, err := money.ParseMinor(v.AmountMinor, from)
	if err != nil {
		return out, access.ErrInvalid
	}
	d, err := time.Parse(time.DateOnly, v.Date)
	if err != nil || d.Year() < 1 || d.Year() > 9999 || v.Context != "current" && v.Context != "historical" {
		return out, access.ErrInvalid
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, _ accessdb.Membership) error {
		if from == to {
			if v.ReferenceID != "" {
				return access.ErrInvalid
			}
			out.AmountMinor = new(v.AmountMinor)
			out.Complete = true
			return nil
		}
		if v.ReferenceID == "" {
			out.Reason = "missing_exchange_reference"
			return nil
		}
		r, err := financedb.New(tx).ExchangeReference(ctx, financedb.ExchangeReferenceParams{SpaceID: space, ID: v.ReferenceID})
		if err != nil {
			return absent(err)
		}
		if r.FromCurrency != v.Currency || r.ToCurrency != v.TargetCurrency || r.Context != v.Context || r.EffectiveDate.Time.After(d) || v.Context == "historical" && !r.EffectiveDate.Time.Equal(d) {
			return access.ErrInvalid
		}
		converted, err := money.ConvertInformative(amount, to, r.Rate)
		if err != nil {
			return access.ErrInvalid
		}
		out.AmountMinor = new(strconv.FormatInt(converted.Minor(), 10))
		out.ReferenceID = r.ID
		out.Complete = true
		return nil
	})
	return out, err
}
