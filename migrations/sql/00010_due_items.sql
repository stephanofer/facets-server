-- +goose Up
CREATE TABLE due_items (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 account_id text NOT NULL,
 direction text NOT NULL CHECK(direction IN ('payment','receipt')),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 due_date date NOT NULL,
 certainty text NOT NULL CHECK(certainty IN ('confirmed','estimated','range','unknown')),
 amount_minor bigint CHECK(amount_minor>0),
 maximum_minor bigint CHECK(maximum_minor>0),
 state text NOT NULL DEFAULT 'open' CHECK(state IN ('open','skipped','cancelled')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 author text NOT NULL REFERENCES principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id),
 CHECK((certainty='unknown' AND amount_minor IS NULL AND maximum_minor IS NULL)
  OR (certainty IN ('confirmed','estimated') AND amount_minor IS NOT NULL AND maximum_minor IS NULL)
  OR (certainty='range' AND amount_minor IS NOT NULL AND maximum_minor IS NOT NULL AND maximum_minor>=amount_minor))
);
CREATE INDEX due_items_agenda ON due_items(space_id,due_date,id);
CREATE INDEX due_items_account ON due_items(space_id,account_id,id);
CREATE TABLE due_item_revisions (
 space_id text NOT NULL,
 due_id text NOT NULL,
 version bigint NOT NULL,
 name text NOT NULL,
 due_date date NOT NULL,
 certainty text NOT NULL,
 amount_minor bigint,
 maximum_minor bigint,
 state text NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,due_id,version),
 FOREIGN KEY(space_id,due_id) REFERENCES due_items(space_id,id)
);
CREATE TABLE payment_applications (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 due_id text NOT NULL,
 operation_id text NOT NULL,
 source_revision bigint NOT NULL,
 effect_ordinal integer NOT NULL,
 movement_minor bigint NOT NULL CHECK(movement_minor>0),
 due_minor bigint NOT NULL CHECK(due_minor>0),
 author text NOT NULL REFERENCES principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,due_id) REFERENCES due_items(space_id,id),
 FOREIGN KEY(space_id,operation_id,source_revision,effect_ordinal) REFERENCES position_effects(space_id,operation_id,revision,ordinal)
);
CREATE INDEX payment_applications_due ON payment_applications(space_id,due_id,id);
CREATE INDEX payment_applications_operation ON payment_applications(space_id,operation_id,effect_ordinal,id);
CREATE TABLE payment_application_releases (
 space_id text NOT NULL,
 application_id text NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,application_id),
 FOREIGN KEY(space_id,application_id) REFERENCES payment_applications(space_id,id)
);
-- +goose Down
DROP TABLE payment_application_releases;
DROP TABLE payment_applications;
DROP TABLE due_item_revisions;
DROP TABLE due_items;
