//go:build integration

package access_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/config"
	"github.com/stephanofer/facets-server/internal/mail"
)

type runtimeLogs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (w *runtimeLogs) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.Write(p)
}

func (w *runtimeLogs) snapshot() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes())
}

func TestDevelopmentMailRuntime(t *testing.T) {
	f := setup(t)
	c, err := config.Load(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	c.Access = f.policy
	c.DatabaseURL = f.store.DB.Pool.Config().ConnString()
	c.Address, c.DiagnosticsAddress = "127.0.0.1:0", "127.0.0.1:0"
	var logs runtimeLogs
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	ctx, cancel := context.WithCancel(t.Context())
	results := make(chan error, 2)
	for _, mode := range []string{"serve", "worker"} {
		go func() { results <- app.Run(ctx, c, mode, "", "", log, io.Discard) }()
	}
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case err := <-results:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(20 * time.Second):
				t.Error("runtime did not stop")
			}
		}
	})
	waitFor := func(find func([]byte) bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if find(logs.snapshot()) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("runtime did not produce the expected event")
	}
	// Replace the fixture HTTP endpoint with the real composed server.
	f.server.Close()
	waitFor(func(data []byte) bool {
		for line := range bytes.SplitSeq(data, []byte{'\n'}) {
			var record struct {
				Msg     string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal(line, &record) == nil && record.Msg == "server started" {
				f.server.URL = "http://" + record.Address
				return true
			}
		}
		return false
	})
	email, password := "local-runtime@example.test", "Synthetic phrase for local runtime verification"
	f.request[map[string]any](t, "POST", "/v1/auth/register", "", "", access.Registration{Email: email, Password: password, Name: "Local", Currency: "PEN", Zone: "America/Lima", Locale: "es-PE"}, 200)
	var code string
	waitFor(func(data []byte) bool {
		for line := range bytes.SplitSeq(data, []byte{'\n'}) {
			var record struct {
				Message mail.Message `json:"message"`
			}
			if json.Unmarshal(line, &record) == nil && record.Message.To == email && record.Message.Subject == "Facets access confirmation" {
				code, _, _ = strings.Cut(strings.TrimPrefix(record.Message.Body, "Your Facets confirmation code: "), "\n")
				return code != ""
			}
		}
		return false
	})
	waitFor(func([]byte) bool {
		return f.count(t, `SELECT count(*) FROM jobs WHERE state='succeeded' AND result_ref='development-log'`) == 1
	})
	if f.count(t, `SELECT count(*) FROM access_deliveries WHERE octet_length(ciphertext)>0`) != 0 {
		t.Fatal("successful log delivery retained delivery secrets")
	}
	f.request[map[string]any](t, "POST", "/v1/auth/verify", "", "", map[string]any{"token": code}, 200)
	session := f.request[access.SessionResult](t, "POST", "/v1/auth/login", "", "", access.Credentials{Email: email, Password: password, Device: "local"}, 200)
	if session.Token == "" {
		t.Fatal("verified user could not log in")
	}
	for _, environment := range []string{"production", "test"} {
		t.Run(environment+" rejects missing sender", func(t *testing.T) {
			strict := c
			strict.Environment = environment
			if err := app.Run(t.Context(), strict, "worker", "", "", log, io.Discard); err == nil || err.Error() != "invalid mail configuration" {
				t.Fatalf("worker startup error = %v", err)
			}
		})
	}
}
