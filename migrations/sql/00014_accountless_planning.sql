-- +goose Up
CREATE TABLE planning_resources (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 responsible_member text NOT NULL,
 visibility text NOT NULL DEFAULT 'private' CHECK(visibility IN ('private','space','selected')),
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,responsible_member) REFERENCES memberships(space_id,id)
);
CREATE TABLE planning_readers (
 space_id text NOT NULL,
 resource_id text NOT NULL,
 member_id text NOT NULL,
 PRIMARY KEY(space_id,resource_id,member_id),
 FOREIGN KEY(space_id,resource_id) REFERENCES planning_resources(space_id,id),
 FOREIGN KEY(space_id,member_id) REFERENCES memberships(space_id,id)
);
CREATE TABLE planning_access_history (
 space_id text NOT NULL,
 resource_id text NOT NULL,
 version bigint NOT NULL,
 responsible_member text NOT NULL,
 visibility text NOT NULL,
 readers text[] NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,resource_id,version),
 FOREIGN KEY(space_id,resource_id) REFERENCES planning_resources(space_id,id),
 FOREIGN KEY(space_id,responsible_member) REFERENCES memberships(space_id,id)
);
ALTER TABLE due_items ALTER COLUMN account_id DROP NOT NULL,
 ADD COLUMN currency text,
 ADD COLUMN scale integer,
 ADD COLUMN resource_id text,
 ADD FOREIGN KEY(space_id,resource_id) REFERENCES planning_resources(space_id,id);
UPDATE due_items d SET currency=a.currency,scale=a.scale FROM financial_accounts a WHERE (a.space_id,a.id)=(d.space_id,d.account_id);
ALTER TABLE due_items ALTER COLUMN currency SET NOT NULL, ALTER COLUMN scale SET NOT NULL,
 ADD CHECK((account_id IS NULL)=(resource_id IS NOT NULL)),
 ADD CHECK(length(currency)=3 AND scale BETWEEN 0 AND 6);
ALTER TABLE recurring_series ALTER COLUMN account_id DROP NOT NULL,
 ADD COLUMN currency text,
 ADD COLUMN scale integer,
 ADD COLUMN resource_id text,
 ADD FOREIGN KEY(space_id,resource_id) REFERENCES planning_resources(space_id,id);
UPDATE recurring_series s SET currency=a.currency,scale=a.scale FROM financial_accounts a WHERE (a.space_id,a.id)=(s.space_id,s.account_id);
ALTER TABLE recurring_series ALTER COLUMN currency SET NOT NULL, ALTER COLUMN scale SET NOT NULL,
 ADD CHECK((account_id IS NULL)=(resource_id IS NOT NULL)),
 ADD CHECK(length(currency)=3 AND scale BETWEEN 0 AND 6);
CREATE INDEX due_items_resource ON due_items(space_id,resource_id,due_date,id) WHERE resource_id IS NOT NULL;
CREATE INDEX recurring_series_resource ON recurring_series(space_id,resource_id,id) WHERE resource_id IS NOT NULL;
CREATE INDEX planning_resources_responsible ON planning_resources(space_id,responsible_member,id);

-- +goose Down
-- Accountless expectations must be resolved explicitly before downgrading.
ALTER TABLE due_items ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE recurring_series ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE due_items DROP COLUMN currency, DROP COLUMN scale, DROP COLUMN resource_id;
ALTER TABLE recurring_series DROP COLUMN currency, DROP COLUMN scale, DROP COLUMN resource_id;
DROP TABLE planning_access_history;
DROP TABLE planning_readers;
DROP TABLE planning_resources;
