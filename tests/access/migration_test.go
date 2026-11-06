//go:build integration

package access_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/migrations"
)

func TestClientSpaceSelectionMigration(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "migration@example.test")
	dsn := f.db.Pool.Config().ConnString()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	files, err := migrations.Files()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files)
	if err != nil {
		t.Fatal(err)
	}
	// Restore version 2 to exercise upgrading an existing session with a selection.
	if _, err := provider.DownTo(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Ready(t.Context()); err == nil {
		t.Fatal("old schema accepted as ready")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sessions SET active_space=$1 WHERE principal_id=$2`, u.SpaceID, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal("migration reapplication", err)
	}
	if err := f.db.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='sessions' AND column_name='active_space'`) != 0 {
		t.Fatal("session selection column remains after migration")
	}
	profile := f.request[map[string]any](t, "GET", "/v1/me", u.Token, "", nil, 200)
	if profile["id"] != u.ID {
		t.Fatal("migration changed the session identity")
	}
	if _, ok := profile["active_space"]; ok {
		t.Fatal("profile still exposes a selected space")
	}
	f.request[access.SpaceView](t, "GET", "/v1/spaces/"+u.SpaceID, u.Token, "", nil, 200)
}
