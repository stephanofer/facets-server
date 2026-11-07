-- +goose Up
CREATE TABLE recurring_series (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 account_id text NOT NULL,
 direction text NOT NULL CHECK(direction IN ('payment','receipt')),
 current_version bigint NOT NULL DEFAULT 1 CHECK(current_version>0),
 author text NOT NULL REFERENCES principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id)
);
CREATE INDEX recurring_series_account ON recurring_series(space_id,account_id,id);
CREATE TABLE recurring_series_versions (
 space_id text NOT NULL,
 series_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 certainty text NOT NULL CHECK(certainty IN ('confirmed','estimated','range','unknown')),
 amount_minor bigint CHECK(amount_minor>0),
 maximum_minor bigint CHECK(maximum_minor>0),
 start_date date NOT NULL,
 frequency text NOT NULL CHECK(frequency IN ('weekly','biweekly','monthly','twice_monthly','yearly','every_weeks','every_months')),
 interval_count integer NOT NULL CHECK(interval_count BETWEEN 0 AND 1200),
 first_day integer NOT NULL CHECK(first_day BETWEEN 0 AND 31),
 second_day integer NOT NULL CHECK(second_day BETWEEN 0 AND 31),
 end_date date CHECK(end_date>=start_date),
 repetition_count integer NOT NULL CHECK(repetition_count BETWEEN 0 AND 100000),
 effective_from date NOT NULL CHECK(effective_from>=start_date),
 generated_until date NOT NULL CHECK(generated_until>=effective_from),
 state text NOT NULL CHECK(state IN ('active','paused','ended','cancelled')),
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,series_id,version),
 FOREIGN KEY(space_id,series_id) REFERENCES recurring_series(space_id,id),
 CHECK((certainty='unknown' AND amount_minor IS NULL AND maximum_minor IS NULL)
  OR (certainty IN ('confirmed','estimated') AND amount_minor IS NOT NULL AND maximum_minor IS NULL)
  OR (certainty='range' AND amount_minor IS NOT NULL AND maximum_minor IS NOT NULL AND maximum_minor>=amount_minor)),
 CHECK((frequency='twice_monthly' AND interval_count=0 AND first_day>=1 AND second_day>first_day)
  OR (frequency<>'twice_monthly' AND first_day=0 AND second_day=0
   AND ((frequency IN ('every_weeks','every_months') AND interval_count>=1)
    OR (frequency IN ('weekly','biweekly','monthly','yearly') AND interval_count=0))))
);
CREATE TABLE recurring_occurrences (
 space_id text NOT NULL,
 series_id text NOT NULL,
 series_version bigint NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal>=0),
 slot integer NOT NULL CHECK(slot BETWEEN 0 AND 1),
 due_id text NOT NULL,
 PRIMARY KEY(space_id,series_id,series_version,ordinal),
 UNIQUE(space_id,due_id),
 FOREIGN KEY(space_id,series_id,series_version) REFERENCES recurring_series_versions(space_id,series_id,version),
 FOREIGN KEY(space_id,due_id) REFERENCES due_items(space_id,id)
);
-- +goose Down
DROP TABLE recurring_occurrences;
DROP TABLE recurring_series_versions;
DROP TABLE recurring_series;
