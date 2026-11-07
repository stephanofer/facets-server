//go:build integration

package access_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/finance/financedb"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func operationInput(kind, account, amount string) finance.OperationCreate {
	return finance.OperationCreate{Kind: kind, Date: "2026-11-01", Applied: finance.AppliedAmount{AccountID: account, AmountMinor: amount}, Parts: []finance.CategoryPart{}, Fees: []finance.OperationFee{}, TagIDs: []string{}, Confirm: true}
}

func (f *fixture) financialAccount(t *testing.T, u user, space, kind, currency, opening, quality, visibility string) string {
	t.Helper()
	v := accountInput(visibility)
	v.Kind, v.Currency, v.Opening.Quality = kind, currency, quality
	v.Opening.AmountMinor = nil
	if quality != "unknown" {
		v.Opening.AmountMinor = new(opening)
	}
	return f.request[financeReference](t, "POST", "/v1/spaces/"+space+"/accounts", u.Token, uuid.New().String(), v, 200).ID
}

func (f *fixture) assertFinancialBalance(t *testing.T, u user, space, id, want string) {
	t.Helper()
	v := f.request[finance.AccountView](t, "GET", "/v1/spaces/"+space+"/accounts/"+id, u.Token, "", nil, 200)
	got := "unknown"
	if v.BalanceMinor != nil {
		got = *v.BalanceMinor
	}
	if got != want {
		t.Fatalf("balance = %s, want %s", got, want)
	}
	rebuilt, err := financedb.New(f.db.Pool).ReconstructedBalance(t.Context(), financedb.ReconstructedBalanceParams{SpaceID: space, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if want == "unknown" {
		if rebuilt != "" {
			t.Fatal("reconstruction invented an opening reference")
		}
	} else if rebuilt != want {
		t.Fatalf("reconstructed balance = %+v, want %s", rebuilt, want)
	}
}

func TestFinancialOperationsRecognition(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "operations@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "100000", "confirmed", "private")
	card := f.financialAccount(t, u, u.SpaceID, "credit_card", "PEN", "0", "confirmed", "private")
	usd := f.financialAccount(t, u, u.SpaceID, "bank", "USD", "0", "confirmed", "private")
	categories := f.request[access.Page[finance.CatalogView]](t, "GET", base+"/catalog?kind=category", u.Token, "", nil, 200)
	var food, costs, salary string
	for _, category := range categories.Items {
		switch category.DefaultKey {
		case "food":
			food = category.ID
		case "financial_costs":
			costs = category.ID
		case "salary":
			salary = category.ID
		}
	}
	create := func(v finance.OperationCreate) finance.OperationView {
		t.Helper()
		id := f.request[financeReference](t, "POST", base+"/operations", u.Token, uuid.New().String(), v, 200).ID
		return f.request[finance.OperationView](t, "GET", base+"/operations/"+id, u.Token, "", nil, 200)
	}
	income := operationInput("income", bank, "10000")
	income.Parts = []finance.CategoryPart{{AmountMinor: "10000", CategoryID: salary}}
	v := create(income)
	if len(v.Components) != 1 || v.Components[0].Direction != "income" || v.Effects[0].AmountMinor != "10000" || v.Origin != "manual" || v.Revision != 1 {
		t.Fatal("income did not recognize exactly one economic component and effect")
	}
	expense := operationInput("expense", bank, "12000")
	expense.Exceptional = true
	createCatalog := func(kind, name string) string {
		t.Helper()
		return f.request[financeReference](t, "POST", base+"/catalog", u.Token, uuid.New().String(), map[string]any{"kind": kind, "value": finance.CatalogValue{Name: name}}, 200).ID
	}
	tag := createCatalog("tag", "Synthetic trip")
	expense.MerchantID = createCatalog("merchant", "Synthetic merchant")
	expense.CounterpartyID = createCatalog("counterparty", "Synthetic counterparty")
	expense.TagIDs = []string{tag}
	expense.Note = "Synthetic receipt\nManual classification"
	expense.Parts = []finance.CategoryPart{{AmountMinor: "7000", CategoryID: food}, {AmountMinor: "5000"}}
	v = create(expense)
	if len(v.Effects) != 1 || len(v.Components) != 2 || !v.Exceptional || v.Components[1].CategoryID != "" || len(v.Tags) != 1 || v.Tags[0].ID != tag || v.Note != expense.Note || v.MerchantID != expense.MerchantID || v.CounterpartyID != expense.CounterpartyID {
		t.Fatal("split duplicated the position or lost pending classification")
	}
	cardPurchase := operationInput("expense", card, "12000")
	cardPurchase.Parts = []finance.CategoryPart{{AmountMinor: "12000", CategoryID: food}}
	v = create(cardPurchase)
	if v.Effects[0].AmountMinor != "12000" || v.Components[0].Direction != "expense" {
		t.Fatal("purchase did not increase the obligation once")
	}
	transfer := operationInput("transfer", bank, "3800")
	transfer.Received = &finance.AppliedAmount{AccountID: usd, AmountMinor: "1000"}
	transfer.Fees = []finance.OperationFee{{AppliedAmount: finance.AppliedAmount{AccountID: bank, AmountMinor: "100"}, CategoryID: costs}}
	v = create(transfer)
	if len(v.Effects) != 3 || len(v.Components) != 1 || v.Components[0].AmountMinor != "100" || v.Components[0].EffectOrdinal != 2 {
		t.Fatal("transfer principal became income/expense or fee was hidden")
	}
	price := operationInput("expense", bank, "7600")
	price.Original = &finance.OriginalPrice{AmountMinor: "2000", Currency: "USD"}
	v = create(price)
	if v.Original == nil || v.Original.AmountMinor != "2000" || v.Effects[0].AmountMinor != "-7600" || v.Components[0].AmountMinor != "7600" {
		t.Fatal("merchant original price replaced the applied amount")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "86500")
	f.assertFinancialBalance(t, u, u.SpaceID, card, "12000")
	f.assertFinancialBalance(t, u, u.SpaceID, usd, "1000")
	fx := f.request[financeReference](t, "POST", base+"/exchange-references", u.Token, "historical-rate", finance.ExchangeCreate{FromCurrency: "USD", ToCurrency: "PEN", Rate: "3.8", Date: "2026-11-01", Context: "historical", Source: "Synthetic reference"}, 200).ID
	foreignIncome := operationInput("income", usd, "100")
	foreignIncome.ExchangeReferenceID = fx
	v = create(foreignIncome)
	if v.Components[0].ExchangeReferenceID != fx || v.Effects[0].AmountMinor != "100" {
		t.Fatal("historical reference changed the actual account amount")
	}
	foreignIncome.Date = "2026-11-02"
	f.request[httpapi.Problem](t, "POST", base+"/operations", u.Token, "wrong-rate-date", foreignIncome, 422)
	foreignIncome.Date = "2026-11-01"
	foreignIncome.Applied.AccountID = bank
	f.request[httpapi.Problem](t, "POST", base+"/operations", u.Token, "wrong-rate-direction", foreignIncome, 422)
	f.assertFinancialBalance(t, u, u.SpaceID, usd, "1100")
	unknown := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "", "unknown", "private")
	create(operationInput("income", unknown, "100"))
	f.assertFinancialBalance(t, u, u.SpaceID, unknown, "unknown")
	prior := operationInput("expense", bank, "100000")
	prior.Date = "2026-10-31"
	v = create(prior)
	if v.Effects[0].InTracking {
		t.Fatal("antecedent included in opening reference applied twice")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "86500")
	// The same stable identifiers still explain the fact after catalog merging.
	target := f.request[financeReference](t, "POST", base+"/catalog", u.Token, "merge-target", map[string]any{"kind": "category", "value": finance.CatalogValue{Name: "Meals", Direction: "expense", Icon: "utensils", Color: "#123456"}}, 200).ID
	f.request[financeReference](t, "POST", base+"/catalog/"+food+"/merge", u.Token, "merge-used", finance.CatalogMerge{TargetID: target, Version: 1, TargetVersion: 1, Confirm: true}, 200)
	page := f.request[access.Page[finance.AccountHistoryEntry]](t, "GET", base+"/accounts/"+card+"/history?limit=1", u.Token, "", nil, 200)
	if len(page.Items) != 1 || page.Items[0].AmountMinor != "12000" {
		t.Fatal("history does not explain the card position")
	}
	v = f.request[finance.OperationView](t, "GET", base+"/operations/"+page.Items[0].OperationID, u.Token, "", nil, 200)
	if v.Components[0].CategoryID != food || v.Components[0].CanonicalCategoryID != target {
		t.Fatal("catalog merge lost original or current classification")
	}
}

func TestFinancialOperationsPrivacyAndAtomicity(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "operations-owner@example.test")
	member := f.newUser(t, "operations-member@example.test")
	reader := f.newUser(t, "operations-reader@example.test")
	membership := f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	shared := f.financialAccount(t, owner, owner.SpaceID, "bank", "PEN", "10000", "confirmed", "space")
	private := f.financialAccount(t, member, owner.SpaceID, "cash", "PEN", "0", "confirmed", "private")
	v := operationInput("transfer", shared, "100")
	v.Received = &finance.AppliedAmount{AccountID: private, AmountMinor: "100"}
	v.Note = "Private counter-end metadata"
	id := f.request[financeReference](t, "POST", base+"/operations", member.Token, "private-transfer", v, 200).ID
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+id, owner.Token, "", nil, 404)
	page := f.request[access.Page[finance.AccountHistoryEntry]](t, "GET", base+"/accounts/"+shared+"/history?limit=1", owner.Token, "", nil, 200)
	if len(page.Items) != 1 || page.Items[0].OperationID != id || page.Items[0].AmountMinor != "-100" {
		t.Fatal("restricted relation hid the authorized account effect")
	}
	f.request[httpapi.Problem](t, "GET", base+"/accounts/"+private+"/history", owner.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/operations", reader.Token, "reader-operation", operationInput("income", shared, "10"), 403)
	f.request[httpapi.Problem](t, "POST", base+"/operations", owner.Token, "private-endpoint", v, 404)
	foreign := f.financialAccount(t, member, member.SpaceID, "bank", "PEN", "0", "confirmed", "private")
	foreignTransfer := operationInput("transfer", shared, "100")
	foreignTransfer.Received = &finance.AppliedAmount{AccountID: foreign, AmountMinor: "100"}
	f.request[httpapi.Problem](t, "POST", base+"/operations", owner.Token, "cross-space", foreignTransfer, 404)
	// Failing the durable audit must roll back both effects, projections and receipt.
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	_, err := finance.New(f.service).CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + member.Token, SpaceID: owner.SpaceID, Key: "audit-rollback"}, v)
	if err == nil {
		t.Fatal("audit failure confirmed a financial operation")
	}
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	f.assertFinancialBalance(t, owner, owner.SpaceID, shared, "9900")
	f.assertFinancialBalance(t, member, owner.SpaceID, private, "100")
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='audit-rollback'") != 0 {
		t.Fatal("failed operation left partial records")
	}
	// Removing the author revokes old details and replay, without deleting facts.
	f.request[map[string]any](t, "POST", base+"/members/"+membership+"/remove", owner.Token, "remove-author", map[string]any{"confirm": true}, 200)
	f.request[httpapi.Problem](t, "GET", base+"/operations/"+id, member.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/operations", member.Token, "private-transfer", v, 404)
	f.assertFinancialBalance(t, owner, owner.SpaceID, shared, "9900")
}

