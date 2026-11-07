//go:build integration

package access_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

type financeReference struct {
	ID string `json:"id"`
}

func accountInput(visibility string) finance.AccountCreate {
	return finance.AccountCreate{Name: "Synthetic account", Kind: "bank", Currency: "PEN", Opening: finance.OpeningReference{Date: "2026-11-01", AmountMinor: new("-12000"), Quality: "confirmed"}, Sharing: finance.Sharing{Visibility: visibility, SpacePermission: "operate", Grants: []finance.Grant{}}}
}

func TestFinancialAccountsPrivacyAndLifecycle(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "owner-finance@example.test")
	ownerBase := "/v1/spaces/" + owner.SpaceID + "/accounts"
	proposal := f.request[finance.AccountProposal](t, "GET", ownerBase+"/proposal", owner.Token, "", nil, 200)
	if proposal.Visibility != "private" || !proposal.RequiresExplicitConfirmation {
		t.Fatal("unexpected individual proposal")
	}
	preexisting := f.request[financeReference](t, "POST", ownerBase, owner.Token, "before-invitation", accountInput("private"), 200).ID
	member := f.newUser(t, "member-finance@example.test")
	reader := f.newUser(t, "reader-finance@example.test")
	memberID := f.join(t, owner, member, "member")
	readerID := f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID + "/accounts"
	proposal = f.request[finance.AccountProposal](t, "GET", base+"/proposal", owner.Token, "", nil, 200)
	if proposal.Visibility != "space" || !proposal.ExistingAccountsUnchanged {
		t.Fatal("shared proposal ignored current context")
	}
	f.request[httpapi.Problem](t, "GET", base+"/"+preexisting, member.Token, "", nil, 404)
	private := f.request[financeReference](t, "POST", base, member.Token, "private-create", accountInput("private"), 200).ID
	shared := f.request[financeReference](t, "POST", base, member.Token, "shared-create", accountInput("space"), 200).ID
	initial := f.request[finance.AccountView](t, "GET", base+"/"+private, member.Token, "", nil, 200)
	if initial.BalanceMinor == nil || *initial.BalanceMinor != "-12000" || initial.Position != "liquid" || !initial.CanAdminister {
		t.Fatalf("invalid initial position: %+v", initial)
	}
	f.request[httpapi.Problem](t, "GET", base+"/"+private, owner.Token, "", nil, 404)
	page := f.request[access.Page[finance.AccountView]](t, "GET", base+"?limit=1", owner.Token, "", nil, 200)
	if len(page.Items) != 1 || (page.Items[0].ID != shared && page.Items[0].ID != preexisting) || page.NextCursor == "" {
		t.Fatal("private account affected pagination")
	}
	second := f.request[access.Page[finance.AccountView]](t, "GET", base+"?limit=1&cursor="+page.NextCursor, owner.Token, "", nil, 200)
	if len(second.Items) != 1 || second.Items[0].ID == page.Items[0].ID || second.NextCursor != "" {
		t.Fatal("authorized cursor pagination mismatch")
	}
	v := f.request[finance.AccountView](t, "GET", base+"/"+shared, reader.Token, "", nil, 200)
	if v.CanOperate || v.CanAdminister {
		t.Fatal("reader gained financial write permission")
	}
	f.request[httpapi.Problem](t, "POST", base, reader.Token, "reader-create", accountInput("space"), 403)
	f.request[httpapi.Problem](t, "PUT", base+"/"+shared, owner.Token, "owner-edit", finance.AccountEdit{Name: "Not allowed", State: "active", Version: 1}, 403)
	// A selected account grants only the explicitly named membership episode.
	selected := accountInput("selected")
	selected.Sharing.Grants = []finance.Grant{{MemberID: owner.MemberID, Permission: "read"}}
	selectedID := f.request[financeReference](t, "POST", base, member.Token, "selected-create", selected, 200).ID
	f.request[finance.AccountView](t, "GET", base+"/"+selectedID, owner.Token, "", nil, 200)
	f.request[httpapi.Problem](t, "GET", base+"/"+selectedID, reader.Token, "", nil, 404)
	selected.Sharing.Grants = []finance.Grant{{MemberID: readerID, Permission: "operate"}}
	f.request[httpapi.Problem](t, "POST", base, member.Token, "reader-grant", selected, 422)
	// Removing a responsible member preserves all original amounts and grants;
	// the ended responsibility immobilizes accounts without exposing private ones.
	f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/members/"+memberID+"/remove", owner.Token, "remove-responsible", map[string]any{"confirm": true}, 200)
	f.request[httpapi.Problem](t, "GET", base+"/"+private, owner.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "GET", base+"/"+private, member.Token, "", nil, 404)
	v = f.request[finance.AccountView](t, "GET", base+"/"+shared, owner.Token, "", nil, 200)
	if v.State != "immobilized" || v.CanOperate || v.BalanceMinor == nil || *v.BalanceMinor != "-12000" {
		t.Fatal("departure changed position or left it operable")
	}
	newMember := f.join(t, owner, member, "member")
	if newMember == memberID {
		t.Fatal("reused membership identity")
	}
	f.request[httpapi.Problem](t, "GET", base+"/"+private, member.Token, "", nil, 404)
	recovery := f.request[access.Page[finance.RecoveryReference]](t, "GET", base+"/recoverable", member.Token, "", nil, 200)
	if len(recovery.Items) != 3 {
		t.Fatalf("recoverable count=%d", len(recovery.Items))
	}
	f.request[map[string]any](t, "POST", base+"/"+private+"/recovery", member.Token, "recover-private", map[string]any{"confirm": true, "version": 1}, 200)
	f.request[map[string]any](t, "POST", base+"/"+private+"/recovery", member.Token, "recover-private", map[string]any{"confirm": true, "version": 1}, 200)
	v = f.request[finance.AccountView](t, "GET", base+"/"+private, member.Token, "", nil, 200)
	if v.ResponsibleMember != newMember || v.State != "active" || *v.BalanceMinor != "-12000" {
		t.Fatal("explicit recovery corrupted responsibility or balance")
	}
	f.request[httpapi.Problem](t, "GET", base+"/"+private, owner.Token, "", nil, 404)
	// Membership in another space never authorizes a reference in this one.
	f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+member.SpaceID+"/accounts/"+private, member.Token, "", nil, 404)
	if got := f.count(t, "SELECT count(*) FROM financial_accounts WHERE space_id=$1", owner.SpaceID); got != 4 {
		t.Fatal("departure lost accounts")
	}
}

