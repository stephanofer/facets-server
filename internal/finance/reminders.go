package finance

import (
	"context"
	"errors"
	"math"
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
)

type ReminderTiming struct {
	DaysBefore int32  `json:"days_before" minimum:"0" maximum:"366"`
	Hour       int32  `json:"hour" minimum:"0" maximum:"23"`
	Minute     int32  `json:"minute" minimum:"0" maximum:"59"`
	Zone       string `json:"zone" minLength:"1" maxLength:"100"`
}

func (v ReminderTiming) location() (calendar.Zone, error) {
	if v.DaysBefore < 0 || v.DaysBefore > 366 || v.Hour < 0 || v.Hour > 23 || v.Minute < 0 || v.Minute > 59 || len(v.Zone) > 100 {
		return calendar.Zone{}, access.ErrInvalid
	}
	zone, err := calendar.ParseZone(v.Zone)
	if err != nil {
		return calendar.Zone{}, access.ErrInvalid
	}
	return zone, nil
}

func (v ReminderTiming) at(date string) (time.Time, error) {
	zone, err := v.location()
	if err != nil {
		return time.Time{}, err
	}
	day, err := calendar.Parse(date)
	if err != nil {
		return time.Time{}, access.ErrInvalid
	}
	day, err = day.AddDays(-int(v.DaysBefore))
	if err != nil {
		return time.Time{}, access.ErrInvalid
	}
	at, err := zone.At(day, int(v.Hour), int(v.Minute))
	if err != nil {
		return time.Time{}, access.ErrInvalid
	}
	return at.UTC(), nil
}

type ReminderPreferenceEdit struct {
	ReminderTiming
	Version int64  `json:"version" minimum:"0" doc:"Zero creates a preference; subsequent edits require the current version."`
	Enabled bool   `json:"enabled"`
	Confirm bool   `json:"confirm"`
	Reason  string `json:"reason" minLength:"1" maxLength:"500"`
}

func (v ReminderPreferenceEdit) validate() error {
	if v.Version < 0 || v.Version == math.MaxInt64 || !v.Confirm || !text(v.Reason, 500, false) {
		return access.ErrInvalid
	}
	_, err := v.location()
	return err
}

type DueReminderEdit struct {
	ReminderPreferenceEdit
	DueVersion int64 `json:"due_version" minimum:"1"`
}

type ReminderPreferenceView struct {
	Timing     *ReminderTiming `json:"timing,omitempty"`
	Configured bool            `json:"configured"`
	Enabled    bool            `json:"enabled"`
	Version    int64           `json:"version"`
}

type DueReminderView struct {
	ReminderPreferenceView
	ID          string     `json:"id,omitempty"`
	ScheduledAt time.Time  `json:"scheduled_at,omitzero"`
	DeliveredAt *time.Time `json:"delivered_at" nullable:"true"`
	Stale       bool       `json:"stale" doc:"The due date changed; review and explicitly reconfigure this reminder."`
	Channel     string     `json:"channel" enum:"in_app"`
}

