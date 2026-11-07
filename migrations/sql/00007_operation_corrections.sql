-- +goose Up
ALTER TABLE operation_revisions
 ADD COLUMN change_kind text NOT NULL DEFAULT 'initial' CHECK(change_kind IN ('initial','descriptive','financial','void')),
 ADD COLUMN reason text NOT NULL DEFAULT '' CHECK(length(reason)<=500),
 ADD COLUMN voided boolean NOT NULL DEFAULT false,
 ADD CONSTRAINT operation_revision_void CHECK(voided=(change_kind='void'));

-- +goose Down
ALTER TABLE operation_revisions
 DROP CONSTRAINT operation_revision_void,
 DROP COLUMN voided,
 DROP COLUMN reason,
 DROP COLUMN change_kind;
