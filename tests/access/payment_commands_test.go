//go:build integration

package access_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func TestMovementCapacityLifecycle(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "movement-capacity@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueInput(bank, "100"), 200).ID
	op := operationInput("expense", bank, "100")
	id := f.request[financeReference](t, "POST", base+"/operations", u.Token, "movement", op, 200).ID
	path := base + "/operations/" + id + "/effects/0/application-capacity"
	check := func(revision int64, capacity, applied, available string, eligible bool) {
		t.Helper()
		v := f.request[finance.MovementCapacityView](t, "GET", path, u.Token, "", nil, 200)
		if v.Revision != revision || v.OperationID != id || v.AccountID != bank || v.Currency != "PEN" || v.CapacityMinor != capacity || v.AppliedMinor != applied || v.AvailableMinor != available || v.Eligible != eligible {
			t.Fatalf("unexpected capacity: %+v", v)
		}
	}
	check(1, "100", "0", "100", true)
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", paymentInput(id, due, "60", 1), 200)
	check(1, "100", "60", "40", true)
	op.Applied.AmountMinor = "120"
	f.request[financeReference](t, "PUT", base+"/operations/"+id, u.Token, "increase", finance.OperationCorrection{Version: 1, Reason: "Correct actual charge", Replacement: op}, 200)
	check(2, "120", "60", "60", true)
	apps := f.request[[]finance.PaymentApplicationView](t, "GET", base+"/dues/"+due+"/applications", u.Token, "", nil, 200)
	f.request[financeReference](t, "POST", base+"/dues/"+due+"/applications/"+apps[0].ID+"/unlink", u.Token, "unlink", finance.PaymentUnlink{Version: 2, Confirm: true, Reason: "Release capacity"}, 200)
	check(2, "120", "0", "120", true)
	f.request[financeReference](t, "POST", base+"/operations/"+id+"/void", u.Token, "void", finance.OperationVoid{Version: 2, Confirm: true, Reason: "Recorded in error"}, 200)
	check(3, "0", "0", "0", false)
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+id+"/effects/1/application-capacity", u.Token, "", nil, 422)
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+id+"/effects/-1/application-capacity", u.Token, "", nil, 422)
	prior := operationInput("expense", bank, "1")
	prior.Date = "2026-10-31"
	id = f.request[financeReference](t, "POST", base+"/operations", u.Token, "antecedent", prior, 200).ID
	path = base + "/operations/" + id + "/effects/0/application-capacity"
	check(1, "0", "0", "0", false)
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "1000")
}

