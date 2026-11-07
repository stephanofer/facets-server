//go:build integration

package access_test

import (
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func dueInput(account, amount string) finance.DueCreate {
	return finance.DueCreate{AccountID: account, Direction: "payment", Confirm: true, DueValue: finance.DueValue{Name: "Synthetic rent", Date: "2026-11-01", Amount: finance.DueAmount{Certainty: "confirmed", AmountMinor: new(amount)}}}
}

func paymentInput(operation, due, amount string, version int64) finance.PaymentApplicationCreate {
	return finance.PaymentApplicationCreate{OperationID: operation, Revision: 1, Confirm: true, Allocations: []finance.DueAllocation{{DueID: due, Version: version, MovementMinor: amount, DueMinor: amount}}}
}

func TestDuesFulfillmentAndCorrections(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "dues@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "300000", "confirmed", "private")
	v := dueInput(bank, "200000")
	due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "rent", v, 200).ID
	if replay := f.request[financeReference](t, "POST", base+"/dues", u.Token, "rent", v, 200).ID; replay != due {
		t.Fatal("expectation duplicated on retry")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "300000")
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("reaching the due date created a financial fact")
	}
	op := operationInput("expense", bank, "185000")
	operation := f.request[financeReference](t, "POST", base+"/operations", u.Token, "actual-payment", op, 200).ID
	application := paymentInput(operation, due, "185000", 1)
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", application, 200)
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", application, 200)
	read := func() finance.DueView {
		t.Helper()
		return f.request[finance.DueView](t, "GET", base+"/dues/"+due, u.Token, "", nil, 200)
	}
	detail := read()
	if detail.Status != "partial" || detail.PendingMinor == nil || *detail.PendingMinor != "15000" || detail.Version != 2 {
		t.Fatalf("1850 paid 2000 obligation: %+v", detail)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "115000")
	// A correction retains applications only if their actual capacity survives.
	reduced := op
	reduced.Applied.AmountMinor = "184999"
	f.request[httpapi.Problem](t, "PUT", base+"/operations/"+operation, u.Token, "invalid-reduction", finance.OperationCorrection{Version: 1, Reason: "Insufficient replacement", Replacement: reduced}, 422)
	op.Note = "Corrected description"
	f.request[financeReference](t, "PUT", base+"/operations/"+operation, u.Token, "describe-payment", finance.OperationCorrection{Version: 1, Reason: "Description only", Replacement: op}, 200)
	if read().AppliedMinor != "185000" {
		t.Fatal("descriptive revision lost the application")
	}
	f.request[financeReference](t, "POST", base+"/operations/"+operation+"/void", u.Token, "void-payment", finance.OperationVoid{Version: 2, Confirm: true, Reason: "Recorded in error"}, 200)
	detail = read()
	if detail.AppliedMinor != "0" || detail.Status == "paid" || detail.Version != 3 || *detail.PendingMinor != "200000" {
		t.Fatalf("void did not reopen obligation: %+v", detail)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "300000")
	if f.count(t, "SELECT count(*) FROM payment_applications") != 1 || f.count(t, "SELECT count(*) FROM payment_application_releases") != 1 {
		t.Fatal("void erased application evidence")
	}
	// Explicit confirmation of the real amount, not a tolerance, closes an estimate.
	v.Amount.Certainty = "estimated"
	estimate := f.request[financeReference](t, "POST", base+"/dues", u.Token, "estimated", v, 200).ID
	operation = f.request[financeReference](t, "POST", base+"/operations", u.Token, "real-charge", op, 200).ID
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply-estimate", paymentInput(operation, estimate, "185000", 1), 200)
	detail = f.request[finance.DueView](t, "GET", base+"/dues/"+estimate, u.Token, "", nil, 200)
	if detail.Status != "partial" || detail.PendingMinor != nil {
		t.Fatal("estimate pretended to have a confirmed remainder")
	}
	v.Amount.Certainty, v.Amount.AmountMinor = "confirmed", new("185000")
	edit := finance.DueEdit{DueValue: v.DueValue, Version: 2, Confirm: true, State: "open", Reason: "Confirmed invoice"}
	f.request[financeReference](t, "PUT", base+"/dues/"+estimate, u.Token, "confirm-real", edit, 200)
	detail = f.request[finance.DueView](t, "GET", base+"/dues/"+estimate, u.Token, "", nil, 200)
	if detail.Status != "paid" || *detail.PendingMinor != "0" {
		t.Fatal("confirmed actual amount did not close obligation")
	}
	apps := f.request[[]finance.PaymentApplicationView](t, "GET", base+"/dues/"+estimate+"/applications", u.Token, "", nil, 200)
	f.request[financeReference](t, "POST", base+"/dues/"+estimate+"/applications/"+apps[0].ID+"/unlink", u.Token, "unlink", finance.PaymentUnlink{Version: 3, Confirm: true, Reason: "Wrong obligation"}, 200)
	detail = f.request[finance.DueView](t, "GET", base+"/dues/"+estimate, u.Token, "", nil, 200)
	if detail.AppliedMinor != "0" || detail.Status == "paid" {
		t.Fatal("unlink did not recalculate fulfillment")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "115000")
}