func TestFinancialAccountsCommandsAndConcurrency(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "concurrent-finance@example.test")
	base := "/v1/spaces/" + u.SpaceID + "/accounts"
	s := finance.New(f.service, f.store)
	v := accountInput("private")
	var wg sync.WaitGroup
	results := make(chan string, 8)
	errorsFound := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			id, err := s.CreateAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "same-create"}, v)
			results <- id
			errorsFound <- err
		})
	}
	wg.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	for got := range results {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("concurrent retry duplicated position")
		}
	}
	if f.count(t, "SELECT count(*) FROM financial_accounts WHERE space_id=$1", u.SpaceID) != 1 {
		t.Fatal("duplicate account")
	}
	v.Name = "Different content"
	if _, err := s.CreateAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "same-create"}, v); !errors.Is(err, execution.ErrConflict) {
		t.Fatalf("content conflict=%v", err)
	}
	unknown := accountInput("private")
	unknown.Opening.Quality = "unknown"
	unknown.Opening.AmountMinor = nil
	unknownID := f.request[financeReference](t, "POST", base, u.Token, "unknown", unknown, 200).ID
	got := f.request[finance.AccountView](t, "GET", base+"/"+unknownID, u.Token, "", nil, 200)
	if got.BalanceMinor != nil || got.Opening.AmountMinor != nil {
		t.Fatal("unknown became zero")
	}
	f.request[map[string]any](t, "PUT", base+"/"+id, u.Token, "archive", finance.AccountEdit{Name: "Archived", State: "archived", Version: 1}, 200)
	f.request[httpapi.Problem](t, "PUT", base+"/"+id, u.Token, "stale-edit", finance.AccountEdit{Name: "Overwrite", State: "active", Version: 1}, 409)
	got = f.request[finance.AccountView](t, "GET", base+"/"+id, u.Token, "", nil, 200)
	if got.State != "archived" || got.CanOperate || *got.BalanceMinor != "-12000" {
		t.Fatal("archive lost historical position")
	}
	// There is deliberately no generic currency/opening/balance mutation field.
	f.request[httpapi.Problem](t, "PUT", base+"/"+id, u.Token, "currency-relabel", map[string]any{"name": "Bad", "state": "active", "version": 2, "currency": "USD"}, 422)
	if _, err := f.db.Pool.Exec(t.Context(), "UPDATE space_capabilities SET ceiling=0 WHERE space_id=$1 AND capability='finance.write'", u.SpaceID); err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "POST", base, u.Token, "missing-capability", accountInput("private"), 403)
	got = f.request[finance.AccountView](t, "GET", base+"/"+unknownID, u.Token, "", nil, 200)
	if got.CanOperate || got.CanAdminister {
		t.Fatal("effective permission flags ignored disabled capability")
	}
	// Confirmed references are not economic operations and never create an outbox job.
	if f.count(t, "SELECT count(*) FROM action_receipts WHERE scope=$1 AND action='account.create'", u.SpaceID) != 2 {
		t.Fatal("unexpected confirmed action count")
	}
}