func TestPaymentOperationAtomicity(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "atomic-payment@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	first := f.request[financeReference](t, "POST", base+"/dues", u.Token, "first", dueInput(bank, "60"), 200).ID
	second := f.request[financeReference](t, "POST", base+"/dues", u.Token, "second", dueInput(bank, "60"), 200).ID
	valid := func() finance.PaymentOperationCreate {
		return finance.PaymentOperationCreate{Operation: operationInput("expense", bank, "100"), Allocations: []finance.DueAllocation{
			{DueID: first, Version: 1, MovementMinor: "60", DueMinor: "60"},
			{DueID: second, Version: 1, MovementMinor: "40", DueMinor: "40"},
		}}
	}
	for _, tt := range []struct {
		name   string
		status int
		change func(*finance.PaymentOperationCreate)
	}{
		{"stale target", 409, func(v *finance.PaymentOperationCreate) { v.Allocations[1].Version = 2 }},
		{"overcapacity", 422, func(v *finance.PaymentOperationCreate) {
			v.Allocations[1].MovementMinor, v.Allocations[1].DueMinor = "41", "41"
		}},
		{"currency mismatch", 422, func(v *finance.PaymentOperationCreate) { v.Allocations[1].DueMinor = "41" }},
		{"wrong direction", 422, func(v *finance.PaymentOperationCreate) { v.Operation.Kind = "income" }},
		{"antecedent", 422, func(v *finance.PaymentOperationCreate) { v.Operation.Date = "2026-10-31" }},
		{"missing target", 404, func(v *finance.PaymentOperationCreate) { v.Allocations[1].DueID = uuid.New().String() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := valid()
			tt.change(&v)
			f.request[httpapi.Problem](t, "POST", base+"/payment-operations", u.Token, strings.ReplaceAll(tt.name, " ", "-"), v, tt.status)
			f.assertFinancialBalance(t, u, u.SpaceID, bank, "1000")
			for _, table := range []string{"financial_operations", "operation_revisions", "position_effects", "payment_applications"} {
				if f.count(t, "SELECT count(*) FROM "+table) != 0 {
					t.Fatalf("failed command left records in %s", table)
				}
			}
			if f.count(t, "SELECT count(*) FROM due_item_revisions") != 2 {
				t.Fatal("failed command retained a partial due revision")
			}
		})
	}
	v := valid()
	id := f.request[financeReference](t, "POST", base+"/payment-operations", u.Token, "pay", v, 200).ID
	if replay := f.request[financeReference](t, "POST", base+"/payment-operations", u.Token, "pay", v, 200).ID; replay != id {
		t.Fatal("replay duplicated financial fact")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "900")
	paid := f.request[finance.DueView](t, "GET", base+"/dues/"+first, u.Token, "", nil, 200)
	partial := f.request[finance.DueView](t, "GET", base+"/dues/"+second, u.Token, "", nil, 200)
	if paid.Status != "paid" || partial.Status != "partial" || partial.PendingMinor == nil || *partial.PendingMinor != "20" || paid.Version != 2 || partial.Version != 2 {
		t.Fatal("composed command did not derive exact fulfillment", paid, partial)
	}
	v.Operation.Note = "Different content"
	f.request[httpapi.Problem](t, "POST", base+"/payment-operations", u.Token, "pay", v, 409)
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 || f.count(t, "SELECT count(*) FROM payment_applications") != 2 {
		t.Fatal("replay or failed command created duplicate facts/applications")
	}
}

func TestPaymentOperationConcurrentRetry(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "concurrent-payment@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueInput(bank, "100"), 200).ID
	v := finance.PaymentOperationCreate{Operation: operationInput("expense", bank, "100"), Allocations: []finance.DueAllocation{{DueID: due, Version: 1, MovementMinor: "100", DueMinor: "100"}}}
	s := finance.New(f.service, f.store)
	c := access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "concurrent-composed"}
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			id, err := s.CreatePaymentOperation(t.Context(), c, v)
			results <- result{id, err}
		})
	}
	wg.Wait()
	close(results)
	var first string
	for r := range results {
		if r.err != nil || r.id == "" || first != "" && first != r.id {
			t.Fatal("concurrent replay failed", r.id, r.err)
		}
		first = r.id
	}
	// A distinct action based on the stale target version must not leave a
	// second financial fact, even though operation creation precedes allocation.
	c.Key = "distinct-stale"
	if _, err := s.CreatePaymentOperation(t.Context(), c, v); !errors.Is(err, execution.ErrVersion) {
		t.Fatal("stale target accepted", err)
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 || f.count(t, "SELECT count(*) FROM payment_applications") != 1 {
		t.Fatal("concurrency duplicated composed command")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "900")
}

