//go:build integration

package access_test

import (
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"testing"
)

func TestAccountlessPrivacyAndApplications(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "planning-owner@example.test")
	member := f.newUser(t, "planning-member@example.test")
	reader := f.newUser(t, "planning-reader@example.test")
	membership := f.join(t, owner, member, "member")
	readerMembership := f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	v := finance.DueCreate{Name: "Unassigned expected income", Date: "2026-11-30", Amount: finance.DueAmount{Certainty: "confirmed", AmountMinor: new("100")}, Currency: "PEN", Direction: "receipt", Confirm: true}
	id := f.request[financeReference](t, "POST", base+"/dues", member.Token, "private-due", v, 200).ID
	got := f.request[finance.DueView](t, "GET", base+"/dues/"+id, member.Token, "", nil, 200)
	if !got.Unassigned || got.AccountID != "" || got.ResourceID != id || got.Currency != "PEN" || got.Scale != 2 {
		t.Fatal(got)
	}
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+id, owner.Token, "", nil, 404)
	edit := finance.PlanningAccessEdit{Version: 1, Confirm: true, ResponsibleMemberID: membership, Visibility: "selected", Readers: []string{readerMembership}, Reason: "Allow consultation only"}
	resource := base + "/planning-resources/" + id
	f.request[financeReference](t, "PUT", resource+"/access", member.Token, "share", edit, 200)
	f.request[finance.DueView](t, "GET", base+"/dues/"+id, reader.Token, "", nil, 200)
	f.request[httpapi.Problem](t, "PUT", base+"/dues/"+id, reader.Token, "reader-edit", finance.DueEdit{DueValue: v.DueValue, Version: 1, Confirm: true, State: "open", Reason: "Not authorized"}, 403)
	f.request[httpapi.Problem](t, "GET", resource+"/access", reader.Token, "", nil, 403)
	bank := f.financialAccount(t, member, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	op := f.request[financeReference](t, "POST", base+"/operations", member.Token, "income", operationInput("income", bank, "100"), 200).ID
	payment := paymentInput(op, id, "100", 1)
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "unconfirmed-restriction", payment, 422)
	payment.ConfirmAccessRestriction = true
	f.request[financeReference](t, "POST", base+"/payment-applications", member.Token, "apply", payment, 200)
	// Sharing the expectation does not share a private receipt through an aggregate.
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+id, reader.Token, "", nil, 404)
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?unassigned=true&limit=1", reader.Token, "", nil, 200)
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatal("private relation leaked", page)
	}
	own := f.request[finance.DueView](t, "GET", base+"/dues/"+id, member.Token, "", nil, 200)
	if own.Status != "paid" || own.AppliedMinor != "100" {
		t.Fatal(own)
	}
	f.assertFinancialBalance(t, member, owner.SpaceID, bank, "1100")
	edit.Version, edit.Visibility, edit.Readers = 2, "space", []string{}
	f.request[financeReference](t, "PUT", resource+"/access", member.Token, "share-space", edit, 200)
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+id, owner.Token, "", nil, 404)
}

func TestAccountlessConservationAndSeries(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "planning-conservation-owner@example.test")
	member := f.newUser(t, "planning-conservation-member@example.test")
	membership := f.join(t, owner, member, "member")
	base := "/v1/spaces/" + owner.SpaceID
	v := finance.SeriesCreate{Name: "Private subscription", Currency: "JPY", Direction: "payment", Confirm: true, Amount: finance.DueAmount{Certainty: "estimated", AmountMinor: new("600")}, Recurrence: calendar.Recurrence{Start: "2026-11-01", Frequency: "monthly"}, Subscription: &finance.FinancialSubscription{Plan: "Synthetic personal plan", RenewalOn: "2026-12-01", CancelBy: "2026-11-28"}}
	id := f.request[financeReference](t, "POST", base+"/series", member.Token, "series", v, 200).ID
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", member.Token, "generate", finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2026-12-01"}, 200)
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?unassigned=true&series_id="+id, member.Token, "", nil, 200)
	if len(page.Items) != 1 || page.Items[0].ResourceID != id || page.Items[0].Scale != 0 {
		t.Fatal(page)
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 || f.count(t, "SELECT count(*) FROM financial_accounts") != 0 {
		t.Fatal("unassigned expectations created financial facts")
	}
	// Departure preserves private planning but never grants the owner access.
	f.request[struct{}](t, "POST", base+"/members/"+membership+"/remove", owner.Token, "remove", map[string]any{"confirm": true}, 200)
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, owner.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, member.Token, "", nil, 404)
	newMembership := f.join(t, owner, member, "member")
	if newMembership == membership {
		t.Fatal("membership reused")
	}
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, member.Token, "", nil, 404)
	refs := f.request[access.Page[finance.RecoveryReference]](t, "GET", base+"/planning-resources/recoverable", member.Token, "", nil, 200)
	if len(refs.Items) != 1 || refs.Items[0].ID != id {
		t.Fatal(refs)
	}
	f.request[financeReference](t, "POST", base+"/planning-resources/"+id+"/recover", member.Token, "recover", finance.FinancialVersionConfirmation{Version: 1, Confirm: true}, 200)
	got := f.request[finance.SeriesView](t, "GET", base+"/series/"+id, member.Token, "", nil, 200)
	if got.Subscription == nil || got.Equivalents == nil || got.Equivalents.Annual.AmountMinor == nil || *got.Equivalents.Annual.AmountMinor != "7200" {
		t.Fatal(got)
	}
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", member.Token, "generate-next", finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2027-01-01"}, 200)
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, owner.Token, "", nil, 404)
}