type AgendaReminder struct {
	ID          string     `json:"id"`
	Version     int64      `json:"version"`
	Enabled     bool       `json:"enabled"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	DeliveredAt *time.Time `json:"delivered_at" nullable:"true"`
	Stale       bool       `json:"stale"`
}

func reminderView(row financedb.DueReminderRow, date string) DueReminderView {
	r := row.DueReminder
	v := DueReminderView{Configured: true, Enabled: r.Enabled, Version: r.Version, ID: r.ID, ScheduledAt: r.ScheduledAt.Time, Stale: optionalSeriesDate(r.DueDate) != date, Channel: "in_app", Timing: &ReminderTiming{DaysBefore: r.DaysBefore, Hour: r.Hour, Minute: r.Minute, Zone: r.Zone}}
	if row.DeliveredAt.Valid {
		v.DeliveredAt = new(row.DeliveredAt.Time)
	}
	return v
}

func (s *Service) DueReminder(ctx context.Context, authorization, space, due string) (DueReminderView, error) {
	out := DueReminderView{Channel: "in_app"}
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		d, err := readDue(ctx, tx, space, due, m.ID)
		if err != nil {
			return err
		}
		r, err := financedb.New(tx).DueReminder(ctx, financedb.DueReminderParams{SpaceID: space, DueID: due, MemberID: m.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = reminderView(r, optionalSeriesDate(d.DueItem.DueDate))
		return nil
	})
	return out, err
}

func reminderKey(id string, version int64) string {
	return "planning.reminder:" + id + ":" + strconv.FormatInt(version, 10)
}
func reminderJob(r financedb.DueReminder) execution.Job {
	return execution.Job{Queue: "notifications", Kind: "planning.reminder", DedupKey: reminderKey(r.ID, r.Version), Payload: []byte(r.SpaceID + ":" + r.ID + ":" + strconv.FormatInt(r.Version, 10)), MaxAttempts: 5, AvailableAt: r.ScheduledAt.Time}
}

func (s *Service) putReminder(ctx context.Context, tx pgx.Tx, actor access.Actor, r financedb.DueReminder, reason string) error {
	q := financedb.New(tx)
	if r.Version > 1 {
		if err := s.jobs.CancelPending(ctx, tx, reminderKey(r.ID, r.Version-1)); err != nil {
			return err
		}
	}
	if err := q.PutDueReminder(ctx, financedb.PutDueReminderParams(r)); err != nil {
		return err
	}
	if err := q.CaptureReminderConfiguration(ctx, financedb.CaptureReminderConfigurationParams{SpaceID: r.SpaceID, ID: r.ID, Author: actor.ID(), Reason: reason}); err != nil {
		return err
	}
	if r.Enabled {
		_, err := s.jobs.Enqueue(ctx, tx, reminderJob(r))
		return err
	}
	return nil
}

func reminderAuthorization(space, due string) func(context.Context, pgx.Tx, access.Actor, accessdb.Membership, string) error {
	return func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership, ref string) error {
		if _, err := readDue(ctx, tx, space, due, m.ID); err != nil {
			return err
		}
		if ref == "" {
			return nil
		}
		r, err := financedb.New(tx).ReminderByID(ctx, financedb.ReminderByIDParams{SpaceID: space, ID: ref})
		if err != nil {
			return absent(err)
		}
		if r.MemberID != m.ID || r.DueID != due {
			return access.ErrNotFound
		}
		return nil
	}
}

func (s *Service) SetDueReminder(ctx context.Context, c access.SpaceCommand, due string, v DueReminderEdit) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	if v.DueVersion < 1 {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "reminder.configure", "read", struct {
		Due   string
		Value DueReminderEdit
	}{due, v}
	return s.access.RunSpaceCommand(ctx, c, reminderAuthorization(c.SpaceID, due), func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		d, err := readDue(ctx, tx, c.SpaceID, due, m.ID)
		if err != nil {
			return "", err
		}
		if d.DueItem.Version != v.DueVersion {
			return "", execution.ErrVersion
		}
		old, err := financedb.New(tx).DueReminder(ctx, financedb.DueReminderParams{SpaceID: c.SpaceID, DueID: due, MemberID: m.ID})
		version := int64(0)
		id := uuid.New().String()
		if err == nil {
			version, id = old.DueReminder.Version, old.DueReminder.ID
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if version != v.Version {
			return "", execution.ErrVersion
		}
		at, err := v.at(optionalSeriesDate(d.DueItem.DueDate))
		if err != nil {
			return "", err
		}
		r := financedb.DueReminder{ID: id, SpaceID: c.SpaceID, DueID: due, MemberID: m.ID, Version: version + 1, Enabled: v.Enabled, DueDate: d.DueItem.DueDate, DaysBefore: v.DaysBefore, Hour: v.Hour, Minute: v.Minute, Zone: v.Zone, ScheduledAt: pgtype.Timestamptz{Time: at, Valid: true}}
		if err := s.putReminder(ctx, tx, actor, r, v.Reason); err != nil {
			return "", err
		}
		return id, audit(ctx, tx, actor, c.SpaceID, c.Kind, id)
	})
}

type ReminderSnooze struct {
	FinancialVersionConfirmation
	Until  time.Time `json:"until"`
	Reason string    `json:"reason" minLength:"1" maxLength:"500"`
}

func (s *Service) SnoozeReminder(ctx context.Context, c access.SpaceCommand, due string, v ReminderSnooze) (string, error) {
	if v.Version < 1 || v.Version == math.MaxInt64 || !v.Confirm || !text(v.Reason, 500, false) || v.Until.IsZero() {
		return "", access.ErrInvalid
	}
	c.Kind, c.Capability, c.Content = "reminder.snooze", "read", struct {
		Due   string
		Value ReminderSnooze
	}{due, v}
	return s.access.RunSpaceCommand(ctx, c, reminderAuthorization(c.SpaceID, due), func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		d, err := readDue(ctx, tx, c.SpaceID, due, m.ID)
		if err != nil {
			return "", err
		}
		old, err := financedb.New(tx).DueReminder(ctx, financedb.DueReminderParams{SpaceID: c.SpaceID, DueID: due, MemberID: m.ID})
		if err != nil {
			return "", absent(err)
		}
		r := old.DueReminder
		if r.Version != v.Version {
			return "", execution.ErrVersion
		}
		if !v.Until.After(time.Now()) || v.Until.After(time.Now().AddDate(0, 0, 366)) {
			return "", access.ErrInvalid
		}
		if !r.Enabled || optionalSeriesDate(r.DueDate) != optionalSeriesDate(d.DueItem.DueDate) {
			return "", access.ErrInvalid
		}
		r.Version++
		r.ScheduledAt = pgtype.Timestamptz{Time: v.Until.UTC(), Valid: true}
		if err := s.putReminder(ctx, tx, actor, r, v.Reason); err != nil {
			return "", err
		}
		return r.ID, audit(ctx, tx, actor, c.SpaceID, c.Kind, r.ID)
	})
}

func (s *Service) SeriesReminder(ctx context.Context, authorization, space, series string) (ReminderPreferenceView, error) {
	var out ReminderPreferenceView
	err := s.access.ReadSpace(ctx, authorization, space, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership) error {
		if _, err := readSeries(ctx, tx, space, series, m.ID); err != nil {
			return err
		}
		r, err := financedb.New(tx).SeriesReminderPreference(ctx, financedb.SeriesReminderPreferenceParams{SpaceID: space, SeriesID: series, MemberID: m.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		out = ReminderPreferenceView{Configured: true, Enabled: r.Enabled, Version: r.Version, Timing: &ReminderTiming{DaysBefore: r.DaysBefore, Hour: r.Hour, Minute: r.Minute, Zone: r.Zone}}
		return nil
	})
	return out, err
}

func (s *Service) SetSeriesReminder(ctx context.Context, c access.SpaceCommand, series string, v ReminderPreferenceEdit) (string, error) {
	if err := v.validate(); err != nil {
		return "", err
	}
	c.Kind, c.Capability, c.Content = "series.reminder", "read", struct {
		Series string
		Value  ReminderPreferenceEdit
	}{series, v}
	// The preference identity includes the membership episode. Persist that
	// episode in the receipt, while returning the series as the public reference.
	_, err := s.access.RunSpaceCommand(ctx, c, func(ctx context.Context, tx pgx.Tx, _ access.Actor, m accessdb.Membership, ref string) error {
		if _, err := readSeries(ctx, tx, c.SpaceID, series, m.ID); err != nil {
			return err
		}
		if ref != "" && ref != m.ID {
			return access.ErrNotFound
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx, actor access.Actor, m accessdb.Membership) (string, error) {
		q := financedb.New(tx)
		old, err := q.SeriesReminderPreference(ctx, financedb.SeriesReminderPreferenceParams{SpaceID: c.SpaceID, SeriesID: series, MemberID: m.ID})
		version := int64(0)
		if err == nil {
			version = old.Version
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if version != v.Version {
			return "", execution.ErrVersion
		}
		if v.Enabled {
			prefs, err := q.SeriesReminderPreferences(ctx, financedb.SeriesReminderPreferencesParams{SpaceID: c.SpaceID, SeriesID: series})
			if err != nil {
				return "", err
			}
			existing := false
			for _, p := range prefs {
				if p.MemberID == m.ID {
					existing = true
				}
			}
			if len(prefs) >= 10 && !existing {
				return "", access.ErrCapacity
			}
		}
		if err := q.PutSeriesReminderPreference(ctx, financedb.PutSeriesReminderPreferenceParams{SpaceID: c.SpaceID, SeriesID: series, MemberID: m.ID, Version: version + 1, Enabled: v.Enabled, DaysBefore: v.DaysBefore, Hour: v.Hour, Minute: v.Minute, Zone: v.Zone}); err != nil {
			return "", err
		}
		if err := q.CaptureSeriesReminder(ctx, financedb.CaptureSeriesReminderParams{SpaceID: c.SpaceID, SeriesID: series, MemberID: m.ID, Reason: v.Reason}); err != nil {
			return "", err
		}
		return m.ID, audit(ctx, tx, actor, c.SpaceID, c.Kind, series)
	})
	if err != nil {
		return "", err
	}
	return series, nil
}

func (s *Service) generateReminders(ctx context.Context, tx pgx.Tx, actor access.Actor, space, series string, ids []string, dates []pgtype.Date) error {
	q := financedb.New(tx)
	prefs, err := q.SeriesReminderPreferences(ctx, financedb.SeriesReminderPreferencesParams{SpaceID: space, SeriesID: series})
	if err != nil {
		return err
	}
	if len(prefs) > 10 {
		return access.ErrCapacity
	}
	insert := financedb.InsertGeneratedRemindersParams{SpaceID: space}
	jobs := []execution.Job{}
	now := time.Now()
	for _, p := range prefs {
		if _, err := s.access.AuthorizeNotificationRecipient(ctx, tx, space, p.MemberID); err != nil {
			if obsoleteReminder(err) {
				continue
			}
			return err
		}
		if _, err := readSeries(ctx, tx, space, series, p.MemberID); err != nil {
			if obsoleteReminder(err) {
				continue
			}
			return err
		}
		timing := ReminderTiming{DaysBefore: p.DaysBefore, Hour: p.Hour, Minute: p.Minute, Zone: p.Zone}
		for i, due := range ids {
			at, err := timing.at(optionalSeriesDate(dates[i]))
			if err != nil {
				return err
			}
			if at.Before(now) {
				continue
			}
			r := financedb.DueReminder{ID: uuid.New().String(), SpaceID: space, DueID: due, MemberID: p.MemberID, Version: 1, Enabled: true, DueDate: dates[i], DaysBefore: p.DaysBefore, Hour: p.Hour, Minute: p.Minute, Zone: p.Zone, ScheduledAt: pgtype.Timestamptz{Time: at, Valid: true}}
			insert.Ids = append(insert.Ids, r.ID)
			insert.DueIds = append(insert.DueIds, due)
			insert.Members = append(insert.Members, p.MemberID)
			insert.Dates = append(insert.Dates, dates[i])
			insert.Days = append(insert.Days, p.DaysBefore)
			insert.Hours = append(insert.Hours, p.Hour)
			insert.Minutes = append(insert.Minutes, p.Minute)
			insert.Zones = append(insert.Zones, p.Zone)
			insert.Scheduled = append(insert.Scheduled, r.ScheduledAt)
			jobs = append(jobs, reminderJob(r))
		}
	}
	if len(jobs) == 0 {
		return nil
	}
	if err := q.InsertGeneratedReminders(ctx, insert); err != nil {
		return err
	}
	if err := q.CaptureGeneratedReminders(ctx, financedb.CaptureGeneratedRemindersParams{SpaceID: space, Ids: insert.Ids, Author: actor.ID()}); err != nil {
		return err
	}
	return s.jobs.EnqueueBatch(ctx, tx, jobs)
}

func obsoleteReminder(err error) bool {
	return errors.Is(err, access.ErrNotFound) || errors.Is(err, access.ErrForbidden) || errors.Is(err, access.ErrCapacity) || errors.Is(err, pgx.ErrNoRows)
}

func (s *Service) reminderDelivery(ctx context.Context, payload []byte, deliver bool) (string, error) {
	parts := strings.Split(string(payload), ":")
	if len(parts) != 3 {
		return "", execution.ErrPermanent
	}
	for _, id := range parts[:2] {
		if _, err := uuid.Parse(id); err != nil {
			return "", execution.ErrPermanent
		}
	}
	version, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || version < 1 {
		return "", execution.ErrPermanent
	}
	var result string
	err = s.jobs.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := financedb.New(tx)
		initial, err := q.ReminderByID(ctx, financedb.ReminderByIDParams{SpaceID: parts[0], ID: parts[1]})
		if err != nil {
			return err
		}
		if _, err := s.access.AuthorizeNotificationRecipient(ctx, tx, initial.SpaceID, initial.MemberID); err != nil {
			return err
		}
		d, err := readDue(ctx, tx, initial.SpaceID, initial.DueID, initial.MemberID)
		if err != nil {
			return err
		}
		current, err := q.DueReminder(ctx, financedb.DueReminderParams{SpaceID: initial.SpaceID, DueID: initial.DueID, MemberID: initial.MemberID})
		if err != nil {
			return err
		}
		r := current.DueReminder
		if r.ID != initial.ID || r.Version != version || !r.Enabled || r.ScheduledAt.Time.After(time.Now()) || optionalSeriesDate(r.DueDate) != optionalSeriesDate(d.DueItem.DueDate) {
			return execution.ErrObsolete
		}
		today, err := dueToday(ctx, tx, r.SpaceID)
		if err != nil {
			return err
		}
		view, err := dueView(d, today)
		if err != nil {
			return err
		}
		if view.Status == "paid" || view.Status == "skipped" || view.Status == "cancelled" {
			return execution.ErrObsolete
		}
		result = r.ID
		if !deliver {
			return nil
		}
		n, err := q.DeliverReminder(ctx, financedb.DeliverReminderParams{SpaceID: r.SpaceID, ReminderID: r.ID, Version: r.Version})
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
		return execution.Audit(ctx, tx, "worker", r.SpaceID, "reminder.delivered", r.ID, "")
	})
	if obsoleteReminder(err) {
		return "", execution.ErrObsolete
	}
	return result, err
}

// ReminderHandler delivers to a durable, authorized in-app inbox (the agenda).
// There is no external effect: recovery can replay this transaction safely.
func (s *Service) ReminderHandler() execution.Handler {
	return execution.Handler{Check: func(ctx context.Context, payload []byte) error {
		_, err := s.reminderDelivery(ctx, payload, false)
		return err
	}, Execute: func(ctx context.Context, _ string, payload []byte) (string, error) {
		return s.reminderDelivery(ctx, payload, true)
	}}
}
