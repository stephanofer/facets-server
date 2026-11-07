-- +goose Up
ALTER TABLE recurring_series_versions
 ADD COLUMN counterparty_id text,
 ADD COLUMN suggested_category_id text,
 ADD COLUMN subscription_plan text,
 ADD COLUMN trial_until date,
 ADD COLUMN renewal_on date,
 ADD COLUMN cancel_by date,
 ADD COLUMN cancellation_on date,
 ADD FOREIGN KEY(space_id,counterparty_id) REFERENCES financial_catalog(space_id,id),
 ADD FOREIGN KEY(space_id,suggested_category_id) REFERENCES financial_catalog(space_id,id),
 ADD CHECK(subscription_plan IS NULL OR length(subscription_plan) BETWEEN 1 AND 100),
 ADD CHECK(subscription_plan IS NOT NULL OR (trial_until IS NULL AND renewal_on IS NULL AND cancel_by IS NULL AND cancellation_on IS NULL)),
 ADD CHECK(trial_until IS NULL OR trial_until>=start_date),
 ADD CHECK(renewal_on IS NULL OR renewal_on>=start_date),
 ADD CHECK(cancel_by IS NULL OR renewal_on IS NULL OR cancel_by<=renewal_on);

-- +goose Down
ALTER TABLE recurring_series_versions
 DROP COLUMN counterparty_id,
 DROP COLUMN suggested_category_id,
 DROP COLUMN subscription_plan,
 DROP COLUMN trial_until,
 DROP COLUMN renewal_on,
 DROP COLUMN cancel_by,
 DROP COLUMN cancellation_on;
