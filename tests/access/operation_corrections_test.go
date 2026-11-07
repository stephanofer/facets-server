//go:build integration

package access_test

import (
	"bytes"
	"database/sql"
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

	"github.com/pressly/goose/v3"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"github.com/stephanofer/facets-server/migrations"
)

func TestOperationCorrectionsAndVoiding(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "corrections@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "10000", "confirmed", "private")
	other := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "0", "estimated", "private")
	v := operationInput("expense", bank, "1000")
	id := f.request[financeReference](t, "POST", base+"/operations", u.Token, "original", v, 200).ID
	path := base + "/operations/" + id
	v.Note = "Corrected description"
	v.Parts = []finance.CategoryPart{{AmountMinor: "600"}, {AmountMinor: "400"}}
	correction := finance.OperationCorrection{Version: 1, Reason: "Correct description and split", Replacement: v}
	preview := f.request[finance.OperationConsequences](t, "POST", path+"/correction-preview", u.Token, "", correction, 200)
	if preview.ChangeKind != "descriptive" || len(preview.Accounts) != 1 || *preview.Accounts[0].BeforeMinor != "9000" || *preview.Accounts[0].AfterMinor != "9000" {
		t.Fatal("descriptive preview moved money", preview)
	}
	if f.count(t, "SELECT count(*) FROM operation_revisions") != 1 {
		t.Fatal("preview persisted a revision")
	}
	f.request[financeReference](t, "PUT", path, u.Token, "descriptive", correction, 200)
	f.request[financeReference](t, "PUT", path, u.Token, "descriptive", correction, 200)
	current := f.request[finance.OperationView](t, "GET", path, u.Token, "", nil, 200)
	if current.Revision != 2 || current.ChangeKind != "descriptive" || current.Note != v.Note || len(current.Components) != 2 || current.RevisionAuthor != u.ID || current.Reason != correction.Reason {
		t.Fatal("missing revision evidence", current)
	}
	initial := f.request[finance.OperationView](t, "GET", path+"/revisions/1", u.Token, "", nil, 200)
	if initial.Note != "" || len(initial.Components) != 1 || initial.ChangeKind != "initial" || initial.Voided {
		t.Fatal("initial evidence was overwritten")
	}
	f.request[httpapi.Problem](t, "PUT", path, u.Token, "stale", correction, 409)
	correction.Replacement.Note = "Different content"
	f.request[httpapi.Problem](t, "PUT", path, u.Token, "descriptive", correction, 409)
	correction.Version, correction.Replacement.Applied = 2, finance.AppliedAmount{AccountID: other, AmountMinor: "3000"}
	correction.Replacement.Parts = []finance.CategoryPart{}
	correction.Replacement.Date = "2026-11-02"
	preview = f.request[finance.OperationConsequences](t, "POST", path+"/correction-preview", u.Token, "", correction, 200)
	if preview.ChangeKind != "financial" || len(preview.Accounts) != 2 || preview.AfterDate != "2026-11-02" {
		t.Fatal("financial preview omitted consequences")
	}
	f.request[financeReference](t, "PUT", path, u.Token, "financial", correction, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "10000")
	f.assertFinancialBalance(t, u, u.SpaceID, other, "-3000")
	current = f.request[finance.OperationView](t, "GET", path, u.Token, "", nil, 200)
	if current.Revision != 3 || current.ChangeKind != "financial" || current.Date != "2026-11-02" || current.Effects[0].AccountID != other {
		t.Fatal("financial revision lost effective fact")
	}
	// Moving the fact before tracking removes its contribution, not its evidence.
	correction.Version, correction.Replacement.Date = 3, "2026-10-31"
	f.request[financeReference](t, "PUT", path, u.Token, "antecedent", correction, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, other, "0")
	correction.Version, correction.Replacement.Date = 4, "2026-11-01"
	f.request[financeReference](t, "PUT", path, u.Token, "tracked-again", correction, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, other, "-3000")
	void := finance.OperationVoid{FinancialVersionConfirmation: finance.FinancialVersionConfirmation{Version: 5, Confirm: true}, Reason: "Duplicate capture confirmed by user"}
	f.request[financeReference](t, "POST", path+"/void", u.Token, "void", void, 200)
	f.request[financeReference](t, "POST", path+"/void", u.Token, "void", void, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, other, "0")
	current = f.request[finance.OperationView](t, "GET", path, u.Token, "", nil, 200)
	if current.Revision != 6 || !current.Voided || current.ChangeKind != "void" || len(current.Effects) != 1 || len(current.Components) != 1 {
		t.Fatal("void erased its evidence")
	}
	history := f.request[access.Page[finance.AccountHistoryEntry]](t, "GET", base+"/accounts/"+other+"/history", u.Token, "", nil, 200)
	if len(history.Items) != 0 {
		t.Fatal("voided effect still contributes to account history")
	}
	page := f.request[access.Page[finance.OperationRevisionSummary]](t, "GET", path+"/revisions?limit=2", u.Token, "", nil, 200)
	if len(page.Items) != 2 || page.Items[0].Revision != 1 || page.NextCursor != "2" {
		t.Fatal("unstable revision page", page)
	}
	page = f.request[access.Page[finance.OperationRevisionSummary]](t, "GET", path+"/revisions?limit=2&cursor="+page.NextCursor, u.Token, "", nil, 200)
	if len(page.Items) != 2 || page.Items[0].Revision != 3 || page.NextCursor != "4" {
		t.Fatal("revision cursor repeated or skipped evidence")
	}
	void.Version = 6
	f.request[httpapi.Problem](t, "POST", path+"/void", u.Token, "void-again", void, 422)
	correction.Version = 6
	f.request[httpapi.Problem](t, "PUT", path, u.Token, "correct-void", correction, 422)
	f.request[httpapi.Problem](t, "GET", path+"/revisions/999", u.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "GET", path+"/revisions?cursor=-1", u.Token, "", nil, 422)
	// Creation replay still resolves the original identity after voiding.
	f.request[financeReference](t, "POST", base+"/operations", u.Token, "original", operationInput("expense", bank, "1000"), 200)
	if f.count(t, "SELECT count(*) FROM financial_operations") != 1 || f.count(t, "SELECT count(*) FROM operation_revisions") != 6 {
		t.Fatal("replay recreated canceled effects")
	}
}

