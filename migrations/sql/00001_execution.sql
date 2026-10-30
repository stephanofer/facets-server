-- +goose Up
CREATE TABLE action_receipts (
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 128),
 scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 128),
 action text NOT NULL CHECK (length(action) BETWEEN 1 AND 80),
 action_key text NOT NULL CHECK (length(action_key) BETWEEN 1 AND 128),
 fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
 result_ref text NOT NULL CHECK (length(result_ref) <= 256),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY (actor, scope, action, action_key)
);
CREATE TABLE technical_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor text NOT NULL CHECK (length(actor) BETWEEN 1 AND 128),
 scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 128),
 event text NOT NULL CHECK (length(event) BETWEEN 1 AND 80),
 resource_ref text NOT NULL CHECK (length(resource_ref) <= 256),
 request_id text NOT NULL CHECK (length(request_id) <= 64),
 occurred_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX technical_audit_time ON technical_audit (occurred_at, id);
CREATE TABLE jobs (
 id text PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 64),
 queue text NOT NULL CHECK (queue IN ('notifications', 'heavy')),
 kind text NOT NULL CHECK (length(kind) BETWEEN 1 AND 80),
 dedup_key text NOT NULL UNIQUE CHECK (length(dedup_key) BETWEEN 1 AND 256),
 fingerprint bytea NOT NULL CHECK (octet_length(fingerprint) = 32),
 payload bytea NOT NULL CHECK (octet_length(payload) <= 65536),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','running','succeeded','failed','uncertain','cancelled')),
 attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 10),
 available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_until timestamptz,
 lease_token text,
 external_effect boolean NOT NULL,
 result_ref text NOT NULL DEFAULT '' CHECK (length(result_ref) <= 256),
 error_code text NOT NULL DEFAULT '' CHECK (length(error_code) <= 80),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK ((state = 'running') = (lease_until IS NOT NULL AND lease_token IS NOT NULL))
);
CREATE INDEX jobs_ready ON jobs (queue, available_at, id) WHERE state = 'pending';
CREATE INDEX jobs_expired ON jobs (lease_until) WHERE state = 'running';
-- +goose Down
DROP TABLE jobs;
DROP TABLE technical_audit;
DROP TABLE action_receipts;
