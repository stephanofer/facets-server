-- +goose Up
CREATE TABLE financial_catalog (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 kind text NOT NULL CHECK(kind IN ('category','merchant','counterparty','tag')),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 100),
 direction text NOT NULL DEFAULT '' CHECK(direction IN ('','income','expense')),
 parent_id text,
 icon text NOT NULL DEFAULT '' CHECK(length(icon)<=50),
 color text NOT NULL DEFAULT '' CHECK(color='' OR color ~ '^#[0-9A-Fa-f]{6}$'),
 default_key text,
 archived boolean NOT NULL DEFAULT false,
 merged_into text,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 UNIQUE(space_id,default_key),
 FOREIGN KEY(space_id,parent_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,merged_into) REFERENCES financial_catalog(space_id,id),
 CHECK(parent_id IS NULL OR parent_id<>id),
 CHECK(merged_into IS NULL OR (merged_into<>id AND archived)),
 CHECK((kind='category' AND direction<>'' AND icon<>'' AND color<>'') OR
 (kind<>'category' AND direction='' AND parent_id IS NULL AND default_key IS NULL))
);
CREATE INDEX financial_catalog_space ON financial_catalog(space_id,kind,id);
CREATE INDEX financial_catalog_parent ON financial_catalog(space_id,parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX financial_catalog_redirect ON financial_catalog(space_id,merged_into) WHERE merged_into IS NOT NULL;
CREATE TABLE catalog_configuration_history (
 space_id text NOT NULL,
 entry_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 name text NOT NULL,
 parent_id text,
 icon text NOT NULL,
 color text NOT NULL,
 archived boolean NOT NULL,
 merged_into text,
 actor text NOT NULL REFERENCES principals(id),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,entry_id,version),
 FOREIGN KEY(space_id,entry_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,parent_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,merged_into) REFERENCES financial_catalog(space_id,id)
);
CREATE UNIQUE INDEX financial_catalog_names ON financial_catalog(space_id,kind,direction,COALESCE(parent_id,''),lower(btrim(name))) WHERE NOT archived;
CREATE TABLE catalog_merges (
 source_id text PRIMARY KEY,
 space_id text NOT NULL,
 target_id text NOT NULL,
 actor text NOT NULL REFERENCES principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(space_id,source_id) REFERENCES financial_catalog(space_id,id),
 FOREIGN KEY(space_id,target_id) REFERENCES financial_catalog(space_id,id)
);
ALTER TABLE financial_accounts ADD COLUMN counterparty_id text;
ALTER TABLE account_configuration_history ADD COLUMN counterparty_id text;
ALTER TABLE financial_accounts ADD FOREIGN KEY(space_id,counterparty_id) REFERENCES financial_catalog(space_id,id);
ALTER TABLE account_configuration_history ADD FOREIGN KEY(space_id,counterparty_id) REFERENCES financial_catalog(space_id,id);
CREATE INDEX financial_accounts_counterparty ON financial_accounts(space_id,counterparty_id) WHERE counterparty_id IS NOT NULL;
CREATE TABLE exchange_references (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 from_currency text NOT NULL CHECK(length(from_currency)=3),
 to_currency text NOT NULL CHECK(length(to_currency)=3),
 rate text NOT NULL CHECK(length(rate)<=80 AND rate ~ '^[0-9]+(\.[0-9]+)?$' AND rate::numeric>0),
 effective_date date NOT NULL,
 context text NOT NULL CHECK(context IN ('historical','current')),
 source text NOT NULL CHECK(length(source) BETWEEN 1 AND 200),
 precision_digits integer NOT NULL CHECK(precision_digits BETWEEN 1 AND 80),
 rounding text NOT NULL DEFAULT 'half_even' CHECK(rounding='half_even'),
 replaces text,
 author text NOT NULL REFERENCES principals(id),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 UNIQUE(replaces),
 FOREIGN KEY(space_id,replaces) REFERENCES exchange_references(space_id,id),
 CHECK(from_currency<>to_currency),
 CHECK(replaces IS NULL OR replaces<>id)
);
CREATE INDEX exchange_references_scope ON exchange_references(space_id,id);
CREATE INDEX exchange_references_pair ON exchange_references(space_id,from_currency,to_currency,context,effective_date,id);
-- +goose Down
DROP TABLE exchange_references;
ALTER TABLE financial_accounts DROP COLUMN counterparty_id;
ALTER TABLE account_configuration_history DROP COLUMN counterparty_id;
DROP TABLE catalog_merges;
DROP TABLE catalog_configuration_history;
DROP TABLE financial_catalog;
