//go:build integration

package access_test

import (
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/calendar"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

func seriesInput(account string) finance.SeriesCreate {
	return finance.SeriesCreate{AccountID: account, Direction: "payment", Confirm: true, Name: "Synthetic recurring rent", Amount: finance.DueAmount{Certainty: "confirmed", AmountMinor: new("100")}, Recurrence: calendar.Recurrence{Start: "2026-11-01", Frequency: "monthly"}}
}

func TestSeriesGenerationAndLifecycle(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "series@example.test")
	base := "/v1/spaces/" + u.SpaceID
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	v := seriesInput(bank)
	v.Recurrence = calendar.Recurrence{Start: "2026-11-01", Frequency: "twice_monthly", FirstDay: 30, SecondDay: 31}
	id := f.request[financeReference](t, "POST", base+"/series", u.Token, "series", v, 200).ID
	if replay := f.request[financeReference](t, "POST", base+"/series", u.Token, "series", v, 200).ID; replay != id {
		t.Fatal("series retry duplicated")
	}
	preview := f.request[[]calendar.Occurrence](t, "POST", base+"/series/preview", u.Token, "", finance.SeriesPreview{Recurrence: v.Recurrence, From: "2026-11-01", Until: "2026-12-01"}, 200)
	if len(preview) != 2 || preview[0].Date != "2026-11-30" || preview[1].Date != "2026-11-30" || preview[0].Ordinal == preview[1].Ordinal {
		t.Fatal("coincident slots lost", preview)
	}
	generate := finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2026-12-01"}
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", generate, 200)
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate", generate, 200)
	// Even a different action key cannot recreate an already materialized horizon.
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "generate-again", generate, 200)
	page := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues", u.Token, "", nil, 200)
	if len(page.Items) != 2 || page.Items[0].SeriesID != id || page.Items[0].SeriesVersion != 1 || page.Items[0].OccurrenceSlot == page.Items[1].OccurrenceSlot {
		t.Fatal("occurrences not backed by common dues", page)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "1000")
	if f.count(t, "SELECT count(*) FROM financial_operations") != 0 {
		t.Fatal("generation moved money")
	}
	// Apply and omit through the existing engine, not a second fulfillment model.
	paid, skipped := page.Items[0], page.Items[1]
	op := f.request[financeReference](t, "POST", base+"/operations", u.Token, "payment", operationInput("expense", bank, "100"), 200).ID
	f.request[financeReference](t, "POST", base+"/payment-applications", u.Token, "apply", paymentInput(op, paid.ID, "100", 1), 200)
	f.request[financeReference](t, "PUT", base+"/dues/"+skipped.ID, u.Token, "skip", finance.DueEdit{Name: skipped.Name, Date: skipped.Date, Amount: skipped.Amount, Version: 1, Confirm: true, State: "skipped", Reason: "Omit just this occurrence"}, 200)
	edit := finance.SeriesEdit{SeriesValue: v.SeriesValue, Version: 1, Confirm: true, EffectiveFrom: "2026-12-01", State: "paused", Reason: "Temporary pause"}
	f.request[financeReference](t, "PUT", base+"/series/"+id, u.Token, "pause", edit, 200)
	generate.Version, generate.Until = 2, "2027-01-01"
	f.request[httpapi.Problem](t, "POST", base+"/series/"+id+"/generate", u.Token, "paused-generate", generate, 422)
	edit.Version, edit.State, edit.EffectiveFrom, edit.Reason = 2, "active", "2027-01-01", "Resume at the new price"
	edit.Amount.AmountMinor = new("150")
	f.request[financeReference](t, "PUT", base+"/series/"+id, u.Token, "resume", edit, 200)
	generate.Version, generate.Until = 3, "2027-02-01"
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", u.Token, "resume-generate", generate, 200)
	old := f.request[finance.DueView](t, "GET", base+"/dues/"+paid.ID, u.Token, "", nil, 200)
	omitted := f.request[finance.DueView](t, "GET", base+"/dues/"+skipped.ID, u.Token, "", nil, 200)
	if old.Status != "paid" || *old.Amount.AmountMinor != "100" || omitted.Status != "skipped" {
		t.Fatal("series edit rewrote history", old, omitted)
	}
	jan := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?from=2027-01-01&until=2027-02-01", u.Token, "", nil, 200)
	if len(jan.Items) != 2 || *jan.Items[0].Amount.AmountMinor != "150" || jan.Items[0].SeriesVersion != 3 {
		t.Fatal("new conditions not applied", jan)
	}
	dec := f.request[access.Page[finance.DueView]](t, "GET", base+"/dues?from=2026-12-01&until=2027-01-01", u.Token, "", nil, 200)
	if len(dec.Items) != 0 {
		t.Fatal("resume backfilled paused interval")
	}
	edit.Version, edit.State, edit.EffectiveFrom, edit.Reason = 3, "cancelled", "2027-02-01", "Stop tracking; provider contract is unchanged"
	f.request[financeReference](t, "PUT", base+"/series/"+id, u.Token, "cancel", edit, 200)
	edit.Version, edit.State = 4, "active"
	f.request[httpapi.Problem](t, "PUT", base+"/series/"+id, u.Token, "resurrect", edit, 422)
	history := f.request[access.Page[finance.SeriesVersionView]](t, "GET", base+"/series/"+id+"/versions?limit=2", u.Token, "", nil, 200)
	if len(history.Items) != 2 || history.NextCursor != "2" || *history.Items[0].Amount.AmountMinor != "100" {
		t.Fatal("version evidence missing", history)
	}
	next := f.request[access.Page[finance.SeriesVersionView]](t, "GET", base+"/series/"+id+"/versions?cursor=2&limit=2", u.Token, "", nil, 200)
	if len(next.Items) != 2 || next.NextCursor != "" || next.Items[1].State != "cancelled" {
		t.Fatal("version pagination", next)
	}
	f.assertFinancialBalance(t, u, u.SpaceID, bank, "900")
}