func TestPaymentOperationPermissionsAndRollback(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "payment-owner@example.test")
	member := f.newUser(t, "payment-member@example.test")
	reader := f.newUser(t, "payment-reader@example.test")
	f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	shared := f.financialAccount(t, owner, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	private := f.financialAccount(t, member, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	due := f.request[financeReference](t, "POST", base+"/dues", owner.Token, "due", dueInput(shared, "100"), 200).ID
	v := finance.PaymentOperationCreate{Operation: operationInput("expense", shared, "100"), Allocations: []finance.DueAllocation{{DueID: due, Version: 1, MovementMinor: "100", DueMinor: "100"}}}
	f.request[httpapi.Problem](t, "POST", base+"/payment-operations", reader.Token, "reader", v, 403)
	v.Operation.Applied.AccountID = private
	f.request[httpapi.Problem](t, "POST", base+"/payment-operations", owner.Token, "private-source", v, 404)
	f.request[httpapi.Problem](t, "POST", base+"/payment-operations", member.Token, "privacy-confirmation", v, 422)
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("missing privacy confirmation left a real movement")
	}
	// An audit write failure occurs after every financial write and must still
	// roll back the operation, applications, due versions, projections and receipt.
	v.ConfirmAccessRestriction = true
	s := finance.New(f.service, f.store)
	c := access.SpaceCommand{Authorization: "Bearer " + member.Token, SpaceID: owner.SpaceID, Key: "audit-rollback-payment"}
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	_, commandErr := s.CreatePaymentOperation(t.Context(), c, v)
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	if commandErr == nil || f.count(t, "SELECT count(*) FROM financial_operations") != 0 || f.count(t, "SELECT count(*) FROM payment_applications") != 0 || f.count(t, "SELECT count(*) FROM due_item_revisions") != 1 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='audit-rollback-payment'") != 0 {
		t.Fatal("audit failure left a partial composed command", commandErr)
	}
	f.assertFinancialBalance(t, member, owner.SpaceID, private, "1000")
	id := f.request[financeReference](t, "POST", base+"/payment-operations", member.Token, c.Key, v, 200).ID
	f.assertFinancialBalance(t, member, owner.SpaceID, private, "900")
	f.assertFinancialBalance(t, owner, owner.SpaceID, shared, "1000")
	f.request[httpapi.Problem](t, "GET", base+"/dues/"+due, owner.Token, "", nil, 404)
	f.request[financeReference](t, "POST", base+"/payment-operations", member.Token, c.Key, v, 200)
	_, err := s.ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "revoke-target"}, shared, finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "POST", base+"/payment-operations", member.Token, c.Key, v, 404)
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+id+"/effects/0/application-capacity", member.Token, "", nil, 404)
}

func TestPaymentOperationReceiptsAndCurrency(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "payment-receipts@example.test")
	reader := f.newUser(t, "capacity-reader@example.test")
	f.join(t, u, reader, "reader")
	base := "/v1/spaces/" + u.SpaceID
	dollars := f.financialAccount(t, u, u.SpaceID, "bank", "USD", "1000", "confirmed", "space")
	soles := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	for _, kind := range []string{"income", "refund"} {
		t.Run(kind, func(t *testing.T) {
			d := dueInput(soles, "380")
			d.Direction = "receipt"
			due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due-"+kind, d, 200).ID
			v := finance.PaymentOperationCreate{Operation: operationInput(kind, dollars, "100"), Allocations: []finance.DueAllocation{{DueID: due, Version: 1, MovementMinor: "100", DueMinor: "380"}}, ConfirmAccessRestriction: true}
			id := f.request[financeReference](t, "POST", base+"/payment-operations", u.Token, "receipt-"+kind, v, 200).ID
			detail := f.request[finance.DueView](t, "GET", base+"/dues/"+due, u.Token, "", nil, 200)
			if detail.Status != "paid" || detail.Currency != "PEN" || detail.AppliedMinor != "380" {
				t.Fatal("receipt did not retain explicit attributed amount", detail)
			}
			capacity := f.request[finance.MovementCapacityView](t, "GET", base+"/operations/"+id+"/effects/0/application-capacity", reader.Token, "", nil, 200)
			if !capacity.Eligible || capacity.Direction != "receipt" || capacity.Currency != "USD" || capacity.AppliedMinor != "100" || capacity.AvailableMinor != "0" {
				t.Fatal("capacity did not preserve actual currency or denied a reader", capacity)
			}
		})
	}
	f.assertFinancialBalance(t, u, u.SpaceID, dollars, "1200")
	f.assertFinancialBalance(t, u, u.SpaceID, soles, "1000")
}
