package finance

import (
	"context"
	"slices"
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

type OperationFilter struct {
	From           string `query:"from" format:"date"`
	To             string `query:"to" format:"date"`
	AccountID      string `query:"account_id" format:"uuid"`
	Currency       string `query:"currency" minLength:"3" maxLength:"3"`
	CategoryID     string `query:"category_id" format:"uuid"`
	MerchantID     string `query:"merchant_id" format:"uuid"`
	CounterpartyID string `query:"counterparty_id" format:"uuid"`
	TagID          string `query:"tag_id" format:"uuid"`
	Kind           string `query:"kind" enum:"income,expense,transfer,refund,adjustment"`
	MinMinor       string `query:"min_minor" pattern:"^(0|[1-9][0-9]{0,18})$"`
	MaxMinor       string `query:"max_minor" pattern:"^(0|[1-9][0-9]{0,18})$"`
	Query          string `query:"q" maxLength:"100"`
}

type OperationSummary struct {
	ID                 string `json:"id"`
	Kind               string `json:"kind"`
	Date               string `json:"date"`
	Version            int64  `json:"version"`
	Note               string `json:"note"`
	Exceptional        bool   `json:"exceptional"`
	MerchantID         string `json:"merchant_id,omitempty"`
	CounterpartyID     string `json:"counterparty_id,omitempty"`
	PrimaryAmountMinor string `json:"primary_amount_minor"`
	PrimaryCurrency    string `json:"primary_currency"`
}

func queryDate(value, fallback string) (pgtype.Date, error) {
	if value == "" {
		value = fallback
	}
	d, err := time.Parse(time.DateOnly, value)
	if err != nil || d.Year() < 1 || d.Year() > 9999 {
		return pgtype.Date{}, access.ErrInvalid
	}
	return pgtype.Date{Time: d, Valid: true}, nil
}

func validateQueryIDs(ids ...string) error {
	for _, id := range ids {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return access.ErrInvalid
			}
		}
	}
	return nil
}

func (s *Service) SearchOperations(ctx context.Context, authorization, space string, v OperationFilter, cursor string, limit int32) (access.Page[OperationSummary], error) {
	out := access.Page[OperationSummary]{Items: []OperationSummary{}}
	if limit < 1 || limit > 100 || !text(v.Query, 100, true) || v.Kind != "" && !slices.Contains([]string{"income", "expense", "transfer", "refund", "adjustment"}, v.Kind) {
		return out, access.ErrInvalid
	}
	if err := validateQueryIDs(v.AccountID, v.CategoryID, v.MerchantID, v.CounterpartyID, v.TagID); err != nil {
		return out, err
	}
	if v.Currency != "" {
		if _, err := money.ParseCurrency(v.Currency); err != nil {
			return out, access.ErrInvalid
		}
	}
	from, err := queryDate(v.From, "0001-01-01")
	if err != nil {
		return out, err
	}
	to, err := queryDate(v.To, "9999-12-31")
	if err != nil || !from.Time.Before(to.Time) {
		return out, access.ErrInvalid
	}
	lo, hi := "0", "9223372036854775808"
	if v.MinMinor != "" {
		lo = v.MinMinor
	}
	if v.MaxMinor != "" {
		hi = v.MaxMinor
	}
	minAmount, e1 := strconv.ParseUint(lo, 10, 64)
	maxAmount, e2 := strconv.ParseUint(hi, 10, 64)
	// A negative int64 adjustment can have magnitude 2^63.
	if e1 != nil || e2 != nil || maxAmount > 1<<63 || maxAmount < minAmount || strconv.FormatUint(minAmount, 10) != lo || strconv.FormatUint(maxAmount, 10) != hi {
		return out, access.ErrInvalid
	}
	date, id := "0001-01-01", ""
	if cursor != "" {
		var ok bool
		date, id, ok = strings.Cut(cursor, ":")
		if !ok || validateQueryIDs(id) != nil || id == "" {
			return out, access.ErrInvalid
		}
	}
	cutoff, err := queryDate(date, "0001-01-01")
	if err != nil {
		return out, err
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		rows, err := financedb.New(tx).SearchOperations(ctx, financedb.SearchOperationsParams{SpaceID: space, MemberID: m.ID, FromDate: from, ToDate: to, CursorDate: cutoff, CursorID: id, Kind: v.Kind, AccountID: v.AccountID, Currency: v.Currency, CategoryID: v.CategoryID, MerchantID: v.MerchantID, CounterpartyID: v.CounterpartyID, TagID: v.TagID, MinMinor: lo, MaxMinor: hi, Query: v.Query, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []OperationSummary{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			last := rows[len(rows)-1]
			out.NextCursor = last.EffectiveDate.Time.Format(time.DateOnly) + ":" + last.ID
		}
		for _, r := range rows {
			out.Items = append(out.Items, OperationSummary{ID: r.ID, Kind: r.Kind, Date: r.EffectiveDate.Time.Format(time.DateOnly), Version: r.Revision, Note: r.Note, Exceptional: r.Exceptional, MerchantID: r.MerchantID.String, CounterpartyID: r.CounterpartyID.String, PrimaryAmountMinor: strconv.FormatInt(r.AmountMinor, 10), PrimaryCurrency: r.Currency})
		}
		return nil
	})
	return out, err
}

type RecentSelection struct {
	OperationID string    `json:"operation_id"`
	AccountID   string    `json:"account_id"`
	CategoryID  string    `json:"category_id,omitempty"`
	MerchantID  string    `json:"merchant_id,omitempty"`
	SelectedAt  time.Time `json:"selected_at"`
}

func (s *Service) OperationSuggestions(ctx context.Context, authorization, space, kind, merchant string, limit int32) ([]RecentSelection, error) {
	out := []RecentSelection{}
	if !slices.Contains([]string{"income", "expense", "refund"}, kind) || limit < 1 || limit > 20 {
		return out, access.ErrInvalid
	}
	if err := validateQueryIDs(merchant); err != nil {
		return out, err
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, a access.Actor, m accessdb.Membership) error {
		if m.Role == "reader" {
			out = []RecentSelection{}
			return nil
		}
		rows, err := financedb.New(tx).RecentOperationSelections(ctx, financedb.RecentOperationSelectionsParams{SpaceID: space, Author: a.ID(), MemberID: m.ID, Kind: kind, MerchantID: merchant, PageSize: limit})
		if err != nil {
			return err
		}
		out = []RecentSelection{}
		for _, r := range rows {
			out = append(out, RecentSelection{OperationID: r.ID, AccountID: r.AccountID, CategoryID: r.CategoryID, MerchantID: r.MerchantID, SelectedAt: r.RecordedAt.Time})
		}
		return nil
	})
	return out, err
}

