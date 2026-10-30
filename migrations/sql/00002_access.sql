-- +goose Up
CREATE TABLE principals (
 id text PRIMARY KEY,
 realm text NOT NULL CHECK (realm IN ('customer','staff')),
 email text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
 password_hash text NOT NULL CHECK (length(password_hash) <= 512),
 display_name text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 100),
 state text NOT NULL CHECK (state IN ('pending','active','suspended')),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 mfa_secret bytea,
 mfa_pending bytea,
 mfa_pending_until timestamptz,
 mfa_step bigint NOT NULL DEFAULT -1,
 locale text NOT NULL DEFAULT 'en',
 zone text NOT NULL DEFAULT 'UTC',
 currency text NOT NULL DEFAULT 'USD',
 notifications boolean NOT NULL DEFAULT true,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE (realm,email),
 CHECK ((mfa_pending IS NULL) = (mfa_pending_until IS NULL))
);
CREATE TABLE recovery_codes (
 principal_id text NOT NULL REFERENCES principals(id),
 verifier bytea NOT NULL CHECK (octet_length(verifier)=32),
 used_at timestamptz,
 PRIMARY KEY(principal_id,verifier)
);
CREATE INDEX principals_pending_mfa ON principals(mfa_pending_until,id) WHERE mfa_pending_until IS NOT NULL;
CREATE TABLE sessions (
 id text PRIMARY KEY,
 principal_id text NOT NULL REFERENCES principals(id),
 verifier bytea NOT NULL UNIQUE CHECK (octet_length(verifier)=32),
 device text NOT NULL CHECK (length(device) BETWEEN 1 AND 100),
 created_at timestamptz NOT NULL,
 seen_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 reauthenticated_at timestamptz NOT NULL,
 revoked_at timestamptz,
 active_space text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_principal ON sessions(principal_id,created_at,id);
CREATE INDEX sessions_live_activity ON sessions(principal_id,seen_at,id) WHERE revoked_at IS NULL;
CREATE TABLE identity_tokens (
 id text PRIMARY KEY,
 principal_id text NOT NULL REFERENCES principals(id),
 purpose text NOT NULL CHECK (purpose IN ('verify','recover','email')),
 verifier bytea NOT NULL UNIQUE CHECK (octet_length(verifier)=32),
 target_email text NOT NULL,
 expires_at timestamptz NOT NULL,
 consumed_at timestamptz,
 invalidated_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX identity_tokens_principal ON identity_tokens(principal_id,purpose,created_at);
CREATE INDEX identity_tokens_expiry ON identity_tokens(expires_at,id) WHERE target_email<>'';
CREATE TABLE access_limits (
 bucket bytea PRIMARY KEY CHECK (octet_length(bucket)=32),
 attempts integer NOT NULL CHECK (attempts > 0),
 expires_at timestamptz NOT NULL
);
CREATE INDEX access_limits_expiry ON access_limits(expires_at);
CREATE TABLE spaces (
 id text PRIMARY KEY,
 name text NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
 purpose text NOT NULL CHECK (purpose IN ('personal','family','group')),
 currency text NOT NULL CHECK (length(currency)=3),
 zone text NOT NULL CHECK (length(zone) BETWEEN 1 AND 100),
 locale text NOT NULL CHECK (length(locale) BETWEEN 2 AND 50),
 period_day integer NOT NULL CHECK (period_day BETWEEN 1 AND 31),
 new_account_visibility text NOT NULL CHECK (new_account_visibility IN ('private','space','propose_shared')),
 personal_for text UNIQUE REFERENCES principals(id),
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 access_epoch bigint NOT NULL DEFAULT 1 CHECK (access_epoch > 0),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE memberships (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 principal_id text NOT NULL REFERENCES principals(id),
 role text NOT NULL CHECK (role IN ('owner','admin','member','reader')),
 joined_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 ended_at timestamptz,
 UNIQUE(space_id,id)
);
CREATE UNIQUE INDEX memberships_active ON memberships(space_id,principal_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX memberships_owner ON memberships(space_id) WHERE ended_at IS NULL AND role='owner';
CREATE INDEX memberships_principal ON memberships(principal_id,space_id) WHERE ended_at IS NULL;
CREATE TABLE space_access (
 space_id text PRIMARY KEY REFERENCES spaces(id),
 source text NOT NULL CHECK (source IN ('development','commercial')),
 profile text NOT NULL CHECK (length(profile) BETWEEN 1 AND 100),
 source_ref text NOT NULL DEFAULT '' CHECK (length(source_ref)<=256),
 state text NOT NULL CHECK (state IN ('active','restricted')),
 valid_from timestamptz NOT NULL,
 valid_until timestamptz NOT NULL,
 billing_member text NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 FOREIGN KEY(space_id,billing_member) REFERENCES memberships(space_id,id),
 CHECK(valid_until>valid_from)
);
CREATE TABLE space_capabilities (
 space_id text NOT NULL REFERENCES space_access(space_id),
 capability text NOT NULL CHECK (length(capability) BETWEEN 1 AND 80),
 ceiling bigint NOT NULL CHECK (ceiling>=0),
 used bigint NOT NULL DEFAULT 0 CHECK (used>=0),
 PRIMARY KEY(space_id,capability)
);
CREATE TABLE invitations (
 id text PRIMARY KEY,
 space_id text NOT NULL REFERENCES spaces(id),
 inviter text NOT NULL,
 email text NOT NULL CHECK (length(email) BETWEEN 3 AND 254),
 role text NOT NULL CHECK (role IN ('admin','member','reader')),
 verifier bytea NOT NULL UNIQUE CHECK (octet_length(verifier)=32),
 state text NOT NULL CHECK (state IN ('pending','accepted','rejected','cancelled','expired')),
 expires_at timestamptz NOT NULL,
 accepted_member text,
 generation integer NOT NULL DEFAULT 1 CHECK (generation > 0),
 sent_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(space_id,inviter) REFERENCES memberships(space_id,id),
 FOREIGN KEY(space_id,accepted_member) REFERENCES memberships(space_id,id)
);
CREATE UNIQUE INDEX invitations_pending ON invitations(space_id,email) WHERE state='pending';
CREATE INDEX invitations_space ON invitations(space_id,id);
-- Short-lived encrypted delivery material, never a reusable plaintext outbox payload.
CREATE TABLE access_deliveries (
 id text PRIMARY KEY,
 kind text NOT NULL CHECK (kind IN ('verify','recover','email','invitation','security')),
 resource_ref text NOT NULL,
 principal_id text REFERENCES principals(id),
 target_email text NOT NULL,
 ciphertext bytea NOT NULL CHECK (octet_length(ciphertext)<=8192),
 expires_at timestamptz NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX access_deliveries_expiry ON access_deliveries(expires_at) WHERE target_email<>'';
CREATE INDEX access_delivery_secrets_expiry ON access_deliveries(expires_at) WHERE octet_length(ciphertext)>0;
-- +goose Down
DROP TABLE access_deliveries;
DROP TABLE invitations;
DROP TABLE space_capabilities;
DROP TABLE space_access;
DROP TABLE memberships;
DROP TABLE spaces;
DROP TABLE access_limits;
DROP TABLE identity_tokens;
DROP TABLE sessions;
DROP TABLE recovery_codes;
DROP TABLE principals;