func TestDuesAtomicDistributionAndConcurrency(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "due-capacity@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	makeDue := func(key string) string {
		t.Helper()
		return f.request[financeReference](t, "POST", base+"/dues", u.Token, key, dueInput(bank, "100"), 200).ID
	}
	first, second := makeDue("first"), makeDue("second")
	op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "movement", operationInput("expense", bank, "100"), 200).ID
	s := finance.New(f.service, f.store)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{first, second} {
		wg.Go(func() {
			_, err := s.ApplyPayment(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: uuid.New().String()}, paymentInput(op, id, "60", 1))
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
	if success != 1 || denied != 1 || f.count(t, "SELECT sum(movement_minor) FROM payment_applications") != 60 {
		t.Fatal("concurrent requests overused movement capacity")
	}
	third, fourth := makeDue("third"), makeDue("fourth")
	allocation := paymentInput(op, third, "20", 1)
	allocation.Allocations = append(allocation.Allocations, finance.DueAllocation{DueID: fourth, Version: 2, MovementMinor: "20", DueMinor: "20"})
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", u.Token, "atomic-version", allocation, 409)
	if f.count(t, "SELECT count(*) FROM payment_applications") != 1 {
		t.Fatal("invalid second allocation partially committed first")
	}
	allocation.Allocations[1].Version = 1
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "remaining-capacity", allocation, 200)
	if f.count(t, "SELECT sum(movement_minor) FROM payment_applications") != 100 {
		t.Fatal("atomic split did not conserve movement capacity")
	}
	// Audit failure must roll back a new expectation and its receipt/history.
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	_, err := s.CreateDue(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "due-rollback"}, dueInput(bank, "10"))
	if err == nil {
		t.Fatal("audit failure committed expectation")
	}
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "SELECT count(*) FROM due_items") != 4 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='due-rollback'") != 0 {
		t.Fatal("rollback left partial records")
	}
}

