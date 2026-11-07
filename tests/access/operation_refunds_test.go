//go:build integration

package access_test

import (
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestOperationRefunds(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "refunds@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "10000", "confirmed", "private")
	card := f.financialAccount(t, u, u.SpaceID, "credit_card", "PEN", "0", "confirmed", "private")
	usd := f.financialAccount(t, u, u.SpaceID, "bank", "USD", "0", "confirmed", "private")
	purchase := operationInput("expense", card, "1200")
	id := f.request[financeReference](t, "POST", base+"/operations", u.Token, "purchase", purchase, 200).ID
	refund := operationInput("refund", bank, "500")
	refund.Date = "2026-11-02"
	refund.Refund = &finance.RefundLink{OperationID: id, AmountMinor: "500"}
	key := "refund"
	rid := f.request[financeReference](t, "POST", base+"/operations", u.Token, key, refund, 200).ID
	if replay := f.request[financeReference](t, "POST", base+"/operations", u.Token, key, refund, 200).ID; replay != rid {
		t.Fatal("refund replay duplicated the fact")
	}
	v := f.request[finance.OperationView](t, "GET", base+"/operations/"+rid, u.Token, "", nil, 200)
	if v.Refund == nil || v.Refund.OperationID != id || v.Components[0].Direction != "expense" || v.Components[0].AmountMinor != "-500" || v.Effects[0].AmountMinor != "500" || v.Date != "2026-11-02" {
		t.Fatal("refund became income or rewrote the purchase")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "10500")
	f.assertFinancialBalance(t, u, u.SpaceID, card, "1200")
	change := finance.OperationCorrection{Version: 1, Reason: "Invalid reduction", Replacement: purchase}
	change.Replacement.Applied.AmountMinor = "400"
	f.request[httpapi.Problem](t, "PUT", base+"/operations/"+id, u.Token, "reduce-purchase", change, 422)
	f.request[httpapi.Problem](t, "POST", base+"/operations/"+id+"/void", u.Token, "void-linked-purchase", finance.OperationVoid{Version: 1, Confirm: true, Reason: "Still refunded"}, 422)
	tooMuch := refund
	tooMuch.Applied.AmountMinor = "701"
	tooMuch.Refund = &finance.RefundLink{OperationID: id, AmountMinor: "701"}
	f.request[httpapi.Problem](t, "POST", base+"/operations", u.Token, "over-refund", tooMuch, 422)
	foreign := operationInput("refund", usd, "100")
	foreign.Refund = &finance.RefundLink{OperationID: id, AmountMinor: "380"}
	foreignID := f.request[financeReference](t, "POST", base+"/operations", u.Token, "foreign-refund", foreign, 200).ID
	f.assertFinancialBalance(t, u, u.SpaceID, usd, "100")
	cardRefund := operationInput("refund", card, "320")
	cardRefund.Refund = &finance.RefundLink{OperationID: id, AmountMinor: "320"}
	f.request[financeReference](t, "POST", base+"/operations", u.Token, "card-refund", cardRefund, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, card, "880")
	f.request[financeReference](t, "POST", base+"/operations/"+foreignID+"/void", u.Token, "void-foreign-refund", finance.OperationVoid{Version: 1, Confirm: true, Reason: "Wrong credit"}, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, usd, "0")
	refund.Applied.AmountMinor = "400"
	refund.Refund.AmountMinor = "400"
	f.request[financeReference](t, "PUT", base+"/operations/"+rid, u.Token, "correct-refund", finance.OperationCorrection{Version: 1, Reason: "Corrected actual credit", Replacement: refund}, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "10400")
	antecedent := operationInput("refund", bank, "50")
	antecedent.Date = "2026-10-31"
	f.request[financeReference](t, "POST", base+"/operations", u.Token, "prior-refund", antecedent, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "10400")
}

func TestRefundConcurrencyAndPrivacy(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "refund-owner@example.test")
	member := f.newUser(t, "refund-member@example.test")
	reader := f.newUser(t, "refund-reader@example.test")
	f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	private := f.financialAccount(t, member, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	shared := f.financialAccount(t, owner, owner.SpaceID, "bank", "PEN", "0", "confirmed", "space")
	id := f.request[financeReference](t, "POST", base+"/operations", member.Token, "purchase", operationInput("expense", private, "100"), 200).ID
	v := operationInput("refund", shared, "60")
	v.Refund = &finance.RefundLink{OperationID: id, AmountMinor: "60"}
	f.request[httpapi.Problem](t, "POST", base+"/operations", owner.Token, "private-link", v, 404)
	s := finance.New(f.service)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := s.CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + member.Token, SpaceID: owner.SpaceID, Key: uuid.New().String()}, v)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	success, denied := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, access.ErrInvalid) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatal("concurrent refunds exceeded the purchase")
	}
	page := f.request[access.Page[finance.AccountHistoryEntry]](t, "GET", base+"/accounts/"+shared+"/history", owner.Token, "", nil, 200)
	rid := page.Items[0].OperationID
	detail := f.request[finance.OperationView](t, "GET", base+"/operations/"+rid, owner.Token, "", nil, 200)
	if detail.Refund != nil {
		t.Fatal("refund disclosed private purchase reference")
	}
	f.request[httpapi.Problem](t, "POST", base+"/operations/"+rid+"/void", owner.Token, "hidden-source", finance.OperationVoid{Version: 1, Confirm: true, Reason: "No source access"}, 404)
	f.request[httpapi.Problem](t, "POST", base+"/operations", reader.Token, "reader-refund", operationInput("refund", shared, "10"), 403)
	f.assertFinancialBalance(t, owner, owner.SpaceID, shared, "60")
}
