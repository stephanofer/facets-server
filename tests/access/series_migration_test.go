//go:build integration

package access_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/migrations"
)

func TestSeriesMigrationPreservesDueApplications(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "series-upgrade@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueInput(bank, "100"), 200).ID
	op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "payment", operationInput("expense", bank, "30"), 200).ID
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", paymentInput(op, due, "30", 1), 200)
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
	if _, err := provider.DownTo(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Ready(t.Context()); err == nil {
		t.Fatal("old planning schema accepted as ready")
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal("migration replay", err)
	}
	if _, err := f.db.Pool.Exec(t.Context(), `GRANT SELECT,INSERT ON recurring_series,recurring_series_versions,recurring_occurrences TO access_app;
 GRANT UPDATE(current_version) ON recurring_series TO access_app;
 GRANT UPDATE(generated_until) ON recurring_series_versions TO access_app;
 GRANT SELECT,INSERT,UPDATE ON planning_resources,due_reminders,series_reminder_preferences TO access_app;
 GRANT SELECT,INSERT,DELETE ON planning_readers TO access_app;
 GRANT SELECT,INSERT ON planning_access_history,reminder_configuration_history,reminder_deliveries,series_reminder_history TO access_app`); err != nil {
		t.Fatal(err)
	}
	old := f.request[finance.DueView](t, "GET", base+"/dues/"+due, u.Token, "", nil, 200)
	if old.AppliedMinor != "30" || old.PendingMinor == nil || *old.PendingMinor != "70" || old.SeriesID != "" {
		t.Fatal("upgrade altered existing expectation", old)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "970")
	id := f.request[financeReference](t, "POST", base+"/series", u.Token, "series", seriesInput(bank), 200).ID
	if _, err := f.db.Pool.Exec(t.Context(), "UPDATE recurring_series SET current_version=2 WHERE space_id=$1 AND id=$2", u.SpaceID, id); err == nil {
		t.Fatal("database accepted a missing current version")
	}
	if err := f.db.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
}
