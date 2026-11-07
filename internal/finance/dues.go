package finance

import (
	"context"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/money"
)

// DueAmount keeps an expectation distinct from a confirmed amount payable.
type DueAmount struct {
	Certainty    string  `json:"certainty" enum:"confirmed,estimated,range,unknown"`
	AmountMinor  *string `json:"amount_minor" nullable:"true" pattern:"^[1-9][0-9]{0,18}$"`
	MaximumMinor *string `json:"maximum_minor" nullable:"true" pattern:"^[1-9][0-9]{0,18}$"`
}

func (v DueAmount) parse() (pgtype.Int8, pgtype.Int8, error) {
	var amount, maximum pgtype.Int8
	parse := func(value *string) (pgtype.Int8, error) {
		if value == nil {
			return pgtype.Int8{}, nil
		}
		n, err := strconv.ParseInt(*value, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != *value {
			return pgtype.Int8{}, access.ErrInvalid
		}
		return pgtype.Int8{Int64: n, Valid: true}, nil
	}
	amount, err := parse(v.AmountMinor)
	if err != nil {
		return amount, maximum, err
	}
	maximum, err = parse(v.MaximumMinor)
	if err != nil {
		return amount, maximum, err
	}
	valid := v.Certainty == "unknown" && !amount.Valid && !maximum.Valid ||
		(v.Certainty == "confirmed" || v.Certainty == "estimated") && amount.Valid && !maximum.Valid ||
		v.Certainty == "range" && amount.Valid && maximum.Valid && maximum.Int64 >= amount.Int64
	if !valid {
		return amount, maximum, access.ErrInvalid
	}
	return amount, maximum, nil
}

type DueValue struct {
	Name   string    `json:"name" minLength:"1" maxLength:"100"`
	Date   string    `json:"date" format:"date"`
	Amount DueAmount `json:"amount"`
}

func (v DueValue) validate() error {
	if !text(v.Name, 100, false) {
		return access.ErrInvalid
	}
	if _, err := calendar.Parse(v.Date); err != nil {
		return access.ErrInvalid
	}
	_, _, err := v.Amount.parse()
	return err
}

type DueCreate struct {
	DueValue
	PlanningTarget
	Direction string `json:"direction" enum:"payment,receipt"`
	Confirm   bool   `json:"confirm"`
}

type DueEdit struct {
	DueValue
	FinancialVersionConfirmation
	State  string `json:"state" enum:"open,skipped,cancelled"`
	Reason string `json:"reason" minLength:"1" maxLength:"500"`
}

type DueView struct {
	ID                  string          `json:"id"`
	AccountID           string          `json:"account_id"`
	ResourceID          string          `json:"resource_id,omitempty" doc:"Accountless access configuration; recurring occurrences share their series resource."`
	Unassigned          bool            `json:"unassigned"`
	Direction           string          `json:"direction"`
	Name                string          `json:"name"`
	Date                string          `json:"date"`
	Currency            string          `json:"currency"`
	Scale               int32           `json:"scale"`
	Amount              DueAmount       `json:"amount"`
	AppliedMinor        string          `json:"applied_minor"`
	PendingMinor        *string         `json:"pending_minor" nullable:"true"`
	ExcessMinor         *string         `json:"excess_minor" nullable:"true"`
	Status              string          `json:"status" enum:"pending,upcoming,overdue,partial,paid,skipped,cancelled"`
	Overdue             bool            `json:"overdue"`
	Version             int64           `json:"version"`
	SeriesID            string          `json:"series_id,omitempty"`
	SeriesVersion       int64           `json:"series_version,omitzero"`
	OccurrenceOrdinal   int32           `json:"occurrence_ordinal"`
	OccurrenceSlot      int32           `json:"occurrence_slot"`
	CounterpartyID      string          `json:"counterparty_id,omitempty"`
	SuggestedCategoryID string          `json:"suggested_category_id,omitempty"`
	SubscriptionPlan    string          `json:"subscription_plan,omitempty"`
	Reminder            *AgendaReminder `json:"reminder,omitempty"`
}

func dueView(row financedb.AuthorizedDueItemsRow, today string) (DueView, error) {
	d := row.DueItem
	v := DueView{ID: d.ID, AccountID: d.AccountID.String, ResourceID: d.ResourceID.String, Unassigned: !d.AccountID.Valid, Direction: d.Direction, Name: d.Name, Date: d.DueDate.Time.Format(time.DateOnly), Currency: row.Currency, Scale: row.Scale, Version: d.Version, AppliedMinor: row.AppliedMinor, Amount: DueAmount{Certainty: d.Certainty}, Status: "pending"}
	v.SeriesID, v.SeriesVersion, v.OccurrenceOrdinal, v.OccurrenceSlot = row.SeriesID, row.SeriesVersion, row.OccurrenceOrdinal, row.OccurrenceSlot
	v.CounterpartyID, v.SuggestedCategoryID, v.SubscriptionPlan = row.CounterpartyID, row.SuggestedCategoryID, row.SubscriptionPlan
	if row.ReminderID != "" {
		v.Reminder = &AgendaReminder{ID: row.ReminderID, Version: row.ReminderVersion, Enabled: row.ReminderEnabled, ScheduledAt: row.ReminderAt.Time, Stale: optionalSeriesDate(row.ReminderDueDate) != v.Date}
		if row.ReminderDeliveredAt.Valid {
			v.Reminder.DeliveredAt = new(row.ReminderDeliveredAt.Time)
		}
	}
	if d.AmountMinor.Valid {
		v.Amount.AmountMinor = new(strconv.FormatInt(d.AmountMinor.Int64, 10))
	}
	if d.MaximumMinor.Valid {
		v.Amount.MaximumMinor = new(strconv.FormatInt(d.MaximumMinor.Int64, 10))
	}
	paid, ok := new(big.Int).SetString(row.AppliedMinor, 10)
	if !ok || paid.Sign() < 0 || !paid.IsInt64() {
		return v, access.ErrInvalid
	}
	if d.Certainty == "confirmed" {
		remaining := new(big.Int).Sub(big.NewInt(d.AmountMinor.Int64), paid)
		v.PendingMinor, v.ExcessMinor = new("0"), new("0")
		if remaining.Sign() > 0 {
			v.PendingMinor = new(remaining.String())
		} else {
			v.ExcessMinor = new(new(big.Int).Neg(remaining).String())
			v.Status = "paid"
		}
	}
	if v.Status != "paid" && paid.Sign() > 0 {
		v.Status = "partial" // An estimate or range never closes itself.
	}
	v.Overdue = v.Status != "paid" && v.Date < today
	if v.Status == "pending" {
		if v.Overdue {
			v.Status = "overdue"
		} else {
			date, _ := calendar.Parse(today)
			near, err := date.AddDays(7)
			if err == nil && v.Date <= near.String() {
				v.Status = "upcoming"
			}
		}
	}
	if d.State != "open" {
		v.Status, v.Overdue = d.State, false
	}
	return v, nil
}

func dueToday(ctx context.Context, tx pgx.Tx, space string) (string, error) {
	v, err := accessdb.New(tx).GetSpace(ctx, space)
	if err != nil {
		return "", err
	}
	zone, err := time.LoadLocation(v.Zone)
	if err != nil {
		return "", err
	}
	return time.Now().In(zone).Format(time.DateOnly), nil
}

func readDue(ctx context.Context, tx pgx.Tx, space, id, member string) (financedb.AuthorizedDueItemsRow, error) {
	if _, err := uuid.Parse(id); err != nil {
		return financedb.AuthorizedDueItemsRow{}, access.ErrInvalid
	}
	rows, err := financedb.New(tx).AuthorizedDueItems(ctx, financedb.AuthorizedDueItemsParams{SpaceID: space, ID: id, MemberID: member, PageSize: 1})
	if err != nil {
		return financedb.AuthorizedDueItemsRow{}, err
	}
	if len(rows) != 1 {
		return financedb.AuthorizedDueItemsRow{}, access.ErrNotFound
	}
	return rows[0], nil
}

func captureDue(ctx context.Context, tx pgx.Tx, space, id, author, reason string) error {
	return financedb.New(tx).CaptureDueItem(ctx, financedb.CaptureDueItemParams{SpaceID: space, ID: id, Author: author, Reason: reason})
}

func (s *Service) CreateDue(ctx context.Context, c access.SpaceCommand, v DueCreate) (string, error) {
	if err := v.DueValue.validate(); err != nil {
		return "", err
	}
	if err := v.PlanningTarget.validate(); err != nil || !v.Confirm || !slices.Contains([]string{"payment", "receipt"}, v.Direction) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "due.create", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if ref != "" {
			_, err := readDue(ctx, tx, c.SpaceID, ref, m.ID)
			return err
		}
		_, _, err := s.planningCurrency(ctx, tx, actor, c.SpaceID, v.PlanningTarget)
		return err
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		id := uuid.New().String()
		currency, scale, err := s.planningCurrency(ctx, tx, actor, c.SpaceID, v.PlanningTarget)
		if err != nil {
			return "", err
		}
		resource := ""
		if v.AccountID == "" {
			resource = id
			if err := createPlanningResource(ctx, tx, c.SpaceID, id, m.ID, actor.ID()); err != nil {
				return "", err
			}
		}
		date, _ := time.Parse(time.DateOnly, v.Date)
		amount, maximum, _ := v.Amount.parse()
		if err := financedb.New(tx).InsertDueItem(ctx, financedb.InsertDueItemParams{ID: id, SpaceID: c.SpaceID, AccountID: nullable(v.AccountID), ResourceID: nullable(resource), Currency: currency, Scale: scale, Direction: v.Direction, Name: v.Name, DueDate: pgtype.Date{Time: date, Valid: true}, Certainty: v.Amount.Certainty, AmountMinor: amount, MaximumMinor: maximum, Author: actor.ID()}); err != nil {
			return "", err
		}
		if err := captureDue(ctx, tx, c.SpaceID, id, actor.ID(), "Created manually"); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) Due(ctx context.Context, authorization, space, id string) (DueView, error) {
	var out DueView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		row, err := readDue(ctx, tx, space, id, m.ID)
		if err != nil {
			return err
		}
		today, err := dueToday(ctx, tx, space)
		if err == nil {
			out, err = dueView(row, today)
		}
		return err
	})
	return out, err
}

