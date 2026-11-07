//go:build integration

package access_test

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestRemindersDurableDeliveryAndSnooze(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "reminders@example.test")
	base := "/v1/spaces/" + u.SpaceID
	v := finance.DueCreate{Name: "Synthetic overdue bill", Date: time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly), Currency: "PEN", Direction: "payment", Confirm: true, Amount: finance.DueAmount{Certainty: "confirmed", AmountMinor: new("100")}}
	due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", v, 200).ID
	config := finance.DueReminderEdit{DueVersion: 1, Version: 0, Confirm: true, Enabled: true, Reason: "Explicit personal reminder", ReminderTiming: finance.ReminderTiming{Zone: "UTC"}}
	path := base + "/dues/" + due + "/reminder"
	id := f.request[financeReference](t, "PUT", path, u.Token, "reminder", config, 200).ID
	f.request[financeReference](t, "PUT", path, u.Token, "reminder", config, 200)
	if f.count(t, "SELECT count(*) FROM due_reminders") != 1 {
		t.Fatal("retry duplicated reminder")
	}
	before := f.request[finance.DueView](t, "GET", base+"/dues/"+due, u.Token, "", nil, 200)
	if before.Reminder == nil || before.Reminder.DeliveredAt != nil {
		t.Fatal(before)
	}
	for range 10 {
		worked, err := f.worker.Once(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	delivered := f.request[finance.DueReminderView](t, "GET", path, u.Token, "", nil, 200)
	if delivered.DeliveredAt == nil || delivered.Channel != "in_app" {
		t.Fatal(delivered)
	}
	payload := []byte(u.SpaceID + ":" + id + ":1")
	h := finance.New(f.service, f.store).ReminderHandler()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := h.Execute(t.Context(), "recovered-job", payload); errs <- err })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, "SELECT count(*) FROM reminder_deliveries") != 1 {
		t.Fatal("delivery duplicated after recovery")
	}
	inbox := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?reminder=delivered&limit=1", u.Token, "", nil, 200)
	if len(inbox.Items) != 1 || inbox.Items[0].ID != due {
		t.Fatal(inbox)
	}
	snooze := finance.ReminderSnooze{Version: 1, Confirm: true, Until: time.Now().UTC().Add(time.Hour).Truncate(time.Second), Reason: "Notify again later; do not renegotiate"}
	f.request[financeReference](t, "POST", path+"/snooze", u.Token, "snooze", snooze, 200)
	f.request[financeReference](t, "POST", path+"/snooze", u.Token, "snooze", snooze, 200)
	after := f.request[finance.DueView](t, "GET", base+"/dues/"+due, u.Token, "", nil, 200)
	if after.Date != before.Date || after.Version != before.Version || after.Status != before.Status || after.Reminder.Version != 2 || !after.Reminder.ScheduledAt.Equal(snooze.Until) {
		t.Fatal("snooze changed financial obligation", after)
	}
	if err := h.Check(t.Context(), payload); !errors.Is(err, execution.ErrObsolete) {
		t.Fatal("old delivery still executable", err)
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("reminder moved money")
	}
	// Renegotiation is explicit and invalidates, rather than silently shifting, the reminder.
	f.request[financeReference](t, "PUT", base+"/dues/"+due, u.Token, "renegotiate", finance.DueEdit{Name: v.Name, Amount: v.Amount, Date: time.Now().UTC().AddDate(0, 0, 5).Format(time.DateOnly), Version: 1, Confirm: true, State: "open", Reason: "Explicit new due date"}, 200)
	stale := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?reminder=stale", u.Token, "", nil, 200)
	if len(stale.Items) != 1 || !stale.Items[0].Reminder.Stale {
		t.Fatal(stale)
	}
}

