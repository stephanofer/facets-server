package finance

import (
	"context"
	"math"
	"slices"
	"strconv"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/access/accessdb"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
)

type SeriesValue struct {
	Name                string                 `json:"name" minLength:"1" maxLength:"100"`
	Amount              DueAmount              `json:"amount"`
	Recurrence          calendar.Recurrence    `json:"recurrence"`
	CounterpartyID      string                 `json:"counterparty_id,omitempty" format:"uuid"`
	SuggestedCategoryID string                 `json:"suggested_category_id,omitempty" format:"uuid"`
	Subscription        *FinancialSubscription `json:"subscription,omitempty"`
}

func (v SeriesValue) validate() error {
	if !text(v.Name, 100, false) || v.Recurrence.Validate() != nil {
		return access.ErrInvalid
	}
	for _, id := range []string{v.CounterpartyID, v.SuggestedCategoryID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return access.ErrInvalid
			}
		}
	}
	if v.Subscription != nil {
		if err := v.Subscription.validate(v.Recurrence.Start); err != nil {
			return err
		}
	}
	_, _, err := v.Amount.parse()
	return err
}

type SeriesCreate struct {
	SeriesValue
	PlanningTarget
	Direction string `json:"direction" enum:"payment,receipt"`
	Confirm   bool   `json:"confirm"`
}

type SeriesEdit struct {
	SeriesValue
	FinancialVersionConfirmation
	EffectiveFrom string `json:"effective_from" format:"date" doc:"Must not precede the previous materialization boundary; existing dues are preserved."`
	State         string `json:"state" enum:"active,paused,ended,cancelled"`
	Reason        string `json:"reason" minLength:"1" maxLength:"500"`
}

type SeriesVersionView struct {
	SeriesValue
	Version        int64                    `json:"version"`
	State          string                   `json:"state"`
	EffectiveFrom  string                   `json:"effective_from"`
	GeneratedUntil string                   `json:"generated_until" doc:"Exclusive durable materialization boundary, not a payment date."`
	Reason         string                   `json:"reason"`
	Equivalents    *SubscriptionEquivalents `json:"equivalents,omitempty"`
}

type SeriesView struct {
	SeriesVersionView
	ID                string              `json:"id"`
	AccountID         string              `json:"account_id"`
	Direction         string              `json:"direction"`
	Currency          string              `json:"currency"`
	Scale             int32               `json:"scale"`
	ResourceID        string              `json:"resource_id,omitempty"`
	Unassigned        bool                `json:"unassigned"`
	LastVisibleCharge *SubscriptionCharge `json:"last_visible_charge,omitempty" doc:"Latest currently authorized applied movement, not an inferred provider charge."`
}

func seriesVersionView(v financedb.RecurringSeriesVersion) SeriesVersionView {
	out := SeriesVersionView{
		Name: v.Name, Amount: DueAmount{Certainty: v.Certainty},
		Recurrence: calendar.Recurrence{Start: v.StartDate.Time.Format(time.DateOnly), Frequency: v.Frequency, Interval: int(v.IntervalCount), FirstDay: int(v.FirstDay), SecondDay: int(v.SecondDay), Count: int(v.RepetitionCount)},
		Version:    v.Version, State: v.State, Reason: v.Reason,
		EffectiveFrom: v.EffectiveFrom.Time.Format(time.DateOnly), GeneratedUntil: v.GeneratedUntil.Time.Format(time.DateOnly),
	}
	if v.AmountMinor.Valid {
		out.Amount.AmountMinor = new(strconv.FormatInt(v.AmountMinor.Int64, 10))
	}
	if v.MaximumMinor.Valid {
		out.Amount.MaximumMinor = new(strconv.FormatInt(v.MaximumMinor.Int64, 10))
	}
	if v.EndDate.Valid {
		out.Recurrence.End = v.EndDate.Time.Format(time.DateOnly)
	}
	out.CounterpartyID, out.SuggestedCategoryID = v.CounterpartyID.String, v.SuggestedCategoryID.String
	if v.SubscriptionPlan.Valid {
		out.Subscription = &FinancialSubscription{Plan: v.SubscriptionPlan.String, TrialUntil: optionalSeriesDate(v.TrialUntil), RenewalOn: optionalSeriesDate(v.RenewalOn), CancelBy: optionalSeriesDate(v.CancelBy), CancellationOn: optionalSeriesDate(v.CancellationOn)}
		out.Equivalents = subscriptionEquivalents(out.SeriesValue)
	}
	return out
}

