package finance

import (
	"context"

	"github.com/stephanofer/facets-server/internal/access"
)

func (r *routes) reminders() {
	base := "/v1/spaces/{space_id}"
	r.register("GET", base+"/dues/{due_id}/reminder", "get-due-reminder", "Read your personal durable in-app reminder without changing the due date", func(ctx context.Context, in *DuePathInput) (DueReminderView, error) {
		return r.service.DueReminder(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/dues/{due_id}/reminder", "configure-due-reminder", "Configure your own reminder with a civil time and explicit zone; no third-party recipients", func(ctx context.Context, in *struct {
		DuePathInput
		access.ActionKeyInput
		Body DueReminderEdit
	}) (reference, error) {
		return ref(r.service.SetDueReminder(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("POST", base+"/dues/{due_id}/reminder/snooze", "snooze-due-reminder", "Postpone your reminder without renegotiating or paying the due", func(ctx context.Context, in *struct {
		DuePathInput
		access.ActionKeyInput
		Body ReminderSnooze
	}) (reference, error) {
		return ref(r.service.SnoozeReminder(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
	r.register("GET", base+"/series/{series_id}/reminder", "get-series-reminder", "Read your personal reminder default for future generated occurrences", func(ctx context.Context, in *SeriesPathInput) (ReminderPreferenceView, error) {
		return r.service.SeriesReminder(ctx, in.Authorization, in.SpaceID, in.ID)
	})
	r.register("PUT", base+"/series/{series_id}/reminder", "configure-series-reminder", "Configure your personal reminders for future generation; existing reminders are independently editable", func(ctx context.Context, in *struct {
		SeriesPathInput
		access.ActionKeyInput
		Body ReminderPreferenceEdit
	}) (reference, error) {
		return ref(r.service.SetSeriesReminder(ctx, access.SpaceCommand{Authorization: in.Authorization, SpaceID: in.SpaceID, Key: in.Key}, in.ID, in.Body))
	})
}