func TestOperationCorrectionAtomicityAndConcurrency(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "correction-concurrency@example.test")
	base := "/v1/spaces/" + u.SpaceID
	a := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "10000", "confirmed", "private")
	b := f.financialAccount(t, u, u.SpaceID, "bank", "USD", "0", "confirmed", "private")
	catalog := f.request[access.Page[finance.CatalogView]](t, "GET", base+"/catalog?kind=category", u.Token, "", nil, 200)
	var costs string
	for _, c := range catalog.Items {
		if c.DefaultKey == "financial_costs" {
			costs = c.ID
		}
	}
	v := operationInput("transfer", a, "1000")
	v.Received = &finance.AppliedAmount{AccountID: b, AmountMinor: "250"}
	v.Fees = []finance.OperationFee{{AccountID: a, AmountMinor: "10", CategoryID: costs}}
	id := f.request[financeReference](t, "POST", base+"/operations", u.Token, "transfer", v, 200).ID
	path := base + "/operations/" + id
	v.Applied.AmountMinor, v.Received.AmountMinor, v.Fees[0].AmountMinor = "2000", "500", "20"
	correction := finance.OperationCorrection{Version: 1, Reason: "Correct both principal legs and fee", Replacement: v}
	s := finance.New(f.service)
	command := access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "rollback"}
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CorrectOperation(t.Context(), command, id, correction); err == nil {
		t.Fatal("audit failure committed correction")
	}
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, a, "8990")
	f.assertFinancialBalance(t, u, u.SpaceID, b, "250")
	if f.count(t, "SELECT count(*) FROM operation_revisions") != 1 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='rollback'") != 0 {
		t.Fatal("partial correction survived rollback")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := s.CorrectOperation(t.Context(), access.SpaceCommand{Authorization: command.Authorization, SpaceID: u.SpaceID, Key: "same-correction"}, id, correction)
			errs <- err
		})
	}
	wg.Wait()
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	f.assertFinancialBalance(t, u, u.SpaceID, a, "7980")
	f.assertFinancialBalance(t, u, u.SpaceID, b, "500")
	if f.count(t, "SELECT count(*) FROM operation_revisions") != 2 {
		t.Fatal("concurrent replay duplicated revisions")
	}
	correction.Version = 2
	errs = make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			v := correction
			v.Replacement.Note = strconv.Itoa(i)
			_, err := s.CorrectOperation(t.Context(), access.SpaceCommand{Authorization: command.Authorization, SpaceID: u.SpaceID, Key: "competing-" + strconv.Itoa(i)}, id, v)
			errs <- err
		})
	}
	wg.Wait()
	success, conflicts := 0, 0
	for range 2 {
		err := <-errs
		if err == nil {
			success++
		} else if errors.Is(err, execution.ErrVersion) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal("lost update was not rejected", success, conflicts)
	}
	void := finance.OperationVoid{Version: 3, Confirm: true, Reason: "Void complete transfer"}
	f.request[financeReference](t, "POST", path+"/void", u.Token, "void-transfer", void, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, a, "10000")
	f.assertFinancialBalance(t, u, u.SpaceID, b, "0")
	// A valid final balance must not fail due to the order of intermediate arithmetic.
	maximum := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "9223372036854775807", "confirmed", "private")
	one := operationInput("expense", maximum, "1")
	oneID := f.request[financeReference](t, "POST", base+"/operations", u.Token, uuid.New().String(), one, 200).ID
	otherIncome := operationInput("income", maximum, "1")
	f.request[financeReference](t, "POST", base+"/operations", u.Token, uuid.New().String(), otherIncome, 200)
	one.Note = "Only note changed at int64 ceiling"
	f.request[financeReference](t, "PUT", base+"/operations/"+oneID, u.Token, "ceiling-description", finance.OperationCorrection{Version: 1, Reason: "Description", Replacement: one}, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, maximum, "9223372036854775807")
	f.request[httpapi.Problem](t, "POST", base+"/operations/"+oneID+"/void", u.Token, "overflow-void", finance.OperationVoid{Version: 2, Confirm: true, Reason: "Would overflow"}, 422)
	f.assertFinancialBalance(t, u, u.SpaceID, maximum, "9223372036854775807")
	// The destination's principal and its fee have a valid combined result even
	// at the ceiling. Creation must use the same final-range rule as corrections.
	ceilingTransfer := operationInput("transfer", a, "1")
	ceilingTransfer.Received = &finance.AppliedAmount{AccountID: maximum, AmountMinor: "1"}
	ceilingTransfer.Fees = []finance.OperationFee{{AccountID: maximum, AmountMinor: "1", CategoryID: costs}}
	f.request[financeReference](t, "POST", base+"/operations", u.Token, "ceiling-principal-and-fee", ceilingTransfer, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, maximum, "9223372036854775807")
	f.assertFinancialBalance(t, u, u.SpaceID, a, "9999")
}