func TestFinancialOperationsConcurrencyAndRange(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "operations-race@example.test")
	id := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "0", "confirmed", "private")
	s := finance.New(f.service)
	v := operationInput("income", id, "100")
	var wg sync.WaitGroup
	results := make(chan string, 8)
	errorsFound := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			id, err := s.CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "same-operation"}, v)
			results <- id
			errorsFound <- err
		})
	}
	wg.Wait()
	close(results)
	close(errorsFound)
	var first string
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		if first == "" {
			first = result
		}
		if result != first {
			t.Fatal("concurrent retry duplicated operation")
		}
	}
	f.assertFinancialBalance(t, u, u.SpaceID, id, "100")
	v.Applied.AmountMinor = "101"
	if _, err := s.CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "same-operation"}, v); !errors.Is(err, execution.ErrConflict) {
		t.Fatal("changed content did not conflict", err)
	}
	errorsFound = make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			_, err := s.CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "distinct-" + strconv.Itoa(i)}, v)
			errorsFound <- err
		})
	}
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	f.assertFinancialBalance(t, u, u.SpaceID, id, "908")
	max := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "9223372036854775807", "confirmed", "private")
	if _, err := s.CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "overflow"}, operationInput("income", max, "1")); !errors.Is(err, access.ErrInvalid) {
		t.Fatal("overflow was accepted", err)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, max, "9223372036854775807")
	if f.count(t, "SELECT count(*) FROM financial_operations") != 9 {
		t.Fatal("overflow left a partial operation")
	}
	// Financial history cannot be overwritten or deleted by the runtime role.
	if _, err := f.store.DB.Pool.Exec(t.Context(), "UPDATE position_effects SET amount_minor=1 WHERE account_id=$1", id); err == nil {
		t.Fatal("runtime can overwrite financial effects")
	}
	if _, err := f.store.DB.Pool.Exec(t.Context(), "DELETE FROM operation_revisions WHERE operation_id=$1", first); err == nil {
		t.Fatal("runtime can delete financial history")
	}
}

