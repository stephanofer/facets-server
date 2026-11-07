-- +goose Up
CREATE TABLE financial_accounts (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
 kind text NOT NULL CHECK (kind IN ('cash','bank','savings','wallet','credit_card','simple_debt','loan','financed_purchase','lent_capital','recoverable_advance','service_receivable','refund_receivable')),
 currency text NOT NULL CHECK (length(currency)=3),
 scale integer NOT NULL CHECK (scale BETWEEN 0 AND 9),
 responsible_member text NOT NULL,
 visibility text NOT NULL CHECK (visibility IN ('private','space','selected')),
 space_permission text NOT NULL CHECK (space_permission IN ('read','operate')),
 state text NOT NULL DEFAULT 'active' CHECK (state IN ('active','archived','closed')),
 institution text NOT NULL DEFAULT '' CHECK (length(institution)<=100),
 last_digits text NOT NULL DEFAULT '' CHECK (last_digits='' OR last_digits ~ '^[0-9]{4}$'),
 tracking_date date NOT NULL,
 opening_minor bigint,
 balance_minor bigint,
 quality text NOT NULL CHECK (quality IN ('unknown','estimated','confirmed')),
 version bigint NOT NULL DEFAULT 1 CHECK (version>0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,responsible_member) REFERENCES memberships(space_id,id),
 CHECK ((quality='unknown') = (opening_minor IS NULL)),
 CHECK ((quality='unknown') = (balance_minor IS NULL))
);
CREATE INDEX financial_accounts_space ON financial_accounts(space_id,id);
CREATE INDEX financial_accounts_responsible ON financial_accounts(space_id,responsible_member,id);
CREATE TABLE account_grants (
 space_id text NOT NULL,
 account_id text NOT NULL,
 member_id text NOT NULL,
 permission text NOT NULL CHECK (permission IN ('read','operate')),
 PRIMARY KEY(space_id,account_id,member_id),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id),
 FOREIGN KEY(space_id,member_id) REFERENCES memberships(space_id,id)
);
CREATE INDEX account_grants_member ON account_grants(space_id,member_id,account_id);
-- Prior configuration revisions, not another financial balance or source of ACLs.
CREATE TABLE account_configuration_history (
 space_id text NOT NULL,
 account_id text NOT NULL,
 version bigint NOT NULL CHECK(version>0),
 name text NOT NULL,
 institution text NOT NULL,
 last_digits text NOT NULL,
 responsible_member text NOT NULL,
 visibility text NOT NULL,
 space_permission text NOT NULL,
 state text NOT NULL,
 actor text NOT NULL REFERENCES principals(id),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(space_id,account_id,version),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id),
 FOREIGN KEY(space_id,responsible_member) REFERENCES memberships(space_id,id)
);
CREATE TABLE account_grant_history (
 space_id text NOT NULL,
 account_id text NOT NULL,
 version bigint NOT NULL,
 member_id text NOT NULL,
 permission text NOT NULL,
 PRIMARY KEY(space_id,account_id,version,member_id),
 FOREIGN KEY(space_id,account_id,version) REFERENCES account_configuration_history(space_id,account_id,version),
 FOREIGN KEY(space_id,member_id) REFERENCES memberships(space_id,id)
);
-- Existing non-commercial profiles receive the newly implemented action gate;
-- no commercial grant, validity, quota or existing permission is changed.
INSERT INTO space_capabilities(space_id,capability,ceiling)
 SELECT space_id,'finance.write',1 FROM space_access WHERE source='development'
 ON CONFLICT DO NOTHING;
-- +goose Down
DELETE FROM space_capabilities WHERE capability='finance.write' AND space_id IN
 (SELECT space_id FROM space_access WHERE source='development');
DROP TABLE account_grants;
DROP TABLE account_grant_history;
DROP TABLE account_configuration_history;
DROP TABLE financial_accounts;
