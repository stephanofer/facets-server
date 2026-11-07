-- +goose Up
CREATE TABLE financial_operations (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 kind text NOT NULL CHECK(kind IN ('income','expense','transfer')),
 author text NOT NULL REFERENCES principals(id),
 origin text NOT NULL CHECK(origin IN ('manual')),
 current_revision bigint NOT NULL DEFAULT 1 CHECK(current_revision>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id)
);
CREATE TABLE operation_revisions (
 space_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 effective_date date NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 note text NOT NULL CHECK(length(note)<=1000),
 exceptional boolean NOT NULL,
 merchant_id text,
 counterparty_id text,
 original_minor bigint CHECK(original_minor>0),
 original_currency text CHECK(length(original_currency)=3),
 PRIMARY KEY(space_id,operation_id,revision),
 FOREIGN KEY(space_id,operation_id) REFERENCES financial_operations(space_id,id),
 FOREIGN KEY(space_id,merchant_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,counterparty_id) REFERENCES financial_catalog(space_id,id),
 CHECK((original_minor IS NULL)=(original_currency IS NULL))
);
ALTER TABLE financial_operations ADD CONSTRAINT financial_operations_current_revision
 FOREIGN KEY(space_id,id,current_revision) REFERENCES operation_revisions(space_id,operation_id,revision)
 DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE position_effects (
 space_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 3),
 account_id text NOT NULL,
 kind text NOT NULL CHECK(kind IN ('income','purchase','transfer_out','transfer_in','fee')),
 amount_minor bigint NOT NULL CHECK(amount_minor<>0),
 in_tracking boolean NOT NULL,
 PRIMARY KEY(space_id,operation_id,revision,ordinal),
 FOREIGN KEY(space_id,operation_id,revision) REFERENCES operation_revisions(space_id,operation_id,revision),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id)
);
CREATE INDEX position_effects_account ON position_effects(space_id,account_id,operation_id,revision);
CREATE TABLE economic_components (
 space_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL,
 ordinal integer NOT NULL CHECK(ordinal BETWEEN 0 AND 101),
 effect_ordinal integer NOT NULL,
 direction text NOT NULL CHECK(direction IN ('income','expense')),
 amount_minor bigint NOT NULL CHECK(amount_minor>0),
 category_id text,
 exchange_reference_id text,
 PRIMARY KEY(space_id,operation_id,revision,ordinal),
 FOREIGN KEY(space_id,operation_id,revision,effect_ordinal) REFERENCES position_effects(space_id,operation_id,revision,ordinal),
 FOREIGN KEY(space_id,category_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,exchange_reference_id) REFERENCES exchange_references(space_id,id)
);
CREATE TABLE operation_tags (
 space_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL,
 tag_id text NOT NULL,
 PRIMARY KEY(space_id,operation_id,revision,tag_id),
 FOREIGN KEY(space_id,operation_id,revision) REFERENCES operation_revisions(space_id,operation_id,revision),
 FOREIGN KEY(space_id,tag_id) REFERENCES financial_catalog(space_id,id)
);
CREATE INDEX operation_revisions_date ON operation_revisions(space_id,effective_date,operation_id);
-- +goose Down
DROP TABLE operation_tags;
DROP TABLE economic_components;
DROP TABLE position_effects;
ALTER TABLE financial_operations DROP CONSTRAINT financial_operations_current_revision;
DROP TABLE operation_revisions;
DROP TABLE financial_operations;