func TestFinancialOperationsValidationAndHistory(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "operations-validation@example.test")
	base := "/v1/spaces/" + u.SpaceID
	a := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "0", "confirmed", "private")
	b := f.financialAccount(t, u, u.SpaceID, "cash", "PEN", "0", "confirmed", "private")
	for _, tc := range []struct {
		name string
		edit func(*finance.OperationCreate)
	}{
		{name: "split sum", edit: func(v *finance.OperationCreate) { v.Parts = []finance.CategoryPart{{AmountMinor: "99"}} }},
		{name: "split overflow", edit: func(v *finance.OperationCreate) {
			v.Parts = []finance.CategoryPart{{AmountMinor: "9223372036854775807"}, {AmountMinor: "1"}}
		}},
		{name: "zero", edit: func(v *finance.OperationCreate) { v.Applied.AmountMinor = "0" }},
		{name: "negative", edit: func(v *finance.OperationCreate) { v.Applied.AmountMinor = "-100" }},
		{name: "noncanonical", edit: func(v *finance.OperationCreate) { v.Applied.AmountMinor = "0100" }},
		{name: "future", edit: func(v *finance.OperationCreate) { v.Date = "9999-01-01" }},
		{name: "unconfirmed", edit: func(v *finance.OperationCreate) { v.Confirm = false }},
		{name: "same currency transfer mismatch", edit: func(v *finance.OperationCreate) {
			v.Kind, v.Received = "transfer", &finance.AppliedAmount{AccountID: b, AmountMinor: "99"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := operationInput("expense", a, "100")
			tc.edit(&v)
			f.request[httpapi.Problem](t, "POST", base+"/operations", u.Token, uuid.New().String(), v, 422)
		})
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("invalid commands left financial facts")
	}
	for range 3 {
		v := operationInput("transfer", a, "100")
		v.Received = &finance.AppliedAmount{AccountID: b, AmountMinor: "100"}
		f.request[financeReference](t, "POST", base+"/operations", u.Token, uuid.New().String(), v, 200)
	}
	seen := map[string]bool{}
	cursor := ""
	for range 3 {
		page := f.request[access.Page[finance.AccountHistoryEntry]](t, "GET", base+"/accounts/"+a+"/history?limit=1&cursor="+cursor, u.Token, "", nil, 200)
		if len(page.Items) != 1 || seen[page.Items[0].OperationID] {
			t.Fatal("unstable history pagination")
		}
		seen[page.Items[0].OperationID] = true
		cursor = page.NextCursor
	}
	if cursor != "" {
		t.Fatal("cursor continued beyond the authorized history")
	}
	f.request[httpapi.Problem](t, "GET", base+"/accounts/"+a+"/history?cursor=malformed", u.Token, "", nil, 422)
	f.assertFinancialBalance(t, u, u.SpaceID, a, "-300")
	f.assertFinancialBalance(t, u, u.SpaceID, b, "300")
}