func TestDuesPrivacyAndCurrency(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "due-owner@example.test")
	member := f.newUser(t, "due-member@example.test")
	reader := f.newUser(t, "due-reader@example.test")
	f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	shared := f.financialAccount(t, owner, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	private := f.financialAccount(t, member, owner.SpaceID, "bank", "USD", "1000", "confirmed", "private")
	v := dueInput(shared, "380")
	due := f.request[financeReference](t, "POST", base+"/dues", owner.Token, "shared-due", v, 200).ID
	f.request[finance.DueView](t, "GET", base+"/dues/"+due, reader.Token, "", nil, 200)
	f.request[httpapi.Problem](t, "POST", base+"/dues", reader.Token, "reader-due", v, 403)
	op := f.request[financeReference](t, "POST", base+"/operations", member.Token, "private-payment", operationInput("expense", private, "100"), 200).ID
	application := paymentInput(op, due, "100", 1)
	application.Allocations[0].DueMinor = "380"
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", owner.Token, "inaccessible-source", application, 404)
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "missing-privacy-confirmation", application, 422)
	application.ConfirmAccessRestriction = true
	f.request[financeReference](t, "POST", base+"/payment-applications", member.Token, "explicit-conversion", application, 200)
	detail := f.request[finance.DueView](t, "GET", base+"/dues/"+due, member.Token, "", nil, 200)
	if detail.Status != "paid" || detail.Currency != "PEN" || detail.AppliedMinor != "380" {
		t.Fatal("actual attribution was replaced by a conversion guess")
	}
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+due, owner.Token, "", nil, 404)
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?limit=1", owner.Token, "", nil, 200)
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatal("pagination disclosed restricted derived information")
	}
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+due+"/applications", owner.Token, "", nil, 404)
	f.assertFinancialBalance(t, member, owner.SpaceID, private, "900")
	f.assertFinancialBalance(t, owner, owner.SpaceID, shared, "1000")
	apps := f.request[[]finance.PaymentApplicationView](t, "GET", base+"/dues/"+due+"/applications", member.Token, "", nil, 200)
	f.request[financeReference](t, "POST", base+"/dues/"+due+"/applications/"+apps[0].ID+"/unlink", member.Token, "release-private-relation", finance.PaymentUnlink{Version: 2, Confirm: true, Reason: "No longer linked"}, 200)
	f.request[finance.DueView](t, "GET", base+"/dues/"+due, owner.Token, "", nil, 200)
	// A shared movement must not expose capacity allocated to a private due.
	privateDue := f.request[financeReference](t, "POST", base+"/dues", member.Token, "private-expectation", dueInput(private, "26"), 200).ID
	sharedExpense := operationInput("expense", shared, "100")
	sharedOperation := f.request[financeReference](t, "POST", base+"/operations", member.Token, "shared-payment", sharedExpense, 200).ID
	privateApplication := paymentInput(sharedOperation, privateDue, "100", 1)
	privateApplication.Allocations[0].DueMinor = "26"
	privateApplication.ConfirmAccessRestriction = true
	f.request[financeReference](t, "POST", base+"/payment-applications", member.Token, "private-capacity", privateApplication, 200)
	f.request[finance.OperationView](t, "GET", base+"/operations/"+sharedOperation, owner.Token, "", nil, 200)
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+sharedOperation+"/effects/0/application-capacity", owner.Token, "", nil, 404)
	capacity := f.request[finance.MovementCapacityView](t, "GET", base+"/operations/"+sharedOperation+"/effects/0/application-capacity", member.Token, "", nil, 200)
	if capacity.AvailableMinor != "0" || capacity.AppliedMinor != "100" {
		t.Fatal("authorized capacity did not reflect private allocation", capacity)
	}
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", owner.Token, "capacity-probe", paymentInput(sharedOperation, due, "1", 3), 404)
	sharedExpense.Applied.AmountMinor = "99"
	f.request[httpapi.Problem](t, "PUT", base+"/operations/"+sharedOperation, owner.Token, "correction-probe", finance.OperationCorrection{Version: 1, Reason: "Must not reveal private allocated capacity", Replacement: sharedExpense}, 404)
	f.request[financeReference](t, "POST", base+"/operations/"+sharedOperation+"/void", owner.Token, "void-visible-source", finance.OperationVoid{Version: 1, Confirm: true, Reason: "Authorized source reversal"}, 200)
	detail = f.request[finance.DueView](t, "GET", base+"/dues/"+privateDue, member.Token, "", nil, 200)
	if detail.AppliedMinor != "0" || detail.Status == "paid" {
		t.Fatal("void failed to recalculate restricted obligation")
	}
}

