# Production migrations

Goose SQL migrations will be added with the first persisted capability in block 2.
Use a global sequential numbering scheme (`00001_description.sql`) and explicit
`-- +goose Up` / `-- +goose Down` sections. Published migrations are immutable.
Run migrations as an explicit deployment task, never on every server startup.

Block 1 exercises Goose against disposable fixtures in
`tests/environment/testdata/migrations`, not against the development database.
Application and migration credentials will be separated when runtime persistence
is introduced; the local Compose bootstrap identity is not a production role.