func TestFinancialOperationLostResponse(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "operations-lost@example.test")
	a := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "0", "confirmed", "private")
	b := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "10000", "confirmed", "private")
	v := operationInput("transfer", b, "500")
	v.Received = &finance.AppliedAmount{AccountID: a, AmountMinor: "500"}
	var drop atomic.Bool
	drop.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.server.Config.Handler.ServeHTTP(&discardResponse{ResponseWriter: w, Drop: &drop}, r)
	}))
	defer server.Close()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/spaces/" + u.SpaceID + "/operations"
	req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+base, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+u.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "lost-operation")
	conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := req.Write(conn); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	if n, err := conn.Read(one[:]); n != 0 || !errors.Is(err, io.EOF) {
		t.Fatal("response was not lost after commit", n, err)
	}
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 {
		t.Fatal("operation did not commit before the response was lost")
	}
	restarted, err := access.New(f.store, f.policy, finance.SeedDefaults)
	if err != nil {
		t.Fatal(err)
	}
	id, err := finance.New(restarted).CreateOperation(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "lost-operation"}, v)
	if err != nil || id == "" {
		t.Fatal("lost response did not recover after service replacement", err)
	}
	f.request[finance.OperationView](t, "GET", base+"/"+id, u.Token, "", nil, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, a, "500")
	f.assertFinancialBalance(t, u, u.SpaceID, b, "9500")
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 || f.count(t, "SELECT count(*) FROM position_effects") != 2 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='lost-operation'") != 1 {
		t.Fatal("replay repeated committed effects")
	}
}