func TestOperationRevisionPrivacy(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "revision-owner@example.test")
	member := f.newUser(t, "revision-member@example.test")
	reader := f.newUser(t, "revision-reader@example.test")
	f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	private := f.financialAccount(t, member, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	shared := f.financialAccount(t, member, owner.SpaceID, "bank", "PEN", "1000", "confirmed", "space")
	v := operationInput("expense", private, "100")
	v.Note = "Private old description"
	id := f.request[financeReference](t, "POST", base+"/operations", member.Token, "private-original", v, 200).ID
	path := base + "/operations/" + id
	v.Applied.AccountID, v.Note = shared, "Shared correction"
	correction := finance.OperationCorrection{Version: 1, Reason: "Changed payment account", Replacement: v}
	f.request[httpapi.Problem](t, "PUT", path, owner.Token, "unauthorized-old-account", correction, 404)
	f.request[financeReference](t, "PUT", path, member.Token, "share-revision", correction, 200)
	f.request[finance.OperationView](t, "GET", path, owner.Token, "", nil, 200)
	f.request[httpapi.Problem](t, "GET", path+"/revisions/1", owner.Token, "", nil, 404)
	page := f.request[access.Page[finance.OperationRevisionSummary]](t, "GET", path+"/revisions?limit=1", owner.Token, "", nil, 200)
	if len(page.Items) != 1 || page.Items[0].Revision != 2 || page.NextCursor != "" {
		t.Fatal("private revision leaked through pagination", page)
	}
	correction.Version = 2
	f.request[httpapi.Problem](t, "PUT", path, reader.Token, "reader-correct", correction, 403)
	f.request[httpapi.Problem](t, "POST", path+"/void", reader.Token, "reader-void", finance.OperationVoid{Version: 2, Confirm: true, Reason: "Not permitted"}, 403)
	// Revoking current access also blocks old receipts, details and previews.
	f.request[financeReference](t, "PUT", base+"/accounts/"+shared+"/sharing", member.Token, "make-private", map[string]any{"version": 1, "confirm": true, "sharing": finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}}, 200)
	f.request[httpapi.Problem](t, "GET", path, owner.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "GET", path+"/revisions", owner.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", path+"/correction-preview", owner.Token, "", correction, 404)
	// Replaying a mutation still checks current read access to its result.
	correction.Version = 1
	f.request[httpapi.Problem](t, "PUT", path, owner.Token, "share-revision", correction, 404)
	f.assertFinancialBalance(t, member, owner.SpaceID, private, "1000")
	f.assertFinancialBalance(t, member, owner.SpaceID, shared, "900")
}