func TestFinancialOrganizationAndExchange(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "organization@example.test")
	other := f.newUser(t, "other-organization@example.test")
	base := "/v1/spaces/" + u.SpaceID
	catalog := base + "/catalog"
	defaults := f.request[access.Page[finance.CatalogView]](t, "GET", catalog+"?kind=category", u.Token, "", nil, 200)
	if len(defaults.Items) != 7 {
		t.Fatal("new space missing default categories")
	}
	for _, v := range defaults.Items {
		if v.DefaultKey == "" {
			t.Fatal("default identity depends on label")
		}
	}
	create := func(kind string, v finance.CatalogValue) string {
		return f.request[financeReference](t, "POST", catalog, u.Token, uuid.New().String(), map[string]any{"kind": kind, "value": v}, 200).ID
	}
	parent := create("category", finance.CatalogValue{Name: "Parent", Direction: "expense", Icon: "home", Color: "#123456"})
	child := create("category", finance.CatalogValue{Name: "Child", Direction: "expense", Icon: "home", Color: "#123456", ParentID: parent})
	f.request[httpapi.Problem](t, "POST", catalog, u.Token, "third-level", map[string]any{"kind": "category", "value": finance.CatalogValue{Name: "Third", Direction: "expense", Icon: "home", Color: "#123456", ParentID: child}}, 422)
	foreign := f.request[access.Page[finance.CatalogView]](t, "GET", "/v1/spaces/"+other.SpaceID+"/catalog?kind=category", other.Token, "", nil, 200).Items[0].ID
	f.request[httpapi.Problem](t, "POST", catalog, u.Token, "foreign-parent", map[string]any{"kind": "category", "value": finance.CatalogValue{Name: "Foreign child", Direction: "expense", Icon: "home", Color: "#123456", ParentID: foreign}}, 404)
	target := create("category", finance.CatalogValue{Name: "Target", Direction: "expense", Icon: "home", Color: "#654321"})
	f.request[map[string]any](t, "POST", catalog+"/"+parent+"/merge", u.Token, "merge-parent", finance.CatalogMerge{TargetID: target, Version: 1, TargetVersion: 1, Confirm: true}, 200)
	f.request[map[string]any](t, "POST", catalog+"/"+parent+"/merge", u.Token, "merge-parent", finance.CatalogMerge{TargetID: target, Version: 1, TargetVersion: 1, Confirm: true}, 200)
	old := f.request[finance.CatalogView](t, "GET", catalog+"/"+parent, u.Token, "", nil, 200)
	if old.MergedInto != target || !old.Archived {
		t.Fatal("merge destroyed identity")
	}
	childView := f.request[finance.CatalogView](t, "GET", catalog+"/"+child, u.Token, "", nil, 200)
	if childView.Value.ParentID != target {
		t.Fatal("merge orphaned child")
	}
	if f.count(t, "SELECT count(*) FROM catalog_configuration_history WHERE entry_id=$1 AND parent_id=$2", child, parent) != 1 {
		t.Fatal("category reparenting lost its prior classification")
	}
	for _, kind := range []string{"merchant", "counterparty", "tag"} {
		a := create(kind, finance.CatalogValue{Name: "First " + kind})
		b := create(kind, finance.CatalogValue{Name: "Second " + kind})
		f.request[map[string]any](t, "POST", catalog+"/"+a+"/merge", u.Token, "merge-"+kind, finance.CatalogMerge{TargetID: b, Version: 1, TargetVersion: 1, Confirm: true}, 200)
		if f.count(t, "SELECT count(*) FROM financial_catalog WHERE id IN ($1,$2)", a, b) != 2 {
			t.Fatal("merge deleted referenced rows")
		}
	}
	exchange := finance.ExchangeCreate{FromCurrency: "USD", ToCurrency: "PEN", Rate: "3.75", Date: "2026-11-01", Context: "historical", Source: "Manual synthetic reference"}
	rate := f.request[financeReference](t, "POST", base+"/exchange-references", u.Token, "rate-create", exchange, 200).ID
	conversion := finance.ExchangeConversion{AmountMinor: "100", Currency: "USD", TargetCurrency: "PEN", ReferenceID: rate, Date: exchange.Date, Context: "historical"}
	converted := f.request[finance.ConversionView](t, "POST", base+"/exchange-conversions", u.Token, "", conversion, 200)
	if !converted.Complete || converted.AmountMinor == nil || *converted.AmountMinor != "375" {
		t.Fatal("incorrect minor-unit conversion")
	}
	conversion.ReferenceID = ""
	converted = f.request[finance.ConversionView](t, "POST", base+"/exchange-conversions", u.Token, "", conversion, 200)
	if converted.Complete || converted.AmountMinor != nil || converted.Reason != "missing_exchange_reference" {
		t.Fatal("missing rate became 1:1")
	}
	exchange.Replaces = rate
	exchange.Rate = "3.80"
	f.request[map[string]any](t, "POST", base+"/exchange-references", u.Token, "rate-correction", exchange, 200)
	original := f.request[finance.ExchangeView](t, "GET", base+"/exchange-references/"+rate, u.Token, "", nil, 200)
	if original.Reference.Rate != "3.75" {
		t.Fatal("correction rewrote provenance")
	}
	conversion.ReferenceID = rate
	conversion.Date = "2026-11-02"
	f.request[httpapi.Problem](t, "POST", base+"/exchange-conversions", u.Token, "", conversion, 422)
	conversion.Date = "2026-11-01"
	conversion.Context = "current"
	f.request[httpapi.Problem](t, "POST", base+"/exchange-conversions", u.Token, "", conversion, 422)
	badRate := exchange
	badRate.Rate = "0"
	badRate.Replaces = ""
	f.request[httpapi.Problem](t, "POST", base+"/exchange-references", u.Token, "zero-rate", badRate, 422)
	// Changing presentation base does not relabel an existing position or rewrite rates.
	accountID := f.request[financeReference](t, "POST", base+"/accounts", u.Token, "presentation-account", accountInput("private"), 200).ID
	space := f.request[access.SpaceView](t, "GET", base, u.Token, "", nil, 200)
	space.Settings.Currency = "USD"
	f.request[map[string]any](t, "PUT", base, u.Token, "change-base", map[string]any{"settings": space.Settings, "version": space.Version}, 200)
	account := f.request[finance.AccountView](t, "GET", base+"/accounts/"+accountID, u.Token, "", nil, 200)
	if account.Currency != "PEN" || account.BalanceMinor == nil || *account.BalanceMinor != "-12000" {
		t.Fatal("base presentation changed original position")
	}
	f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+other.SpaceID+"/exchange-references/"+rate, other.Token, "", nil, 404)
	// Runtime role cannot tamper with immutable rates or merge history.
	if _, err := f.store.DB.Pool.Exec(t.Context(), "UPDATE exchange_references SET rate='1' WHERE id=$1", rate); err == nil {
		t.Fatal("runtime can rewrite rate history")
	}
	// Future operations resolve catalog aliases inside their own transaction.
	if err := f.store.DB.Transaction(t.Context(), pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		a, err := f.service.AuthenticateCustomer(ctx, tx, "Bearer "+u.Token)
		if err != nil {
			return err
		}
		if _, err := f.service.AuthorizeSpace(ctx, tx, a, u.SpaceID, "read"); err != nil {
			return err
		}
		resolved, err := finance.ResolveCatalog(ctx, tx, u.SpaceID, parent, "category", true)
		if err != nil {
			return err
		}
		if resolved.ID != target {
			return errors.New("unresolved merge")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
