-- +goose Up
ALTER TABLE financial_operations DROP CONSTRAINT financial_operations_kind_check;
ALTER TABLE financial_operations ADD CONSTRAINT financial_operations_kind_check
 CHECK(kind IN ('income','expense','transfer','refund'));
ALTER TABLE position_effects DROP CONSTRAINT position_effects_kind_check;
ALTER TABLE position_effects ADD CONSTRAINT position_effects_kind_check
 CHECK(kind IN ('income','purchase','transfer_out','transfer_in','fee','refund'));
ALTER TABLE economic_components DROP CONSTRAINT economic_components_amount_minor_check;
ALTER TABLE economic_components ADD CONSTRAINT economic_components_amount_minor_check
 CHECK(amount_minor<>0 AND (amount_minor>0 OR direction='expense'));

-- The amount attributed to the purchase is separate from the actual credit.
-- They can differ in currency or because the provider used a different rate.
CREATE TABLE operation_refunds (
 space_id text NOT NULL,
 operation_id text NOT NULL,
 revision bigint NOT NULL,
 purchase_id text NOT NULL,
 purchase_revision bigint NOT NULL,
 purchase_minor bigint NOT NULL CHECK(purchase_minor>0),
 PRIMARY KEY(space_id,operation_id,revision),
 FOREIGN KEY(space_id,operation_id,revision) REFERENCES operation_revisions(space_id,operation_id,revision),
 FOREIGN KEY(space_id,purchase_id,purchase_revision) REFERENCES operation_revisions(space_id,operation_id,revision),
 CHECK(operation_id<>purchase_id)
);
CREATE INDEX operation_refunds_purchase ON operation_refunds(space_id,purchase_id,operation_id,revision);

-- +goose Down
DROP TABLE operation_refunds;
ALTER TABLE economic_components DROP CONSTRAINT economic_components_amount_minor_check;
ALTER TABLE economic_components ADD CONSTRAINT economic_components_amount_minor_check CHECK(amount_minor>0);
ALTER TABLE position_effects DROP CONSTRAINT position_effects_kind_check;
ALTER TABLE position_effects ADD CONSTRAINT position_effects_kind_check CHECK(kind IN ('income','purchase','transfer_out','transfer_in','fee'));
ALTER TABLE financial_operations DROP CONSTRAINT financial_operations_kind_check;
ALTER TABLE financial_operations ADD CONSTRAINT financial_operations_kind_check CHECK(kind IN ('income','expense','transfer'));
