//go:build integration

package access_test

import (
	"net/url"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestOperationQueries(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "query-owner@example.test")
	member := f.newUser(t, "query-member@example.test")
	f.join(t, u, member, "member")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "10000", "confirmed", "space")
	card := f.financialAccount(t, u, u.SpaceID, "credit_card", "PEN", "0", "confirmed", "private")
	private := f.financialAccount(t, member, u.SpaceID, "bank", "PEN", "0", "confirmed", "private")
	f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "", "unknown", "private")
	cat := f.request[access.Page[finance.CatalogView]](t, "GET", base+"/catalog?kind=category", u.Token, "", nil, 200)
	food := ""
	for _, c := range cat.Items {
		if c.DefaultKey == "food" {
			food = c.ID
		}
	}
	createCatalog := func(kind, name string) string {
		t.Helper()
		return f.request[financeReference](t, "POST", base+"/catalog", u.Token, uuid.New().String(), map[string]any{"kind": kind, "value": finance.CatalogValue{Name: name}}, 200).ID
	}
	merchant, party, tag := createCatalog("merchant", "Synthetic merchant"), createCatalog("counterparty", "Synthetic counterparty"), createCatalog("tag", "Synthetic tag")
	create := func(v finance.OperationCreate) string {
		t.Helper()
		return f.request[financeReference](t, "POST", base+"/operations", u.Token, uuid.New().String(), v, 200).ID
	}
	create(operationInput("income", bank, "1000"))
	v := operationInput("expense", bank, "1200")
	v.Parts = []finance.CategoryPart{{AmountMinor: "700", CategoryID: food}, {AmountMinor: "500"}}
	v.MerchantID, v.CounterpartyID, v.TagIDs, v.Note, v.Exceptional = merchant, party, []string{tag}, "Literal 10%_ purchase", true
	expense := create(v)
	cv := operationInput("expense", card, "500")
	cv.Parts = []finance.CategoryPart{{AmountMinor: "500", CategoryID: food}}
	create(cv)
	rv := operationInput("refund", bank, "200")
	rv.Date = "2026-11-02"
	rv.Refund = &finance.RefundLink{OperationID: expense, AmountMinor: "200"}
	rv.Parts = []finance.CategoryPart{{AmountMinor: "200", CategoryID: food}}
	create(rv)
	transfer := operationInput("transfer", bank, "300")
	transfer.Received = &finance.AppliedAmount{AccountID: private, AmountMinor: "300"}
	f.request[financeReference](t, "POST", base+"/operations", member.Token, "restricted-transfer", transfer, 200)
	prior := operationInput("income", bank, "100000")
	prior.Date = "2026-10-31"
	create(prior)
	for _, filter := range []string{
		"merchant_id=" + merchant, "counterparty_id=" + party, "tag_id=" + tag,
		"q=" + url.QueryEscape("10%_"), "account_id=" + bank + "&kind=expense&min_minor=1200&max_minor=1200&currency=PEN",
	} {
		page := f.request[access.Page[finance.OperationSummary]](t, "GET", base+"/operations?"+filter, u.Token, "", nil, 200)
		if len(page.Items) != 1 || page.Items[0].ID != expense {
			t.Fatalf("filter %s did not identify the purchase", filter)
		}
	}
	for _, test := range []struct {
		filter string
		status int
	}{
		{"merchant_id=" + uuid.New().String(), 200},
		{"counterparty_id=" + uuid.New().String(), 200},
		{"from=2026-11-02&to=2026-11-02", 422},
		{"cursor=malformed", 422},
		{"min_minor=020", 422},
	} {
		filter, status := test.filter, test.status
		if status == 200 {
			page := f.request[access.Page[finance.OperationSummary]](t, "GET", base+"/operations?"+filter, u.Token, "", nil, status)
			if len(page.Items) != 0 {
				t.Fatal("unknown filter matched missing references")
			}
		} else {
			f.request[httpapi.Problem](t, "GET", base+"/operations?"+filter, u.Token, "", nil, status)
		}
	}
	cursor := ""
	seen := map[string]bool{}
	for range 5 {
		page := f.request[access.Page[finance.OperationSummary]](t, "GET", base+"/operations?limit=1&cursor="+url.QueryEscape(cursor), u.Token, "", nil, 200)
		if len(page.Items) != 1 || seen[page.Items[0].ID] || page.Items[0].Kind == "transfer" {
			t.Fatal("pagination duplicated or leaked a restricted operation")
		}
		seen[page.Items[0].ID] = true
		cursor = page.NextCursor
	}
	if cursor != "" {
		t.Fatal("unexpected trailing cursor")
	}
	suggestions := f.request[[]finance.RecentSelection](t, "GET", base+"/operation-suggestions?kind=expense&merchant_id="+merchant, u.Token, "", nil, 200)
	if len(suggestions) != 1 || suggestions[0].OperationID != expense || suggestions[0].AccountID != bank || suggestions[0].CategoryID != "" {
		t.Fatal("split suggestion assigned one part to the whole fact")
	}
	query := base + "/financial-summary?from=2026-11-01&to=2026-11-03"
	summary := f.request[finance.FinancialSummary](t, "GET", query, u.Token, "", nil, 200)
	want := map[string]string{"position/liquid": "9700", "position/obligation": "500", "economic/income": "1000", "economic/expense": "1500", "cash/income": "1000", "cash/purchase": "-1200", "cash/refund": "200", "cash/transfer_out": "-300"}
	if len(summary.Totals) != len(want) || summary.Scope != "authorized_accounts_only" || summary.Conversion != "original_currencies_only" {
		t.Fatal("summary lost scope or currencies")
	}
	for _, total := range summary.Totals {
		key := total.Reading + "/" + total.Kind
		if total.KnownMinor != want[key] {
			t.Fatalf("%s=%s, want %s", key, total.KnownMinor, want[key])
		}
		if key == "position/liquid" && total.Complete {
			t.Fatal("unknown reference became confirmed zero")
		}
	}
	filtered := f.request[finance.FinancialSummary](t, "GET", query+"&category_id="+food, u.Token, "", nil, 200)
	for _, total := range filtered.Totals {
		if total.Reading == "economic" && (total.Kind != "expense" || total.KnownMinor != "1000") {
			t.Fatal("category components or refund were counted incorrectly")
		}
	}
	// Catalog aliases keep search, economic totals and suggestions in agreement.
	target := f.request[financeReference](t, "POST", base+"/catalog", u.Token, "target", map[string]any{"kind": "category", "value": finance.CatalogValue{Name: "Meals", Direction: "expense", Icon: "receipt", Color: "#123456"}}, 200).ID
	f.request[financeReference](t, "POST", base+"/catalog/"+food+"/merge", u.Token, "merge", finance.CatalogMerge{TargetID: target, Version: 1, TargetVersion: 1, Confirm: true}, 200)
	for _, id := range []string{food, target} {
		page := f.request[access.Page[finance.OperationSummary]](t, "GET", base+"/operations?category_id="+id, u.Token, "", nil, 200)
		if len(page.Items) != 3 {
			t.Fatal("merge lost filtered operations")
		}
	}
	merchantTarget := createCatalog("merchant", "Consolidated merchant")
	f.request[financeReference](t, "POST", base+"/catalog/"+merchant+"/merge", u.Token, "merge-merchant", finance.CatalogMerge{TargetID: merchantTarget, Version: 1, TargetVersion: 1, Confirm: true}, 200)
	mergedSearch := f.request[access.Page[finance.OperationSummary]](t, "GET", base+"/operations?q="+url.QueryEscape("Consolidated merchant"), u.Token, "", nil, 200)
	if len(mergedSearch.Items) != 1 || mergedSearch.Items[0].ID != expense {
		t.Fatal("consolidated merchant name lost its operations")
	}
	f.request[httpapi.Problem](t, "GET", base+"/operations", member.Token+"invalid", "", nil, 401)
}