func TestSeriesConcurrencyAndRollback(t *testing.T) {
	f := setup(t)
	u := f.newUser(t, "series-concurrency@example.test")
	bank := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
	s := finance.New(f.service, f.store)
	command := func(key string) access.SpaceCommand {
		return access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: key}
	}
	v := seriesInput(bank)
	v.Recurrence.Frequency = "weekly"
	id, err := s.CreateSeries(t.Context(), command("series"), v)
	if err != nil {
		t.Fatal(err)
	}
	generate := finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2027-11-01"}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, err := s.GenerateSeries(t.Context(), command(uuid.New().String()), id, generate)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, "SELECT count(*) FROM recurring_occurrences") != 53 || f.count(t, "SELECT count(*) FROM due_items") != 53 {
		t.Fatal("overlapping concurrent generation duplicated occurrences")
	}
	before, err := s.Series(t.Context(), "Bearer "+u.Token, u.SpaceID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
		t.Fatal(err)
	}
	generate.Until = "2027-12-01"
	_, failed := s.GenerateSeries(t.Context(), command("rollback"), id, generate)
	if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
		t.Fatal(err)
	}
	if failed == nil {
		t.Fatal("audit failure committed generation")
	}
	after, err := s.Series(t.Context(), "Bearer "+u.Token, u.SpaceID, id)
	if err != nil || after.GeneratedUntil != before.GeneratedUntil || f.count(t, "SELECT count(*) FROM due_items") != 53 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='rollback'") != 0 {
		t.Fatal("failed generation left partial progress", err)
	}
	// Retry the failed command after replacing the in-process service.
	s = finance.New(f.service, f.store)
	if _, err := s.GenerateSeries(t.Context(), command("rollback"), id, generate); err != nil {
		t.Fatal(err)
	}
	if f.count(t, "SELECT count(*) FROM due_items") != 57 {
		t.Fatal("recovery missed or duplicated occurrences")
	}
	edit := finance.SeriesEdit{SeriesValue: v.SeriesValue, Version: 1, Confirm: true, EffectiveFrom: "2027-12-01", State: "ended", Reason: "Completed schedule"}
	errs = make(chan error, 2)
	for range 2 {
		wg.Go(func() { _, err := s.EditSeries(t.Context(), command(uuid.New().String()), id, edit); errs <- err })
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
		t.Fatal("concurrent edit lost version protection", success, conflict)
	}
}

func TestSeriesPrivacyAndValidation(t *testing.T) {
	f := setup(t)
	owner := f.newUser(t, "series-owner@example.test")
	member := f.newUser(t, "series-member@example.test")
	reader := f.newUser(t, "series-reader@example.test")
	membership := f.join(t, owner, member, "member")
	f.join(t, owner, reader, "reader")
	base := "/v1/spaces/" + owner.SpaceID
	account := accountInput("selected")
	account.Sharing.Grants = []finance.Grant{{MemberID: membership, Permission: "operate"}}
	bank := f.request[financeReference](t, "POST", base+"/accounts", owner.Token, "account", account, 200).ID
	v := seriesInput(bank)
	id := f.request[financeReference](t, "POST", base+"/series", member.Token, "series", v, 200).ID
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, reader.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/series", reader.Token, "reader-series", v, 403)
	gen := finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2028-01-01"}
	f.request[httpapi.Problem](t, "POST", base+"/series/"+id+"/generate", member.Token, "too-large", gen, 422)
	gen.Until = "2026-12-01"
	f.request[financeReference](t, "POST", base+"/series/"+id+"/generate", member.Token, "generate", gen, 200)
	edit := finance.SeriesEdit{SeriesValue: v.SeriesValue, Version: 1, Confirm: true, EffectiveFrom: "2026-11-01", State: "active", Reason: "Must not rewrite materialized dates"}
	f.request[httpapi.Problem](t, "PUT", base+"/series/"+id, member.Token, "rewrite", edit, 422)
	s := finance.New(f.service, f.store)
	if _, err := s.ShareAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + owner.Token, SpaceID: owner.SpaceID, Key: "revoke"}, bank, finance.Sharing{Visibility: "private", SpacePermission: "read", Grants: []finance.Grant{}}, 1); err != nil {
		t.Fatal(err)
	}
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id, member.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "GET", base+"/series/"+id+"/versions", member.Token, "", nil, 404)
	f.request[httpapi.Problem](t, "POST", base+"/series", member.Token, "series", v, 404)
	f.request[httpapi.Problem](t, "POST", base+"/series/"+id+"/generate", member.Token, "generate", gen, 404)
	page := f.request[access.Page[finance.SeriesView]](t, "GET", base+"/series?limit=1", member.Token, "", nil, 200)
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatal("revoked series leaked through pagination")
	}
	f.request[httpapi.Problem](t, "GET", "/v1/spaces/"+member.SpaceID+"/series/"+id, member.Token, "", nil, 404)
}