func TestReminderRevocationAndPreferences(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "reminder-owner@example.test")
	member := f.newUser(t, "reminder-member@example.test")
	membership := f.join(t, owner, member, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	bank := f.financialAccount(t, owner, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	series := f.request[financeReference](t, "POST", base+"/series", owner.Token, "series", seriesInput(bank), 200).ID
	pref := finance.ReminderPreferenceEdit{Version: 0, Enabled: true, Confirm: true, Reason: "Personal series reminder", ReminderTiming: finance.ReminderTiming{Zone: "UTC"}}
	f.request[financeReference](t, "PUT", base+"/series/"+series+"/reminder", member.Token, "series-pref", pref, 200)
	v := dueInput(bank, "100")
	v.Date = time.Now().UTC().AddDate(0, 0, -1).Format(time.DateOnly)
	due := f.request[financeReference](t, "POST", base+"/dues", owner.Token, "due", v, 200).ID
	config := finance.DueReminderEdit{DueVersion: 1, Version: 0, Confirm: true, Enabled: true, Reason: "Reader's own preference", ReminderTiming: finance.ReminderTiming{Zone: "UTC"}}
	path := base + "/dues/" + due + "/reminder"
	id := f.request[financeReference](t, "PUT", path, member.Token, "reminder", config, 200).ID
	h := finance.New(f.service, f.store).ReminderHandler()
	payload := []byte(owner.SpaceID + ":" + id + ":1")
	if err := h.Check(t.Context(), payload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(t.Context(), "UPDATE principals SET notifications=false WHERE id=$1", member.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.Check(t.Context(), payload); !errors.Is(err, execution.ErrObsolete) {
		t.Fatal("disabled preference ignored", err)
	}
	if _, err := f.db.Pool.Exec(t.Context(), "UPDATE principals SET notifications=true WHERE id=$1", member.ID); err != nil {
		t.Fatal(err)
	}
	f.request[map[string]any](t, "POST", base+"/members/"+membership+"/remove", owner.Token, "remove", map[string]any{"confirm": true}, 200)
	if err := h.Check(t.Context(), payload); !errors.Is(err, execution.ErrObsolete) {
		t.Fatal("ended recipient notified", err)
	}
	f.join(t, owner, member, "reader")
	if err := h.Check(t.Context(), payload); !errors.Is(err, execution.ErrObsolete) {
		t.Fatal("rejoining restored recipient", err)
	}
	got := f.request[finance.DueReminderView](t, "GET", path, member.Token, "", nil, 200)
	if got.Configured {
		t.Fatal("ended reminder restored", got)
	}
	f.request[httpapi.Problem](t, "PUT", path, member.Token, "reminder", config, 404)
	f.request[httpapi.Problem](t, "PUT", base+"/series/"+series+"/reminder", member.Token, "series-pref", pref, 404)
	f.request[financeReference](t, "PUT", base+"/series/"+series+"/reminder", member.Token, "new-episode-pref", pref, 200)
	// Creating a new preference cannot make the old episode's receipt replayable.
	f.request[httpapi.Problem](t, "PUT", base+"/series/"+series+"/reminder", member.Token, "series-pref", pref, 404)
	f.request[financeReference](t, "PUT", path, owner.Token, "own-reminder", config, 200)
	if _, err := finance.New(f.service, f.store).ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "private"}, bank, finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}, 1); err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "GET", path, member.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "PUT", path, member.Token, "reminder", config, 404)
}

func TestSeriesRemindersAndAtomicRollback(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "series-reminders@example.test")
	base := "/v1/spaces/" + u.SpaceID
	start := time.Now().UTC().AddDate(0, 0, 10).Format(time.DateOnly)
	end := time.Now().UTC().AddDate(0, 0, 40).Format(time.DateOnly)
	v := finance.SeriesCreate{Name: "Personal future subscription", Currency: "PEN", Direction: "payment", Confirm: true, Amount: finance.DueAmount{Certainty: "estimated", AmountMinor: new("100")}, Recurrence: calendar.Recurrence{Start: start, Frequency: "weekly"}}
	id := f.request[financeReference](t, "POST", base+"/series", u.Token, "series", v, 200).ID
	config := finance.ReminderPreferenceEdit{Version: 0, Enabled: true, Confirm: true, Reason: "Own future occurrence reminders", ReminderTiming: finance.ReminderTiming{DaysBefore: 2, Hour: 9, Zone: "America/Lima"}}
	f.request[financeReference](t, "PUT", base+"/series/"+id+"/reminder", u.Token, "preference", config, 200)
	generate := finance.SeriesGenerate{Version: 1, Confirm: true, Until: end}
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON jobs FROM access_app"); err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", generate, 500)
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON jobs TO access_app"); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "SELECT count(*) FROM due_items") != 0 || f.count(t, "SELECT count(*) FROM due_reminders") != 0 {
		t.Fatal("outbox failure left partial generation")
	}
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", generate, 200)
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", generate, 200)
	n := f.count(t, "SELECT count(*) FROM due_items")
	if n < 4 || f.count(t, "SELECT count(*) FROM due_reminders") != n || f.count(t, "SELECT count(*) FROM jobs WHERE kind='planning.reminder'") != n {
		t.Fatal("generation/default/dedup mismatch", n)
	}
	for _, state := range []string{"pending", "succeeded"} {
		count := f.count(t, "SELECT count(*) FROM jobs WHERE kind='planning.reminder' AND state='"+state+"'")
		if state == "pending" && count != n || state == "succeeded" && count != 0 {
			t.Fatal(state, strconv.Itoa(count))
		}
	}
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?reminder=scheduled", u.Token, "", nil, 200)
	if len(page.Items) != n || page.Items[0].Reminder == nil {
		t.Fatal(page)
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("generation or reminders moved money")
	}
}