func seriesView(row financedb.AuthorizedSeriesRow) SeriesView {
	return SeriesView{SeriesVersionView: seriesVersionView(row.RecurringSeriesVersion), ID: row.RecurringSeries.ID, AccountID: row.RecurringSeries.AccountID.String, ResourceID: row.RecurringSeries.ResourceID.String, Unassigned: !row.RecurringSeries.AccountID.Valid, Direction: row.RecurringSeries.Direction, Currency: row.Currency, Scale: row.Scale}
}

func readSeries(ctx context.Context, tx pgx.Tx, space, id, member string) (SeriesView, error) {
	if _, err := uuid.Parse(id); err != nil {
		return SeriesView{}, access.ErrInvalid
	}
	rows, err := financedb.New(tx).AuthorizedSeries(ctx, financedb.AuthorizedSeriesParams{SpaceID: space, ID: id, MemberID: member, PageSize: 1})
	if err != nil {
		return SeriesView{}, err
	}
	if len(rows) != 1 {
		return SeriesView{}, access.ErrNotFound
	}
	return seriesView(rows[0]), nil
}

func seriesDate(value string) pgtype.Date {
	if value == "" {
		return pgtype.Date{}
	}
	d, _ := time.Parse(time.DateOnly, value) // All callers validate civil dates first.
	return pgtype.Date{Time: d, Valid: true}
}

func insertSeriesVersion(ctx context.Context, tx pgx.Tx, space, id, author string, version int64, v SeriesValue, effective, state, reason string) error {
	if err := validateSeriesCatalogs(ctx, tx, space, id, v); err != nil {
		return err
	}
	amount, maximum, _ := v.Amount.parse()
	r := v.Recurrence
	interval, first, second, count := r.Interval, r.FirstDay, r.SecondDay, r.Count
	if interval < 0 || interval > 1200 || first < 0 || first > 31 || second < 0 || second > 31 || count < 0 || count > 100000 {
		return access.ErrInvalid
	}
	var subscription FinancialSubscription
	if v.Subscription != nil {
		subscription = *v.Subscription
	}
	return financedb.New(tx).InsertSeriesVersion(ctx, financedb.InsertSeriesVersionParams{
		SpaceID: space, SeriesID: id, Version: version, Name: v.Name, Certainty: v.Amount.Certainty, AmountMinor: amount, MaximumMinor: maximum,
		StartDate: seriesDate(r.Start), Frequency: r.Frequency, IntervalCount: int32(interval), FirstDay: int32(first), SecondDay: int32(second), EndDate: seriesDate(r.End), RepetitionCount: int32(count),
		EffectiveFrom: seriesDate(effective), State: state, Author: author, Reason: reason,
		CounterpartyID: nullable(v.CounterpartyID), SuggestedCategoryID: nullable(v.SuggestedCategoryID), SubscriptionPlan: nullable(subscription.Plan), TrialUntil: seriesDate(subscription.TrialUntil), RenewalOn: seriesDate(subscription.RenewalOn), CancelBy: seriesDate(subscription.CancelBy), CancellationOn: seriesDate(subscription.CancellationOn),
	})
}

func (s *Service) CreateSeries(ctx context.Context, c access.SpaceCommand, v SeriesCreate) (string, error) {
	if err := v.SeriesValue.validate(); err != nil {
		return "", err
	}
	if err := v.PlanningTarget.validate(); err != nil || !v.Confirm || !slices.Contains([]string{"payment", "receipt"}, v.Direction) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "series.create", "finance.write", v
	return s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		if ref != "" {
			_, err := readSeries(ctx, tx, c.SpaceID, ref, m.ID)
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
		if err := financedb.New(tx).InsertRecurringSeries(ctx, financedb.InsertRecurringSeriesParams{ID: id, SpaceID: c.SpaceID, AccountID: nullable(v.AccountID), ResourceID: nullable(resource), Currency: currency, Scale: scale, Direction: v.Direction, Author: actor.ID()}); err != nil {
			return "", err
		}
		if err := insertSeriesVersion(ctx, tx, c.SpaceID, id, actor.ID(), 1, v.SeriesValue, v.Recurrence.Start, "active", "Created manually"); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) Series(ctx context.Context, authorization, space, id string) (SeriesView, error) {
	var out SeriesView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		var err error
		out, err = readSeries(ctx, tx, space, id, m.ID)
		if err != nil || out.Subscription == nil {
			return err
		}
		rows, err := financedb.New(tx).AuthorizedDueItems(ctx, financedb.AuthorizedDueItemsParams{SpaceID: space, MemberID: m.ID, SeriesFilter: id, ChargeOnly: true, PageSize: 1})
		if err != nil {
			return err
		}
		if len(rows) == 1 {
			row := rows[0]
			out.LastVisibleCharge = &SubscriptionCharge{OperationID: row.ChargeOperationID, Date: row.ChargeDate.Time.Format(time.DateOnly), AmountMinor: row.ChargeMinor, Currency: row.ChargeCurrency}
		}
		return nil
	})
	return out, err
}

