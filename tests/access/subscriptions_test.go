//go:build integration

package access_test

import (
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"testing"
)

func TestSeriesMetadataAndSubscriptions(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "subscriptions@example.test")
	other := f.newUser(t, "subscriptions-other@example.test")
	f.join(t, u, other, "member")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	counterpart := f.request[financeReference](t, "POST", base+"/catalog", u.Token, "counterpart", map[string]any{"kind": "counterparty", "value": finance.CatalogValue{Name: "Synthetic provider"}}, 200).ID
	categories := f.request[access.Page[finance.CatalogView]](t, "GET", base+"/catalog?kind=category", u.Token, "", nil, 200)
	var expense, income string
	for _, c := range categories.Items {
		if c.Value.Direction == "expense" {
			expense = c.ID
		} else {
			income = c.ID
		}
	}
	v := seriesInput(bank)
	v.CounterpartyID = counterpart
	v.SuggestedCategoryID = expense
	v.Subscription = &finance.FinancialSubscription{Plan: "Personal test plan", TrialUntil: "2026-11-10", RenewalOn: "2026-12-01", CancelBy: "2026-11-28"}
	bad := v
	bad.SuggestedCategoryID = income
	f.request[httpapi.Problem](t, "POST", base+"/series", u.Token, "wrong-category", bad, 422)
	bad = v
	bad.CounterpartyID = expense
	f.request[httpapi.Problem](t, "POST", base+"/series", u.Token, "wrong-counterparty", bad, 422)
	foreign := f.request[financeReference](t, "POST", "/v1/spaces/"+other.SpaceID+"/catalog", other.Token, "foreign", map[string]any{"kind": "counterparty", "value": finance.CatalogValue{Name: "Not this space"}}, 200).ID
	bad = v
	bad.CounterpartyID = foreign
	f.request[httpapi.Problem](t, "POST", base+"/series", u.Token, "foreign-reference", bad, 404)
	id := f.request[financeReference](t, "POST", base+"/series", u.Token, "subscription", v, 200).ID
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2026-12-01"}, 200)
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?series_id="+id, u.Token, "", nil, 200)
	if len(page.Items) != 1 || page.Items[0].SuggestedCategoryID != expense || page.Items[0].CounterpartyID != counterpart || page.Items[0].SubscriptionPlan != "Personal test plan" {
		t.Fatal(page)
	}
	// A private actual charge restricts the occurrence, not the shared series' descriptive conditions.
	actual := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "charge", operationInput("expense", actual, "100"), 200).ID
	apply := paymentInput(op, page.Items[0].ID, "100", 1)
	apply.ConfirmAccessRestriction = true
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", apply, 200)
	got := f.request[finance.SeriesView](t, "GET", base+"/series/"+id, u.Token, "", nil, 200)
	if got.LastVisibleCharge == nil || got.LastVisibleCharge.OperationID != op || got.Equivalents.Annual.AmountMinor == nil || *got.Equivalents.Annual.AmountMinor != "1200" {
		t.Fatal(got)
	}
	limited := f.request[finance.SeriesView](t, "GET", base+"/series/"+id, other.Token, "", nil, 200)
	if limited.LastVisibleCharge != nil {
		t.Fatal("private charge leaked through subscription", limited)
	}
	edit := finance.SeriesEdit{SeriesValue: v.SeriesValue, Version: 1, Confirm: true, State: "cancelled", EffectiveFrom: "2026-12-01", Reason: "Tracking ended; provider cancellation recorded separately"}
	edit.Amount.AmountMinor = new("150")
	edit.Subscription = &finance.FinancialSubscription{Plan: "Changed test plan", CancellationOn: "2026-11-07"}
	f.request[financeReference](t, "PUT", base+"/series/"+id, u.Token, "cancel", edit, 200)
	history := f.request[access.Page[finance.SeriesVersionView]](t, "GET", base+"/series/"+id+"/versions", u.Token, "", nil, 200)
	if len(history.Items) != 2 || history.Items[0].Subscription.Plan != "Personal test plan" || *history.Items[0].Amount.AmountMinor != "100" || *history.Items[1].Amount.AmountMinor != "150" {
		t.Fatal(history)
	}
	old := f.request[finance.DueView](t, "GET", base+"/dues/"+page.Items[0].ID, u.Token, "", nil, 200)
	if old.SubscriptionPlan != "Personal test plan" || old.Status != "paid" {
		t.Fatal("price change rewrote history", old)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "1000")
	f.assertFinancialBalance(t, u, u.SpaceID, actual, "900")
}
