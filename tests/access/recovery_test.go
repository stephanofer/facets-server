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
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/pquerna/otp/totp"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/httpapi"
)

type discardResponse struct {
	http.ResponseWriter
	Drop    *atomic.Bool
	discard bool
}

func otpKey(uri string) (string, error) {
	v, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	secret := v.Query().Get("secret")
	if secret == "" {
		return "", errors.New("missing enrollment secret")
	}
	return secret, nil
}
func (w *discardResponse) WriteHeader(status int) {
	if status == 200 && w.Drop.Swap(false) {
		conn, _, err := w.ResponseWriter.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
			w.discard = true
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *discardResponse) Write(p []byte) (int, error) {
	if w.discard {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}

func TestAccessRecovery(t *testing.T) {
	f := setup(t)
	t.Run("service restart preserves sessions verification and durable delivery", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "restart@example.test")
		registration := access.Registration{Email: "pending@example.test", Password: "A unique pending restart phrase", Name: "Pending", Currency: "PEN", Zone: "UTC", Locale: "en"}
		f.request[map[string]any](t, "POST", "/v1/auth/register", "", "", registration, 200)
		restarted, err := access.New(f.store, f.policy)
		if err != nil {
			t.Fatal(err)
		}
		profile, err := restarted.Profile(t.Context(), "Bearer "+u.Token, "customer")
		if err != nil || profile.ID != u.ID {
			t.Fatal("persisted session lost on restart", err)
		}
		oldService := f.service
		f.service = restarted
		t.Cleanup(func() { f.service = oldService })
		// The worker and sender remain process-owned. All delivery material is in
		// PostgreSQL, so a fresh handler can execute an already queued intention.
		oldHandler := f.worker.Handlers["access.mail"]
		defer func() { f.worker.Handlers["access.mail"] = oldHandler }()
		f.worker.Handlers["access.mail"] = restarted.MailHandler(f.sender)
		token := f.code(t, registration.Email)
		if err := restarted.Verify(t.Context(), token, "restart-origin"); err != nil {
			t.Fatal(err)
		}
		if f.count(t, `SELECT count(*) FROM spaces`) != 2 {
			t.Fatal("restart lost or duplicated personal space")
		}
	})
	t.Run("real HTTP response loss after commit replays space creation", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "lost-response@example.test")
		var drop atomic.Bool
		drop.Store(true)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.server.Config.Handler.ServeHTTP(&discardResponse{ResponseWriter: w, Drop: &drop}, r)
		}))
		defer server.Close()
		settings := access.SpaceSettings{Name: "Committed once", Purpose: "group", Currency: "PEN", Zone: "America/Lima", Locale: "en", PeriodDay: 26, NewAccountVisibility: "propose_shared"}
		data, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		key := uuid.New().String()
		req, err := http.NewRequestWithContext(t.Context(), "POST", server.URL+"/v1/spaces", bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+u.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
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
		b := make([]byte, 1)
		if n, err := conn.Read(b); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatal("response was not lost after commit", n, err)
		}
		if f.count(t, `SELECT count(*) FROM spaces`) != 2 {
			t.Fatal("command not committed before disconnection")
		}
		f.request[map[string]any](t, "POST", "/v1/spaces", u.Token, key, settings, 200)
		if f.count(t, `SELECT count(*) FROM spaces`) != 2 || f.count(t, `SELECT count(*) FROM action_receipts WHERE action_key=$1`, key) != 1 {
			t.Fatal("response replay duplicated space or receipt")
		}
	})
	t.Run("concurrent recovery-code use and session admission are atomic", func(t *testing.T) {
		f.reset(t)
		u := f.newUser(t, "factor-race@example.test")
		setup := f.request[struct {
			URI string `json:"uri"`
		}](t, "POST", "/v1/me/mfa/setup", u.Token, "", nil, 200)
		key, err := otpKey(setup.URI)
		if err != nil {
			t.Fatal(err)
		}
		code, err := totp.GenerateCode(key, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		enabled := f.request[struct {
			Codes []string `json:"recovery_codes"`
		}](t, "POST", "/v1/me/mfa/confirm", u.Token, "", map[string]any{"code": code}, 200)
		var successes atomic.Int64
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				_, err := f.service.Login(t.Context(), access.Credentials{Email: u.Email, Password: u.Password, Code: enabled.Codes[0], Device: "concurrent"}, "customer", "parallel-factor")
				if err == nil {
					successes.Add(1)
				} else if !errors.Is(err, access.ErrUnauthorized) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatal("recovery factor accepted more than once")
		}
		if f.count(t, `SELECT count(*) FROM recovery_codes WHERE used_at IS NOT NULL`) != 1 {
			t.Fatal("recovery-code consumption mismatch")
		}
	})
	t.Run("expired verification preserves pending state without a partial space", func(t *testing.T) {
		f.reset(t)
		registration := access.Registration{Email: "expired@example.test", Password: "A synthetic pending identity phrase", Name: "Test", Currency: "PEN", Zone: "UTC", Locale: "en"}
		f.request[map[string]any](t, "POST", "/v1/auth/register", "", "", registration, 200)
		token := f.code(t, registration.Email)
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE identity_tokens SET expires_at=clock_timestamp()-interval '1 second'`); err != nil {
			t.Fatal(err)
		}
		f.request[httpapi.Problem](t, "POST", "/v1/auth/verify", "", "", map[string]any{"token": token}, 401)
		if f.count(t, `SELECT count(*) FROM principals WHERE state='pending'`) != 1 || f.count(t, `SELECT count(*) FROM spaces`) != 0 {
			t.Fatal("expired token created partial registration")
		}
		if _, err := f.db.Pool.Exec(t.Context(), `UPDATE identity_tokens SET created_at=clock_timestamp()-interval '2 minutes'`); err != nil {
			t.Fatal(err)
		}
		f.request[map[string]any](t, "POST", "/v1/auth/verification", "", "", map[string]any{"email": registration.Email}, 200)
		replacement := f.code(t, registration.Email)
		f.request[map[string]any](t, "POST", "/v1/auth/verify", "", "", map[string]any{"token": replacement}, 200)
		if f.count(t, `SELECT count(*) FROM spaces`) != 1 {
			t.Fatal("verification replacement failed")
		}
		f.request[httpapi.Problem](t, "POST", "/v1/auth/verify", "", "", map[string]any{"token": token}, 401)
	})
	t.Run("notification preference is checked when invitation mail executes", func(t *testing.T) {
		f.reset(t)
		owner := f.newUser(t, "owner@example.test")
		recipient := f.newUser(t, "muted@example.test")
		f.request[map[string]any](t, "POST", "/v1/spaces/"+owner.SpaceID+"/invitations", owner.Token, uuid.New().String(), access.Invite{Email: recipient.Email, Role: "member"}, 200)
		p := f.request[access.Profile](t, "GET", "/v1/me", recipient.Token, "", nil, 200)
		f.request[map[string]any](t, "PUT", "/v1/me/preferences", recipient.Token, "", access.Preferences{Name: p.Name, Locale: p.Locale, Zone: p.Zone, Notifications: false, Version: p.Version}, 200)
		f.drain(t)
		if f.count(t, `SELECT count(*) FROM jobs WHERE state='cancelled'`) != 1 {
			t.Fatal("delivery ignored current preference")
		}
		if f.count(t, `SELECT count(*) FROM access_deliveries WHERE kind='invitation' AND octet_length(ciphertext)>0`) != 0 {
			t.Fatal("cancelled delivery retained its usable secret")
		}
	})
}
