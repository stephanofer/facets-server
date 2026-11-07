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
	for _, action := range []string{"refund", "reconciliation", "consistency"} {
		t.Run(action, func(t *testing.T) {
			f := setup(t)
			u := f.newUser(t, "lost-"+action+"@example.test")
			account := f.financialAccount(t, u, u.SpaceID, "bank", "PEN", "1000", "confirmed", "private")
			base := "/v1/spaces/" + u.SpaceID
			path := base + "/operations"
			refund := operationInput("refund", account, "50")
			reconciliation := finance.ReconciliationCreate{Date: "2026-11-02", ObservedMinor: "900", ExpectedCalculatedMinor: new("1000"), Adjust: true, Reason: "Synthetic lost reply", Confirm: true}
			var body any = refund
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
			req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+path, bytes.NewReader(data))
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
			s := finance.New(restarted)
			c := access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "lost-completion"}
			var id string
			switch action {
			case "refund":
				id, err = s.CreateOperation(t.Context(), c, refund)
			case "reconciliation":
				id, err = s.ReconcileAccount(t.Context(), c, account, reconciliation)
			case "consistency":
				id, err = s.CheckAccountConsistency(t.Context(), c, account, true)
			}
			if err != nil || id == "" {
				t.Fatal("replaced service could not recover committed result", err)
			}
			if got := f.request[financeReference](t, "POST", path, u.Token, "lost-completion", body, 200).ID; got != id {
				t.Fatal("HTTP retry changed result identity")
			}
			want := "1000"
			if action == "refund" {
				want = "950"
			}
			if action == "reconciliation" {
				want = "900"
			}
			f.assertFinancialBalance(t, u, u.SpaceID, account, want)
			if f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='lost-completion'") != 1 {
				t.Fatal("response recovery duplicated receipt")
			}
		})
	}
}
