-- +goose Up
ALTER TABLE recurring_series ADD CONSTRAINT recurring_series_current_version
 FOREIGN KEY(space_id,id,current_version)
 REFERENCES recurring_series_versions(space_id,series_id,version)
 DEFERRABLE INITIALLY DEFERRED;
-- +goose Down
ALTER TABLE recurring_series DROP CONSTRAINT recurring_series_current_version;