func (s *Service) ListSeries(ctx context.Context, authorization, space, cursor string, limit int32) (access.Page[SeriesView], error) {
	out := access.Page[SeriesView]{Items: []SeriesView{}}
	if limit < 1 || limit > 100 {
		return out, access.ErrInvalid
	}
	if cursor != "" {
		if _, err := uuid.Parse(cursor); err != nil {
			return out, access.ErrInvalid
		}
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		rows, err := financedb.New(tx).AuthorizedSeries(ctx, financedb.AuthorizedSeriesParams{SpaceID: space, MemberID: m.ID, AfterID: cursor, PageSize: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []SeriesView{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = rows[len(rows)-1].RecurringSeries.ID
		}
		for _, row := range rows {
			out.Items = append(out.Items, seriesView(row))
		}
		return nil
	})
	return out, err
}

// EditSeries only replaces unmaterialized expectations. Existing dues (including
// their payments, omissions and explicit date/amount corrections) never change.
func (s *Service) EditSeries(ctx context.Context, c access.SpaceCommand, id string, v SeriesEdit) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	if _, err := calendar.Parse(v.EffectiveFrom); err != nil || v.EffectiveFrom < v.Recurrence.Start || v.Version < 1 || v.Version == math.MaxInt64 || !v.Confirm || !text(v.Reason, 500, false) || !slices.Contains([]string{"active", "paused", "ended", "cancelled"}, v.State) {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "series.edit", "finance.write", struct {
		ID    string
		Value SeriesEdit
	}{id, v}
	return s.access.RunSpaceCommand(ctx, c, s.seriesCommandAuthorization(c.SpaceID, id), func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		if _, err := q.LockRecurringSeries(ctx, financedb.LockRecurringSeriesParams{SpaceID: c.SpaceID, ID: id}); err != nil {
			return "", absent(err)
		}
		old, err := readSeries(ctx, tx, c.SpaceID, id, m.ID)
		if err != nil {
			return "", err
		}
		if old.Version != v.Version {
			return "", execution.ErrVersion
		}
		if old.State == "ended" || old.State == "cancelled" || v.EffectiveFrom < old.GeneratedUntil {
			return "", access.ErrInvalid
		}
		n, err := q.AdvanceSeriesVersion(ctx, financedb.AdvanceSeriesVersionParams{SpaceID: c.SpaceID, ID: id, CurrentVersion: v.Version})
		if err != nil {
			return "", err
		}
		if err := execution.CheckVersion(n, v.Version); err != nil {
			return "", err
		}
		if err := insertSeriesVersion(ctx, tx, c.SpaceID, id, actor.ID(), v.Version+1, v.SeriesValue, v.EffectiveFrom, v.State, v.Reason); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

func (s *Service) seriesCommandAuthorization(space, id string) func(context.Context, pgx.Tx, access.Actor, accessdb.Membership, string) error {
	return func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership, ref string) error {
		if m.Role == "reader" {
			return access.ErrForbidden
		}
		row, err := readSeries(ctx, tx, space, id, m.ID)
		if err != nil || ref != "" {
			return err
		}
		return s.authorizePlanningWrite(ctx, tx, actor, m, space, row.AccountID, row.ResourceID)
	}
}

type SeriesGenerate struct {
	FinancialVersionConfirmation
	Until string `json:"until" format:"date" doc:"Exclusive horizon, at most 366 civil days beyond generated_until."`
}

func (s *Service) GenerateSeries(ctx context.Context, c access.SpaceCommand, id string, v SeriesGenerate) (string, error) {
	if _, err := calendar.Parse(v.Until); err != nil || v.Version < 1 || !v.Confirm {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "series.generate", "finance.write", struct {
		ID    string
		Value SeriesGenerate
	}{id, v}
	return s.access.RunSpaceCommand(ctx, c, s.seriesCommandAuthorization(c.SpaceID, id), func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		if _, err := q.LockRecurringSeries(ctx, financedb.LockRecurringSeriesParams{SpaceID: c.SpaceID, ID: id}); err != nil {
			return "", absent(err)
		}
		series, err := readSeries(ctx, tx, c.SpaceID, id, m.ID)
		if err != nil {
			return "", err
		}
		if series.Version != v.Version {
			return "", execution.ErrVersion
		}
		if series.State != "active" {
			return "", access.ErrInvalid
		}
		if v.Until <= series.GeneratedUntil {
			return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
		}
		occurrences, err := series.Recurrence.Window(series.GeneratedUntil, v.Until)
		if err != nil {
			return "", access.ErrInvalid
		}
		amount, maximum, _ := series.Amount.parse()
		ids := make([]string, 0, len(occurrences))
		dates := make([]pgtype.Date, 0, len(occurrences))
		ordinals, slots := make([]int32, 0, len(occurrences)), make([]int32, 0, len(occurrences))
		for _, occurrence := range occurrences {
			ordinal, slot := occurrence.Ordinal, occurrence.Slot
			if ordinal < 0 || ordinal > math.MaxInt32 || slot < 0 || slot > 1 {
				return "", access.ErrInvalid
			}
			ids = append(ids, uuid.New().String())
			dates = append(dates, seriesDate(occurrence.Date))
			ordinals, slots = append(ordinals, int32(ordinal)), append(slots, int32(slot))
		}
		if err := q.InsertSeriesDues(ctx, financedb.InsertSeriesDuesParams{Ids: ids, SpaceID: c.SpaceID, AccountID: nullable(series.AccountID), ResourceID: nullable(series.ResourceID), Currency: series.Currency, Scale: series.Scale, Direction: series.Direction, Name: series.Name, Dates: dates, Certainty: series.Amount.Certainty, AmountMinor: amount, MaximumMinor: maximum, Author: actor.ID()}); err != nil {
			return "", err
		}
		if err := q.InsertRecurringOccurrences(ctx, financedb.InsertRecurringOccurrencesParams{SpaceID: c.SpaceID, SeriesID: id, SeriesVersion: series.Version, Ordinals: ordinals, Slots: slots, Ids: ids}); err != nil {
			return "", err
		}
		if err := q.CaptureSeriesDues(ctx, financedb.CaptureSeriesDuesParams{SpaceID: c.SpaceID, Author: actor.ID(), Ids: ids}); err != nil {
			return "", err
		}
		if err := s.generateReminders(ctx, tx, actor, c.SpaceID, id, ids, dates); err != nil {
			return "", err
		}
		if err := q.AdvanceSeriesHorizon(ctx, financedb.AdvanceSeriesHorizonParams{SpaceID: c.SpaceID, SeriesID: id, Version: series.Version, GeneratedUntil: seriesDate(v.Until)}); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

type SeriesPreview struct {
	Recurrence calendar.Recurrence `json:"recurrence"`
	From       string              `json:"from" format:"date"`
	Until      string              `json:"until" format:"date"`
}

func (s *Service) PreviewSeries(ctx context.Context, authorization, space string, v SeriesPreview) ([]calendar.Occurrence, error) {
	out, err := v.Recurrence.Window(v.From, v.Until)
	if err != nil {
		return nil, access.ErrInvalid
	}
	err = s.access.ReadSpace(ctx, authorization, space, func(context.Context, pgx.Tx, access.Actor, accessdb.Membership) error { return nil })
	return out, err
}

func (s *Service) SeriesHistory(ctx context.Context, authorization, space, id, cursor string, limit int32) (access.Page[SeriesVersionView], error) {
	out := access.Page[SeriesVersionView]{Items: []SeriesVersionView{}}
	after := int64(0)
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 1 || strconv.FormatInt(after, 10) != cursor {
			return out, access.ErrInvalid
		}
	}
	if limit < 1 || limit > 100 {
		return out, access.ErrInvalid
	}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		if _, err := readSeries(ctx, tx, space, id, m.ID); err != nil {
			return err
		}
		rows, err := financedb.New(tx).SeriesVersions(ctx, financedb.SeriesVersionsParams{SpaceID: space, SeriesID: id, Version: after, Limit: limit + 1})
		if err != nil {
			return err
		}
		out.Items, out.NextCursor = []SeriesVersionView{}, ""
		if len(rows) > int(limit) {
			rows = rows[:limit]
			out.NextCursor = strconv.FormatInt(rows[len(rows)-1].Version, 10)
		}
		for _, row := range rows {
			out.Items = append(out.Items, seriesVersionView(row))
		}
		return nil
	})
	return out, err
}
