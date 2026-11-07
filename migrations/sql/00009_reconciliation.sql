-- +goose Up
ALTER TABLE financial_operations DROP CONSTRAINT financial_operations_kind_check;
ALTER TABLE financial_operations ADD CONSTRAINT financial_operations_kind_check
 CHECK(kind IN ('income','expense','transfer','refund','adjustment'));
ALTER TABLE position_effects DROP CONSTRAINT position_effects_kind_check;
ALTER TABLE position_effects ADD CONSTRAINT position_effects_kind_check
 CHECK(kind IN ('income','purchase','transfer_out','transfer_in','fee','refund','adjustment'));
CREATE TABLE account_reconciliations (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 account_id text NOT NULL,
 effective_date date NOT NULL,
 calculated_minor bigint,
 observed_minor bigint NOT NULL,
 difference_minor numeric(20,0),
 adjustment_id text,
 author text NOT NULL REFERENCES principals(id),
 reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 500),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(space_id,id),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id),
 FOREIGN KEY(space_id,adjustment_id) REFERENCES financial_operations(space_id,id),
 CHECK((calculated_minor IS NULL)=(difference_minor IS NULL)),
 CHECK(difference_minor=observed_minor::numeric-calculated_minor::numeric),
 CHECK(adjustment_id IS NULL OR (difference_minor IS NOT NULL AND difference_minor<>0))
);
CREATE INDEX account_reconciliations_account ON account_reconciliations(space_id,account_id,id);
CREATE TABLE reconciliation_reviews (
 space_id text NOT NULL,
 reconciliation_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL,
 PRIMARY KEY(space_id,reconciliation_id,operation_id,revision),
 FOREIGN KEY(space_id,reconciliation_id) REFERENCES account_reconciliations(space_id,id),
 FOREIGN KEY(space_id,operation_id,revision) REFERENCES operation_revisions(space_id,operation_id,revision)
);
CREATE TABLE balance_consistency_checks (
 id text PRIMARY KEY,
 space_id text NOT NULL,
 account_id text NOT NULL,
 projected_minor bigint,
 reconstructed_minor numeric,
 discrepancy boolean NOT NULL,
 author text NOT NULL REFERENCES principals(id),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(space_id,account_id) REFERENCES financial_accounts(space_id,id)
);
CREATE INDEX balance_consistency_checks_account ON balance_consistency_checks(space_id,account_id,id);
CREATE INDEX operation_revisions_recent_selection ON operation_revisions(space_id,author,recorded_at DESC,operation_id DESC);
-- +goose Down
DROP INDEX operation_revisions_recent_selection;
DROP TABLE balance_consistency_checks;
DROP TABLE reconciliation_reviews;
DROP TABLE account_reconciliations;
ALTER TABLE position_effects DROP CONSTRAINT position_effects_kind_check;
ALTER TABLE position_effects ADD CONSTRAINT position_effects_kind_check CHECK(kind IN ('income','purchase','transfer_out','transfer_in','fee','refund'));
ALTER TABLE financial_operations DROP CONSTRAINT financial_operations_kind_check;
ALTER TABLE financial_operations ADD CONSTRAINT financial_operations_kind_check CHECK(kind IN ('income','expense','transfer','refund'));
