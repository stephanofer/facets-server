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
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
)

func TestFinancialRecoveryAndAtomicity(t *testing.T) {
	f := setup(t)
	t.Run("audit failure rolls back reference grants and receipt", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "rollback-finance@example.test")
		if _, err := f.db.Pool.Exec(t.Context(), "REVOKE INSERT ON technical_audit FROM access_app"); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := f.db.Pool.Exec(t.Context(), "GRANT INSERT ON technical_audit TO access_app"); err != nil {
				t.Error(err)
			}
		}()
		_, err := finance.New(f.service, f.store).CreateAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "rollback"}, accountInput("private"))
		if err == nil {
			t.Fatal("unexpected success without durable audit")
		}
		if f.count(t, "SELECT count(*) FROM financial_accounts") != 0 || f.count(t, "SELECT count(*) FROM account_grants") != 0 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action='account.create'") != 0 {
			t.Fatal("failed command left partial state")
		}
	})
	t.Run("lost committed response replays after service replacement", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "lost-finance@example.test")
		var drop atomic.Bool
		drop.Store(true)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.server.Config.Handler.ServeHTTP(&discardResponse{ResponseWriter: w, Drop: &drop}, r)
		}))
		defer server.Close()
		data, err := json.Marshal(accountInput("private"))
		if err != nil {
			t.Fatal(err)
		}
		base := "/v1/spaces/" + u.SpaceID + "/accounts"
		req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+base, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+u.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "lost-account")
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
		var b [1]byte
		if n, err := conn.Read(b[:]); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatal("response not lost after commit", n, err)
		}
		if f.count(t, "SELECT count(*) FROM financial_accounts") != 1 {
			t.Fatal("position was not committed")
		}
		restarted, err := access.New(f.store, f.policy, finance.SeedDefaults)
		if err != nil {
			t.Fatal(err)
		}
		id, err := finance.New(restarted, f.store).CreateAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "lost-account"}, accountInput("private"))
		if err != nil || id == "" {
			t.Fatal("cannot recover committed reference", err)
		}
		if f.count(t, "SELECT count(*) FROM financial_accounts") != 1 || f.count(t, "SELECT count(*) FROM action_receipts WHERE action_key='lost-account'") != 1 {
			t.Fatal("replay duplicated confirmed reference")
		}
	})
	t.Run("concurrent versioned edits have one winner", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "edit-race-finance@example.test")
		s := finance.New(f.service, f.store)
		id, err := s.CreateAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: "create"}, accountInput("private"))
		if err != nil {
			t.Fatal(err)
		}
		results := make(chan error, 8)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				_, err := s.EditAccount(t.Context(), access.SpaceCommand{Authorization: "Bearer " + u.Token, SpaceID: u.SpaceID, Key: uuid.New().String()}, id, finance.AccountEdit{Name: "Versioned update", State: "active", Version: 1})
				results <- err
			})
		}
		wg.Wait()
		close(results)
		wins := 0
		for err := range results {
			if err == nil {
				wins++
			} else if !errors.Is(err, execution.ErrVersion) {
				t.Fatal(err)
			}
		}
		if wins != 1 || f.count(t, "SELECT count(*) FROM technical_audit WHERE event='account.edit'") != 1 {
			t.Fatal("concurrent edits overwrote each other")
		}
	})
}