// Dues authorizes the expectation and every active sensitive relation before
// pagination. Uncertain pending amounts remain null, not an estimated zero.
func (s *Service) Dues(ctx context.Context, authorization, space, from, until, cursor string, limit int32) (access.Page[DueView], error) {
	return s.FilteredDues(ctx, authorization, space, from, until, cursor, limit, DueFilters{})
}

type DueFilters struct {
	Reminder   string `query:"reminder" enum:"scheduled,delivered,stale" doc:"Only the current member's in-app reminders; permissions still apply before pagination."`
	Unassigned bool   `query:"unassigned" doc:"Only expectations without an assigned financial account."`
	AccountID  string `query:"account_id" format:"uuid"`
	SeriesID   string `query:"series_id" format:"uuid"`
	Direction  string `query:"direction" enum:"payment,receipt"`
	Currency   string `query:"currency" minLength:"3" maxLength:"3"`
	Origin     string `query:"origin" enum:"standalone,recurring"`
	Status     string `query:"status" enum:"pending,upcoming,overdue,partial,paid,skipped,cancelled"`
}

func (f DueFilters) validate() error {
	if !slices.Contains([]string{"", "scheduled", "delivered", "stale"}, f.Reminder) {
		return access.ErrInvalid
	}
	for _, id := range []string{f.AccountID, f.SeriesID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return access.ErrInvalid
			}
		}
	}
	if f.Currency != "" {
		if _, err := money.ParseCurrency(f.Currency); err != nil {
			return access.ErrInvalid
		}
	}
	if !slices.Contains([]string{"", "payment", "receipt"}, f.Direction) || !slices.Contains([]string{"", "standalone", "recurring"}, f.Origin) || !slices.Contains([]string{"", "pending", "upcoming", "overdue", "partial", "paid", "skipped", "cancelled"}, f.Status) {
		return access.ErrInvalid
	}
	return nil
}