func TestDuesValidationAndLifecycle(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "due-validation@example.test")
	member := f.newUser(t, "due-validator@example.test")
	membership := f.join(t, owner, member, "member")
	base := "/v1/spaces/" + owner.SpaceID
	account := accountInput("selected")
	account.Sharing.Grants = []finance.Grant{{MemberID: membership, Permission: "operate"}}
	bank := f.request[financeReference](t, "POST", base+"/accounts", owner.Token, "selected", account, 200).ID
	v := dueInput(bank, "100")
	due := f.request[financeReference](t, "POST", base+"/dues", member.Token, "member-due", v, 200).ID
	second := f.request[financeReference](t, "POST", base+"/dues", owner.Token, "second-due", v, 200).ID
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?from=2026-11-01&until=2026-11-02&limit=1", member.Token, "", nil, 200)
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("agenda did not paginate the exact date interval")
	}
	next := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?limit=1&cursor="+page.NextCursor, member.Token, "", nil, 200)
	if len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID {
		t.Fatal("agenda repeated an item")
	}
	f.request[httpapi.Problem](t, "GET", base+"/dues?cursor=malformed", member.Token, "", nil, 422)
	op := f.request[financeReference](t, "POST", base+"/operations", member.Token, "purchase", operationInput("expense", bank, "100"), 200).ID
	application := paymentInput(op, due, "50", 1)
	application.Allocations[0].DueMinor = "51"
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "same-currency-mismatch", application, 422)
	application.Allocations[0].DueMinor = "50"
	f.request[financeReference](t, "POST", base+"/payment-applications", member.Token, "partial", application, 200)
	cancel := finance.DueEdit{DueValue: v.DueValue, Version: 2, Confirm: true, State: "cancelled", Reason: "Needs explicit unlink first"}
	f.request[httpapi.Problem](t, "PUT", base+"/dues/"+due, member.Token, "cancel-applied", cancel, 422)
	// A supplied receipt cannot be satisfied with a purchase, nor by an antecedent.
	v.Direction = "receipt"
	receipt := f.request[financeReference](t, "POST", base+"/dues", owner.Token, "receipt", v, 200).ID
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "wrong-direction", paymentInput(op, receipt, "50", 1), 422)
	prior := operationInput("expense", bank, "10")
	prior.Date = "2026-10-31"
	old := f.request[financeReference](t, "POST", base+"/operations", member.Token, "antecedent", prior, 200).ID
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "historical-application", paymentInput(old, second, "10", 1), 422)
	income := f.request[financeReference](t, "POST", base+"/operations", member.Token, "income", operationInput("income", bank, "100"), 200).ID
	f.request[financeReference](t, "POST", base+"/payment-applications", member.Token, "receipt-application", paymentInput(income, receipt, "100", 1), 200)
	// Revocation invalidates lists, details and replay, not just new commands.
	s := finance.New(f.service, f.store)
	_, err := s.ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "revoke-due-access"}, bank, finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+due, member.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/dues", member.Token, "member-due", dueInput(bank, "100"), 404)
	f.request[httpapi.Problem](t, "POST", base+"/payment-applications", member.Token, "partial", application, 404)
	page = f.request[access.Page[finance.DueView]](t, "GET", base+"/dues", member.Token, "", nil, 200)
	if len(page.Items) != 0 {
		t.Fatal("revoked dues remained in agenda")
	}
}

func TestDuesFiltersBeforePagination(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "agenda-filters@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	var zone string
	if err := f.db.Pool.QueryRow(t.Context(), "SELECT zone FROM spaces WHERE id=$1", u.SpaceID).Scan(&zone); err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(location)
	for _, tc := range []struct {
		name, state, applied string
		days                 int
	}{
		{"pending", "open", "", 30}, {"upcoming", "open", "", 3},
		{"overdue", "open", "", -10}, {"partial", "open", "30", -10},
		{"paid", "open", "100", -10}, {"skipped", "skipped", "", -10},
		{"cancelled", "cancelled", "", -10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := dueInput(bank, "100")
			v.Name, v.Date = tc.name, today.AddDate(0, 0, tc.days).Format(time.DateOnly)
			id := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due-"+tc.name, v, 200).ID
			if tc.state != "open" {
				f.request[financeReference](t, "PUT", base+"/dues/"+id, u.Token, "state-"+tc.name, finance.DueEdit{DueValue: v.DueValue, Version: 1, Confirm: true, State: tc.state, Reason: "Synthetic state"}, 200)
			}
			if tc.applied != "" {
				op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "op-"+tc.name, operationInput("expense", bank, tc.applied), 200).ID
				f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply-"+tc.name, paymentInput(op, id, tc.applied, 1), 200)
			}
			page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?limit=1&status="+tc.name+"&origin=standalone&account_id="+bank+"&direction=payment&currency=PEN", u.Token, "", nil, 200)
			if len(page.Items) != 1 || page.Items[0].ID != id || page.Items[0].Status != tc.name || page.NextCursor != "" {
				t.Fatal("status filter differs from fulfillment or ran after pagination", page)
			}
		})
	}
	for _, query := range []string{"currency=USD", "origin=recurring", "direction=receipt", "series_id=" + uuid.New().String()} {
		t.Run(query, func(t *testing.T) {
			page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?limit=1&"+query, u.Token, "", nil, 200)
			if len(page.Items) != 0 || page.NextCursor != "" {
				t.Fatal("nonmatching agenda filter", page)
			}
		})
	}
	for _, query := range []string{"currency=XXX", "origin=invalid", "direction=invalid", "status=open", "account_id=invalid"} {
		t.Run(query, func(t *testing.T) { f.request[httpapi.Problem](t, "GET", base+"/dues?"+query, u.Token, "", nil, 422) })
	}
}