func TestOperationChangeLostResponse(t *testing.T) {
	for _, action := range []string{"correct", "void"} {
		t.Run(action, func(t *testing.T) {
			f := setup(t)
			u := f.newUser(t, "change-lost@example.test")
			a := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
			original := operationInput("expense", a, "100")
			base := "/v1/spaces/" + u.SpaceID + "/operations"
			id := f.request[financeReference](t, "POST", base, u.Token, "create", original, 200).ID
			correction := finance.OperationCorrection{Version: 1, Reason: "Correct capture", Replacement: operationInput("expense", a, "200")}
			void := finance.OperationVoid{Version: 1, Confirm: true, Reason: "Cancel capture"}
			var payload any = correction
			method, path, balance := "PUT", base+"/"+id, "800"
			if action == "void" {
				payload, method, path, balance = void, "POST", path+"/void", "1000"
			}
			var drop atomic.Bool
			drop.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.server.Config.Handler.ServeHTTP(&discardResponse{ResponseWriter: w, Drop: &drop}, r)
			}))
			defer server.Close()
			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+u.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "lost-change")
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
				t.Fatal("response was not lost", n, err)
			}
			restarted, err := access.New(f.store, f.policy, finance.SeedDefaults)
			if err != nil {
				t.Fatal(err)
			}
			s := finance.New(restarted)
			command := access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "lost-change"}
			var recovered string
			if action == "void" {
				recovered, err = s.VoidOperation(t.Context(), command, id, void)
			} else {
				recovered, err = s.CorrectOperation(t.Context(), command, id, correction)
			}
			if err != nil || recovered != id {
				t.Fatal("committed action was not recovered", err)
			}
			f.assertFinancialBalance(t, u, u.SpaceID, a, balance)
			if f.count(t, "SELECT count(*) FROM operation_revisions") != 2 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='lost-change'") != 1 {
				t.Fatal("recovery applied effects twice")
			}
		})
	}
}

func TestOperationCorrectionsMigration(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "operation-upgrade@example.test")
	a := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	base := "/v1/spaces/" + u.SpaceID + "/operations"
	original := operationInput("expense", a, "100")
	id := f.request[financeReference](t, "POST", base, u.Token, "before-upgrade", original, 200).ID
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
	if _, err := provider.DownTo(t.Context(), 6); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Ready(t.Context()); err == nil {
		t.Fatal("version 6 was incorrectly ready for correction code")
	}
	if err := app.Migrate(t.Context(), dsn); err != nil {
		t.Fatal(err)
	}
	// DownTo removes the newly introduced tables, so explicitly refresh their
	// runtime grants just as local provisioning does after an upgrade.
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT SELECT,INSERT ON operation_refunds,account_reconciliations,reconciliation_reviews,balance_consistency_checks TO access_app"); err != nil {
		t.Fatal(err)
	}
	v := f.request[finance.OperationView](t, "GET", base+"/"+id, u.Token, "", nil, 200)
	if v.Revision != 1 || v.ChangeKind != "initial" || v.Voided || v.Effects[0].AmountMinor != "-100" {
		t.Fatal("upgrade changed preexisting facts")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, a, "900")
	f.request[financeReference](t, "POST", base, u.Token, "before-upgrade", original, 200)
	if f.count(t, "SELECT count(*) FROM operation_revisions") != 1 {
		t.Fatal("upgrade duplicated action")
	}
}

