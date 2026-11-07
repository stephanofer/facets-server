-- name: DueReminder :one
SELECT sqlc.embed(r),d.delivered_at FROM due_reminders r LEFT JOIN reminder_deliveries d ON (d.space_id,d.reminder_id,d.version)=(r.space_id,r.id,r.version)
WHERE r.space_id=$1 AND r.due_id=$2 AND r.member_id=$3 FOR UPDATE OF r;

-- name: ReminderByID :one
SELECT * FROM due_reminders WHERE space_id=$1 AND id=$2;

-- name: PutDueReminder :exec
INSERT INTO due_reminders(id,space_id,due_id,member_id,version,enabled,due_date,days_before,hour,minute,zone,scheduled_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT(space_id,due_id,member_id) DO UPDATE SET version=EXCLUDED.version,enabled=EXCLUDED.enabled,
 due_date=EXCLUDED.due_date,days_before=EXCLUDED.days_before,hour=EXCLUDED.hour,minute=EXCLUDED.minute,zone=EXCLUDED.zone,scheduled_at=EXCLUDED.scheduled_at;

-- name: CaptureReminderConfiguration :exec
INSERT INTO reminder_configuration_history(space_id,reminder_id,version,enabled,due_date,days_before,hour,minute,zone,scheduled_at,author,reason)
SELECT r.space_id,r.id,r.version,r.enabled,r.due_date,r.days_before,r.hour,r.minute,r.zone,r.scheduled_at,sqlc.arg(author),sqlc.arg(reason)
FROM due_reminders r WHERE r.space_id=sqlc.arg(space_id) AND r.id=sqlc.arg(id);

-- name: DeliverReminder :execrows
INSERT INTO reminder_deliveries(space_id,reminder_id,version) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING;

-- name: SeriesReminderPreference :one
SELECT * FROM series_reminder_preferences WHERE space_id=$1 AND series_id=$2 AND member_id=$3;

-- name: SeriesReminderPreferences :many
SELECT r.* FROM series_reminder_preferences r JOIN memberships m ON (m.space_id,m.id)=(r.space_id,r.member_id)
WHERE r.space_id=$1 AND r.series_id=$2 AND r.enabled AND m.ended_at IS NULL ORDER BY r.member_id LIMIT 11;

-- name: PutSeriesReminderPreference :exec
INSERT INTO series_reminder_preferences(space_id,series_id,member_id,version,enabled,days_before,hour,minute,zone)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT(space_id,series_id,member_id) DO UPDATE SET version=EXCLUDED.version,enabled=EXCLUDED.enabled,days_before=EXCLUDED.days_before,hour=EXCLUDED.hour,minute=EXCLUDED.minute,zone=EXCLUDED.zone;

-- name: CaptureSeriesReminder :exec
INSERT INTO series_reminder_history(space_id,series_id,member_id,version,enabled,days_before,hour,minute,zone,reason)
SELECT r.space_id,r.series_id,r.member_id,r.version,r.enabled,r.days_before,r.hour,r.minute,r.zone,sqlc.arg(reason)
FROM series_reminder_preferences r WHERE r.space_id=sqlc.arg(space_id) AND r.series_id=sqlc.arg(series_id) AND r.member_id=sqlc.arg(member_id);

-- name: InsertGeneratedReminders :exec
INSERT INTO due_reminders(id,space_id,due_id,member_id,version,enabled,due_date,days_before,hour,minute,zone,scheduled_at)
SELECT unnest(sqlc.arg(ids)::text[]),sqlc.arg(space_id)::text,unnest(sqlc.arg(due_ids)::text[]),unnest(sqlc.arg(members)::text[]),1,true,
 unnest(sqlc.arg(dates)::date[]),unnest(sqlc.arg(days)::integer[]),unnest(sqlc.arg(hours)::integer[]),unnest(sqlc.arg(minutes)::integer[]),unnest(sqlc.arg(zones)::text[]),unnest(sqlc.arg(scheduled)::timestamptz[]);

-- name: CaptureGeneratedReminders :exec
INSERT INTO reminder_configuration_history(space_id,reminder_id,version,enabled,due_date,days_before,hour,minute,zone,scheduled_at,author,reason)
SELECT r.space_id,r.id,r.version,r.enabled,r.due_date,r.days_before,r.hour,r.minute,r.zone,r.scheduled_at,sqlc.arg(author),'Generated from explicit personal series preference'
FROM due_reminders r WHERE r.space_id=sqlc.arg(space_id) AND r.id=ANY(sqlc.arg(ids)::text[]);