func (s *Service) FilteredDues(ctx context.Context, authorization, space, from, until, cursor string, limit int32, filters DueFilters) (access.Page[DueView], error) {
	out := access.Page[DueView]{Items: []DueView{}}
	if err := filters.validate(); err != nil {
		return out, err
	}
	if limit < 1 || limit > 100 {
		return out, access.ErrInvalid
	}
	parse := func(value string) (pgtype.Date, error) {
		if value == "" {
			return pgtype.Date{}, nil
		}
		d, err := calendar.Parse(value)
		if err != nil {
			return pgtype.Date{}, access.ErrInvalid
		}
		t, _ := time.Parse(time.DateOnly, d.String())
		return pgtype.Date{Time: t, Valid: true}, nil
	}
	start, err := parse(from)
	if err != nil {
		return out, err
	}
	end, err := parse(until)
	if err != nil || start.Valid && end.Valid && !start.Time.Before(end.Time) {
		return out, access.ErrInvalid
	}
	after, id := pgtype.Date{}, ""
	if cursor != "" {
		date, key, ok := strings.Cut(cursor, ":")
		after, err = parse(date)
		if !ok || err != nil || !after.Valid {
			return out, access.ErrInvalid
		}
		if _, err := uuid.Parse(key); err != nil {
			return out, access.ErrInvalid
		}
		id = key
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		today, err := dueToday(ctx, tx, space)
		if err != nil {
			return err
		}
		rows, err := financedb.New(tx).AuthorizedDueItems(ctx, financedb.AuthorizedDueItemsParams{SpaceID: space, MemberID: m.ID, FromDate: start, UntilDate: end, AfterDate: after, AfterID: id, PageSize: limit + 1,
			AccountFilter: filters.AccountID, SeriesFilter: filters.SeriesID, DirectionFilter: filters.Direction, CurrencyFilter: filters.Currency, OriginFilter: filters.Origin, StatusFilter: filters.Status, Today: seriesDate(today), UnassignedFilter: filters.Unassigned, ReminderFilter: filters.Reminder})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []DueView{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			last := rows[len(rows)-1].DueItem
			out.NextCursor = last.DueDate.Time.Format(time.DateOnly) + ":" + last.ID
		}
		for _, row := range rows {
			v, err := dueView(row, today)
			if err != nil {
				return err
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return out, err
}

func (s *Service) EditDue(ctx context.Context, c access.SpaceCommand, id string, v DueEdit) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	if v.Version < 1 || !v.Confirm || !text(v.Reason, 500, false) || !slices.Contains([]string{"open", "skipped", "cancelled"}, v.State) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "due.edit", "finance.write", struct {
		ID    string
		Value DueEdit
	}{id, v}
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		row, err := readDue(ctx, tx, c.SpaceID, id, m.ID)
		if err != nil {
			return err
		}
		if ref != "" {
			return nil
		}
		return s.authorizePlanningWrite(ctx, tx, actor, m, c.SpaceID, row.DueItem.AccountID.String, row.DueItem.ResourceID.String)
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		if _, err := q.LockDueItem(ctx, financedb.LockDueItemParams{SpaceID: c.SpaceID, ID: id}); err != nil {
			return "", absent(err)
		}
		row, err := readDue(ctx, tx, c.SpaceID, id, m.ID)
		if err != nil {
			return "", err
		}
		// Omission/cancellation must not conceal recorded applications. They
		// must be unlinked explicitly; neither action means a payment happened.
		if v.State != "open" && row.AppliedMinor != "0" {
			return "", access.ErrInvalid
		}
		date, _ := time.Parse(time.DateOnly, v.Date)
		amount, maximum, _ := v.Amount.parse()
		n, err := q.EditDueItem(ctx, financedb.EditDueItemParams{SpaceID: c.SpaceID, ID: id, Version: v.Version, Name: v.Name, DueDate: pgtype.Date{Time: date, Valid: true}, Certainty: v.Amount.Certainty, AmountMinor: amount, MaximumMinor: maximum, State: v.State})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		if err := captureDue(ctx, tx, c.SpaceID, id, actor.ID(), v.Reason); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}
