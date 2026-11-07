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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/finance"
)

func TestFinancialCompletionLostResponses(t *testing.T) {
	for _, action := range []string{"refund", "reconciliation", "consistency", "due", "payment", "payment-operation", "series", "series-generation", "accountless-due", "reminder"} {
		t.Run(action, func(t *testing.T) {
			f := setup(t)
			u := f.newUser(t, "lost-"+action+"@example.test")
			account := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
			base := "/v1/spaces/" + u.SpaceID
			path := base + "/operations"
			method := "POST"
			refund := operationInput("refund", account, "50")
			reconciliation := finance.ReconciliationCreate{Date: "2026-11-02", ObservedMinor: "900", ExpectedCalculatedMinor: new("1000"), Adjust: true, Reason: "Synthetic lost reply", Confirm: true}
			var body any = refund
			dueValue := dueInput(account, "100")
			reminder := finance.DueReminderEdit{DueVersion: 1, Version: 0, Enabled: true, Confirm: true, Reason: "Recover a lost reminder response", ReminderTiming: finance.ReminderTiming{Zone: "UTC"}}
			var reminderDue string
			var application finance.PaymentApplicationCreate
			var composed finance.PaymentOperationCreate
			seriesValue := seriesInput(account)
			var seriesID string
			generation := finance.SeriesGenerate{Version: 1, Confirm: true, Until: "2027-02-01"}
			if action == "series" {
				path, body = base+"/series", seriesValue
			}
			if action == "series-generation" {
				seriesID = f.request[financeReference](t, "POST", base+"/series", u.Token, "series", seriesValue, 200).ID
				path, body = base+"/series/"+seriesID+"/generate", generation
			}
			if action == "due" {
				path, body = base+"/dues", dueValue
			}
			if action == "accountless-due" {
				dueValue.AccountID, dueValue.Currency = "", "PEN"
				path, body = base+"/dues", dueValue
			}
			if action == "reminder" {
				reminderDue = f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueValue, 200).ID
				path, body, method = base+"/dues/"+reminderDue+"/reminder", reminder, "PUT"
			}
			if action == "payment" {
				due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueValue, 200).ID
				op := f.request[financeReference](t, "POST", path, u.Token, "purchase", operationInput("expense", account, "100"), 200).ID
				application = paymentInput(op, due, "100", 1)
				path, body = base+"/payment-applications", application
			}
			if action == "payment-operation" {
				due := f.request[financeReference](t, "POST", base+"/dues", u.Token, "due", dueValue, 200).ID
				composed = finance.PaymentOperationCreate{Operation: operationInput("expense", account, "100"), Allocations: paymentInput("", due, "100", 1).Allocations}
				path, body = base+"/payment-operations", composed
			}
			if action == "refund" {
				purchase := f.request[financeReference](t, "POST", path, u.Token, "purchase", operationInput("expense", account, "100"), 200).ID
				refund.Refund = &finance.RefundLink{OperationID: purchase, AmountMinor: "50"}
				body = refund
			}
			if action == "reconciliation" {
				path = base + "/accounts/" + account + "/reconciliations"
				body = reconciliation
			}
			if action == "consistency" {
				path = base + "/accounts/" + account + "/consistency-checks"
				body = map[string]bool{"confirm": true}
			}
			var drop atomic.Bool
			drop.Store(true)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				f.server.Config.Handler.ServeHTTP(&discardResponse{ResponseWriter: w, Drop: &drop}, r)
			}))
			defer server.Close()
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+u.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "lost-completion")
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
				t.Fatal("committed response was not lost", n, err)
			}
			restarted, err := access.New(f.store, f.policy, finance.SeedDefaults)
			if err != nil {
				t.Fatal(err)
			}
			s := finance.New(restarted, f.store)
			c := access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "lost-completion"}
			var id string
			switch action {
			case "refund":
				id, err = s.CreateOperation(t.Context(), c, refund)
			case "reconciliation":
				id, err = s.ReconcileAccount(t.Context(), c, account, reconciliation)
			case "consistency":
				id, err = s.CheckAccountConsistency(t.Context(), c, account, true)
			case "due", "accountless-due":
				id, err = s.CreateDue(t.Context(), c, dueValue)
			case "reminder":
				id, err = s.SetDueReminder(t.Context(), c, reminderDue, reminder)
			case "payment":
				id, err = s.ApplyPayment(t.Context(), c, application)
			case "payment-operation":
				id, err = s.CreatePaymentOperation(t.Context(), c, composed)
			case "series":
				id, err = s.CreateSeries(t.Context(), c, seriesValue)
			case "series-generation":
				id, err = s.GenerateSeries(t.Context(), c, seriesID, generation)
			}
			if err != nil || id == "" {
				t.Fatal("replaced service could not recover committed result", err)
			}
			if got := f.request[financeReference](t, method, path, u.Token, "lost-completion", body, 200).ID; got != id {
				t.Fatal("HTTP retry changed result identity")
			}
			want := "1000"
			if action == "accountless-due" && f.count(t, "SELECT count(*) FROM planning_resources") != 1 {
				t.Fatal("lost response duplicated access resource")
			}
			if action == "reminder" && (f.count(t, "SELECT count(*) FROM due_reminders") != 1 || f.count(t, "SELECT count(*) FROM jobs WHERE kind='planning.reminder'") != 1) {
				t.Fatal("lost response duplicated reminder or delivery intent")
			}
			if action == "series" && f.count(t, "SELECT count(*) FROM recurring_series") != 1 {
				t.Fatal("lost response duplicated series")
			}
			if action == "series-generation" && (f.count(t, "SELECT count(*) FROM recurring_occurrences") != 3 || f.count(t, "SELECT count(*) FROM due_items") != 3) {
				t.Fatal("lost response duplicated occurrence horizon")
			}
			if action == "refund" {
				want = "950"
			}
			if action == "reconciliation" {
				want = "900"
			}
			if action == "payment" || action == "payment-operation" {
				want = "900"
				if f.count(t, "SELECT count(*) FROM payment_applications") != 1 {
					t.Fatal("lost response duplicated application")
				}
			}
			f.assertFinancialBalance(t, u, u.SpaceID, account, want)
			if f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='lost-completion'") != 1 {
				t.Fatal("response recovery duplicated receipt")
			}
		})
	}
}