type FinancialQuery struct {
	From       string `query:"from" format:"date" required:"true"`
	To         string `query:"to" format:"date" required:"true"`
	AccountID  string `query:"account_id" format:"uuid"`
	Currency   string `query:"currency" minLength:"3" maxLength:"3"`
	CategoryID string `query:"category_id" format:"uuid"`
}

type FinancialTotal struct {
	Reading    string `json:"reading" enum:"position,economic,cash"`
	Kind       string `json:"kind"`
	Currency   string `json:"currency"`
	Scale      int32  `json:"scale"`
	KnownMinor string `json:"known_minor"`
	Complete   bool   `json:"complete"`
	Confirmed  bool   `json:"confirmed"`
}

type FinancialSummary struct {
	Scope        string           `json:"scope"`
	From         string           `json:"from"`
	To           string           `json:"to"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Totals       []FinancialTotal `json:"totals"`
	HistoryScope string           `json:"history_scope"`
	Conversion   string           `json:"conversion"`
}

// FinancialSummary keeps original currencies and the three independent readings.
// These are common query cases, not a second financial ledger. Position is current;
// economic and cash use the same half-open effective-date interval.
func (s *Service) FinancialSummary(ctx context.Context, authorization, space string, v FinancialQuery) (FinancialSummary, error) {
	out := FinancialSummary{Scope: "authorized_accounts_only", From: v.From, To: v.To, HistoryScope: "in_tracking_only", Conversion: "original_currencies_only", Totals: []FinancialTotal{}}
	if v.From == "" || v.To == "" {
		return out, access.ErrInvalid
	}
	from, err := queryDate(v.From, "")
	if err != nil {
		return out, err
	}
	to, err := queryDate(v.To, "")
	if err != nil || !from.Time.Before(to.Time) {
		return out, access.ErrInvalid
	}
	if err := validateQueryIDs(v.AccountID, v.CategoryID); err != nil {
		return out, err
	}
	if v.Currency != "" {
		if _, err := money.ParseCurrency(v.Currency); err != nil {
			return out, access.ErrInvalid
		}
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		q := financedb.New(tx)
		positions, err := q.PositionTotals(ctx, financedb.PositionTotalsParams{SpaceID: space, MemberID: m.ID, AccountID: v.AccountID, Currency: v.Currency})
		if err != nil {
			return err
		}
		economic, err := q.EconomicTotals(ctx, financedb.EconomicTotalsParams{SpaceID: space, MemberID: m.ID, FromDate: from, ToDate: to, AccountID: v.AccountID, Currency: v.Currency, CategoryID: v.CategoryID})
		if err != nil {
			return err
		}
		cash, err := q.CashTotals(ctx, financedb.CashTotalsParams{SpaceID: space, MemberID: m.ID, FromDate: from, ToDate: to, AccountID: v.AccountID, Currency: v.Currency})
		if err != nil {
			return err
		}
		out.Totals = []FinancialTotal{}
		for _, r := range positions {
			out.Totals = append(out.Totals, FinancialTotal{Reading: "position", Kind: r.Position, Currency: r.Currency, Scale: r.Scale, KnownMinor: r.KnownMinor, Complete: r.Complete, Confirmed: r.Confirmed})
		}
		for _, r := range economic {
			out.Totals = append(out.Totals, FinancialTotal{Reading: "economic", Kind: r.Direction, Currency: r.Currency, Scale: r.Scale, KnownMinor: r.AmountMinor, Complete: true, Confirmed: true})
		}
		for _, r := range cash {
			out.Totals = append(out.Totals, FinancialTotal{Reading: "cash", Kind: r.Kind, Currency: r.Currency, Scale: r.Scale, KnownMinor: r.AmountMinor, Complete: true, Confirmed: true})
		}
		for _, r := range out.Totals {
			if _, err := strconv.ParseInt(r.KnownMinor, 10, 64); err != nil {
				return access.ErrInvalid
			}
		}
		out.UpdatedAt = time.Now().UTC()
		return nil
	})
	return out, err
}
