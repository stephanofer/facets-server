//go:build integration

package environment_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/stephanofer/facets-server/tests/environment/fixturedb"
)

func TestPostgresToolchain(t *testing.T) {
	t.Parallel()
	if testcontainers.ReadConfig().RyukDisabled {
		t.Fatal("Ryuk must be enabled for interrupted-process cleanup")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	data, err := os.ReadFile("../../environment.json")
	if err != nil {
		t.Fatal(err)
	}
	var environment struct {
		PostgresImage string `json:"postgresImage"`
	}
	if err := json.Unmarshal(data, &environment); err != nil {
		t.Fatal(err)
	}
	container, err := postgres.Run(
		ctx,
		environment.PostgresImage,
		postgres.WithDatabase("facets_test"),
		postgres.WithUsername("facets_test"),
		postgres.WithPassword(rand.Text()),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("testdata/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 1 {
		t.Fatalf("migration version = %d, error = %v; want 1", version, err)
	}
	// Reapplying an already applied migration must be safe.
	if results, err := provider.Up(ctx); err != nil || len(results) != 0 {
		t.Fatalf("repeated migration: results = %v, error = %v", results, err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := conn.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("server build: %s", version)
	var versionNumber int
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&versionNumber); err != nil {
		t.Fatal(err)
	}
	if versionNumber != 180006 {
		t.Fatalf("PostgreSQL version = %d, want 180006", versionNumber)
	}
	fixture, err := os.ReadFile("testdata/probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(fixture, &probe); err != nil {
		t.Fatal(err)
	}
	queries := fixturedb.New(conn)
	if err := queries.InsertProbe(ctx, fixturedb.InsertProbeParams{ID: probe.ID, Label: probe.Label}); err != nil {
		t.Fatal(err)
	}
	got, err := queries.GetProbe(ctx, probe.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != probe.ID || got.Label != probe.Label {
		t.Fatalf("query result = %+v, want %+v", got, probe)
	}
	duplicateErr := queries.InsertProbe(ctx, fixturedb.InsertProbeParams{ID: probe.ID, Label: probe.Label})
	if pgErr, ok := errors.AsType[*pgconn.PgError](duplicateErr); !ok || pgErr.Code != "23505" {
		t.Fatalf("duplicate primary key error = %v, want SQLSTATE 23505", duplicateErr)
	}
	if _, err := queries.GetProbe(ctx, 999); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing row error = %v, want pgx.ErrNoRows", err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 0 {
		t.Fatalf("down migration version = %d, error = %v; want 0", version, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := queries.GetProbe(ctx, probe.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("recreated fixture is not empty: %v", err)
	}
}
