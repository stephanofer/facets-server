//go:build integration

package access_test

import (
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestAccountReconciliationAndConsistency(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "reconciliation@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "120000", "confirmed", "private")
	path := base + "/accounts/" + bank
	v := finance.ReconciliationCreate{Date: "2026-11-02", ObservedMinor: "115000", ExpectedCalculatedMinor: new("120000"), Adjust: true, Reason: "Observed statement difference", Confirm: true}
	preview := f.request[finance.ReconciliationPreview](t, "POST", path+"/reconciliation-preview", u.Token, "", finance.ReconciliationObservation{Date: v.Date, ObservedMinor: v.ObservedMinor}, 200)
	if *preview.CalculatedMinor != "120000" || *preview.DifferenceMinor != "-5000" || f.count(t, "SELECT count(*) FROM account_reconciliations") != 0 || f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("preview wrote a financial fact or compared the wrong date")
	}
	id := f.request[financeReference](t, "POST", path+"/reconciliations", u.Token, "reconcile", v, 200).ID
	if replay := f.request[financeReference](t, "POST", path+"/reconciliations", u.Token, "reconcile", v, 200).ID; replay != id {
		t.Fatal("reconciliation replay duplicated adjustment")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "115000")
	page := f.request[access.Page[finance.ReconciliationView]](t, "GET", path+"/reconciliations", u.Token, "", nil, 200)
	r := page.Items[0]
	if r.ID != id || *r.CalculatedMinor != "120000" || r.ObservedMinor != "115000" || *r.DifferenceMinor != "-5000" || r.AdjustmentID == "" || r.NeedsReview {
		t.Fatal("reconciliation lost evidence")
	}
	adjustment := f.request[finance.OperationView](t, "GET", base+"/operations/"+r.AdjustmentID, u.Token, "", nil, 200)
	if adjustment.Kind != "adjustment" || len(adjustment.Components) != 0 || adjustment.Effects[0].AmountMinor != "-5000" {
		t.Fatal("adjustment invented an expense")
	}
	f.request[httpapi.Problem](t, "POST", path+"/reconciliations", u.Token, "stale-balance", v, 409)
	// Late history affects a reconciled date; a later fact does not.
	later := operationInput("income", bank, "100")
	later.Date = "2026-11-03"
	op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "later-income", later, 200).ID
	page = f.request[access.Page[finance.ReconciliationView]](t, "GET", path+"/reconciliations", u.Token, "", nil, 200)
	if page.Items[0].NeedsReview {
		t.Fatal("later history invalidated an earlier reconciliation")
	}
	change := finance.OperationCorrection{Version: 1, Reason: "Correct actual date", Replacement: later}
	change.Replacement.Date = "2026-11-01"
	consequences := f.request[finance.OperationConsequences](t, "POST", base+"/operations/"+op+"/correction-preview", u.Token, "", change, 200)
	if !consequences.Accounts[0].ReconciledHistoryAffected {
		t.Fatal("preview omitted reconciled history warning")
	}
	f.request[financeReference](t, "PUT", base+"/operations/"+op, u.Token, "backdate-income", change, 200)
	page = f.request[access.Page[finance.ReconciliationView]](t, "GET", path+"/reconciliations", u.Token, "", nil, 200)
	if !page.Items[0].NeedsReview || *page.Items[0].CalculatedMinor != "120000" || *page.Items[0].DifferenceMinor != "-5000" {
		t.Fatal("history change rewrote evidence or omitted warning")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "115100")
	f.request[financeReference](t, "POST", path+"/consistency-checks", u.Token, "consistent", map[string]bool{"confirm": true}, 200)
	checks := f.request[access.Page[finance.ConsistencyView]](t, "GET", path+"/consistency-checks", u.Token, "", nil, 200)
	if len(checks.Items) != 1 || checks.Items[0].Discrepancy || *checks.Items[0].ReconstructedMinor != "115100" {
		t.Fatal("valid reconstruction reported a discrepancy")
	}
	// Privileged corruption is detected and retained, never silently repaired.
	if _, err := f.db.Pool.Exec(t.Context(), "UPDATE financial_accounts SET balance_minor=1 WHERE id=$1", bank); err != nil {
		t.Fatal(err)
	}
	f.request[financeReference](t, "POST", path+"/consistency-checks", u.Token, "discrepancy", map[string]bool{"confirm": true}, 200)
	checks = f.request[access.Page[finance.ConsistencyView]](t, "GET", path+"/consistency-checks", u.Token, "", nil, 200)
	found := false
	for _, c := range checks.Items {
		if c.Discrepancy {
			found = true
			if *c.ProjectedMinor != "1" || *c.ReconstructedMinor != "115100" {
				t.Fatal("incident lost discrepant values")
			}
		}
	}
	if !found {
		t.Fatal("no discrepancy incident")
	}
	acc := f.request[finance.AccountView](t, "GET", path, u.Token, "", nil, 200)
	if *acc.BalanceMinor != "1" {
		t.Fatal("check silently repaired data")
	}
}

func TestReconciliationConcurrencyPrivacyAndRollback(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "reconcile-owner@example.test")
	member := f.newUser(t, "reconcile-member@example.test")
	reader := f.newUser(t, "reconcile-reader@example.test")
	f.join(t, u, member, "member")
	f.join(t, u, reader, "reader")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1200", "confirmed", "space")
	private := f.financialAccount(t, member, u.SpaceID, "bank", "PEN", "50", "confirmed", "private")
	v := finance.ReconciliationCreate{Date: "2026-11-02", ObservedMinor: "1150", ExpectedCalculatedMinor: new("1200"), Adjust: true, Reason: "Synthetic comparison", Confirm: true}
	path := base + "/accounts/" + bank
	f.request[httpapi.Problem](t, "POST", path+"/reconciliations", reader.Token, "reader", v, 403)
	f.request[httpapi.Problem](t, "GET", base+"/accounts/"+private+"/reconciliations", u.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/accounts/"+private+"/consistency-checks", u.Token, "private-check", map[string]bool{"confirm": true}, 404)
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	s := finance.New(f.service, f.store)
	if _, err := s.ReconcileAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "rollback"}, bank, v); err == nil {
		t.Fatal("audit failure committed an adjustment")
	}
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "1200")
	if f.count(t, "SELECT count(*) FROM account_reconciliations") != 0 || f.count(t, "SELECT count(*) FROM financial_operations") != 0 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='rollback'") != 0 {
		t.Fatal("rollback left partial reconciliation evidence")
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := s.ReconcileAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: uuid.New().String()}, bank, v)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, execution.ErrVersion) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("concurrent comparisons used a stale calculation")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "1150")
	unknown := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "", "unknown", "private")
	v.ExpectedCalculatedMinor = nil
	f.request[httpapi.Problem](t, "POST", base+"/accounts/"+unknown+"/reconciliations", u.Token, "unknown-adjustment", v, 422)
	v.Adjust = false
	f.request[financeReference](t, "POST", base+"/accounts/"+unknown+"/reconciliations", u.Token, "unknown-observation", v, 200)
	page := f.request[access.Page[finance.ReconciliationView]](t, "GET", base+"/accounts/"+unknown+"/reconciliations", u.Token, "", nil, 200)
	if page.Items[0].CalculatedMinor != nil || page.Items[0].DifferenceMinor != nil {
		t.Fatal("unknown opening reference became a zero difference")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, unknown, "unknown")
	for _, date := range []string{"2026-10-31", "9999-12-31"} {
		v.Date = date
		f.request[httpapi.Problem](t, "POST", path+"/reconciliations", u.Token, uuid.New().String(), v, 422)
	}
}
