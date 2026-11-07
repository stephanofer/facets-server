-- +goose Up
CREATE TABLE series_reminder_preferences (
 space_id text NOT NULL,
 series_id text NOT NULL,
 member_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 days_before integer NOT NULL CHECK(days_before BETWEEN 0 AND 366),
 hour integer NOT NULL CHECK(hour BETWEEN 0 AND 23),
 minute integer NOT NULL CHECK(minute BETWEEN 0 AND 59),
 zone text NOT NULL CHECK(length(zone) BETWEEN 1 AND 100),
 PRIMARY KEY(space_id,series_id,member_id),
 FOREIGN KEY(space_id,series_id) REFERENCES recurring_series(space_id,id),
 FOREIGN KEY(space_id,member_id) REFERENCES memberships(space_id,id)
);
CREATE TABLE due_reminders (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 due_id text NOT NULL,
 member_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 enabled boolean NOT NULL,
 due_date date NOT NULL,
 days_before integer NOT NULL CHECK(days_before BETWEEN 0 AND 366),
 hour integer NOT NULL CHECK(hour BETWEEN 0 AND 23),
 minute integer NOT NULL CHECK(minute BETWEEN 0 AND 59),
 zone text NOT NULL CHECK(length(zone) BETWEEN 1 AND 100),
 scheduled_at timestamptz NOT NULL,
 UNIQUE(space_id,id),
 UNIQUE(space_id,due_id,member_id),
 FOREIGN KEY(space_id,due_id) REFERENCES due_items(space_id,id),
 FOREIGN KEY(space_id,member_id) REFERENCES memberships(space_id,id)
);
CREATE INDEX due_reminders_member ON due_reminders(space_id,member_id,due_id);
CREATE TABLE reminder_configuration_history (
 space_id text NOT NULL,
 reminder_id text NOT NULL,
 version bigint NOT NULL,
 enabled boolean NOT NULL,
 due_date date NOT NULL,
 days_before integer NOT NULL,
 hour integer NOT NULL,
 minute integer NOT NULL,
 zone text NOT NULL,
 scheduled_at timestamptz NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,reminder_id,version),
 FOREIGN KEY(space_id,reminder_id) REFERENCES due_reminders(space_id,id)
);
CREATE TABLE reminder_deliveries (
 space_id text NOT NULL,
 reminder_id text NOT NULL,
 version bigint NOT NULL,
 delivered_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,reminder_id,version),
 FOREIGN KEY(space_id,reminder_id,version) REFERENCES reminder_configuration_history(space_id,reminder_id,version)
);
CREATE TABLE series_reminder_history (
 space_id text NOT NULL,
 series_id text NOT NULL,
 member_id text NOT NULL,
 version bigint NOT NULL,
 enabled boolean NOT NULL,
 days_before integer NOT NULL,
 hour integer NOT NULL,
 minute integer NOT NULL,
 zone text NOT NULL,
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,series_id,member_id,version),
 FOREIGN KEY(space_id,series_id,member_id) REFERENCES series_reminder_preferences(space_id,series_id,member_id)
);
-- +goose Down
DROP TABLE series_reminder_history;
DROP TABLE reminder_deliveries;
DROP TABLE reminder_configuration_history;
DROP TABLE due_reminders;
DROP TABLE series_reminder_preferences;