func TestOperationCorrectionRetainedReferences(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "correction-references@example.test")
	base := "/v1/spaces/" + u.SpaceID
	a := f.financialAccount(t, u, u.SpaceID, "bank", "USD", "1000", "confirmed", "private")
	category := finance.CatalogValue{Name: "Synthetic category", Direction: "expense", Icon: "receipt", Color: "#123456"}
	id := f.request[financeReference](t, "POST", base+"/catalog", u.Token, "category", map[string]any{"kind": "category", "value": category}, 200).ID
	rate := f.request[financeReference](t, "POST", base+"/exchange-references", u.Token, "rate", finance.ExchangeCreate{FromCurrency: "USD", ToCurrency: "PEN", Rate: "3.8", Date: "2026-11-01", Context: "historical", Source: "Synthetic rate"}, 200).ID
	v := operationInput("expense", a, "100")
	v.ExchangeReferenceID = rate
	v.Parts = []finance.CategoryPart{{AmountMinor: "100", CategoryID: id}}
	operation := f.request[financeReference](t, "POST", base+"/operations", u.Token, "expense", v, 200).ID
	f.request[financeReference](t, "PUT", base+"/catalog/"+id, u.Token, "archive-category", map[string]any{"value": category, "version": 1, "archived": true}, 200)
	space := f.request[access.SpaceView](t, "GET", base, u.Token, "", nil, 200)
	space.Settings.Currency = "EUR"
	f.request[map[string]any](t, "PUT", base, u.Token, "change-base", map[string]any{"settings": space.Settings, "version": space.Version}, 200)
	v.Note = "Description corrected without replacing exchange provenance"
	correction := finance.OperationCorrection{Version: 1, Reason: "Description only", Replacement: v}
	f.request[financeReference](t, "PUT", base+"/operations/"+operation, u.Token, "retain-provenance", correction, 200)
	current := f.request[finance.OperationView](t, "GET", base+"/operations/"+operation, u.Token, "", nil, 200)
	if current.ChangeKind != "descriptive" || current.Components[0].ExchangeReferenceID != rate || current.Components[0].CategoryID != id {
		t.Fatal("description correction replaced archived classification or original conversion context")
	}
	f.assertFinancialBalance(t, u, u.SpaceID, a, "900")
	// Retention does not authorize using that archived category in a new fact.
	f.request[httpapi.Problem](t, "POST", base+"/operations", u.Token, "new-use-archived", v, 422)
	correction.Version = 2
	correction.Replacement.Date = "2026-11-02"
	f.request[httpapi.Problem](t, "PUT", base+"/operations/"+operation, u.Token, "wrong-retained-date", correction, 422)
	// Runtime may advance the current pointer, never rewrite historical evidence.
	if _, err := f.store.DB.Pool.Exec(t.Context(), "UPDATE operation_revisions SET note='tampered' WHERE operation_id=$1", operation); err == nil {
		t.Fatal("runtime can rewrite revisions")
	}
	if _, err := f.store.DB.Pool.Exec(t.Context(), "DELETE FROM position_effects WHERE operation_id=$1", operation); err == nil {
		t.Fatal("runtime can delete effects")
	}
	if _, err := f.store.DB.Pool.Exec(t.Context(), "UPDATE financial_operations SET kind='income' WHERE id=$1", operation); err == nil {
		t.Fatal("column grant permits changing operation identity")
	}
	unknown := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "", "unknown", "private")
	uop := f.request[financeReference](t, "POST", base+"/operations", u.Token, "unknown", operationInput("income", unknown, "100"), 200).ID
	f.request[financeReference](t, "PUT", base+"/operations/"+uop, u.Token, "unknown-correct", finance.OperationCorrection{Version: 1, Reason: "Correct amount", Replacement: operationInput("income", unknown, "200")}, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, unknown, "unknown")
	f.request[financeReference](t, "POST", base+"/operations/"+uop+"/void", u.Token, "unknown-void", finance.OperationVoid{Version: 2, Confirm: true, Reason: "Cancel capture"}, 200)
	f.assertFinancialBalance(t, u, u.SpaceID, unknown, "unknown")
}
