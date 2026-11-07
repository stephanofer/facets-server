package mail_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/mail"
)

func TestLoadSender(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, environment, from string
		wantError               bool
	}{
		{"development local", "development", "", false},
		{"production missing sender", "production", "", true},
		{"test missing sender", "test", "", true},
		{"unknown environment", "staging", "", true},
		{"empty environment", "", "", true},
		{"development invalid SES has no fallback", "development", "invalid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			sender, err := mail.LoadSender(t.Context(), tc.environment, mail.Options{From: tc.from, Timeout: time.Second}, slog.New(slog.NewJSONHandler(&output, nil)))
			if (err != nil) != tc.wantError {
				t.Fatalf("LoadSender error = %v, want error %v", err, tc.wantError)
			}
			if tc.wantError {
				if output.Len() != 0 {
					t.Fatal("non-local sender emitted development logs")
				}
				return
			}
			output.Reset()
			message := mail.Message{To: "local@example.test", Subject: "Confirmation", Body: "Your code: secret-code\nDo not share it."}
			id, err := sender.Send(t.Context(), message)
			if err != nil || id == "" {
				t.Fatalf("Send = %q, %v", id, err)
			}
			var record struct {
				Message mail.Message `json:"message"`
			}
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record.Message.To != message.To || record.Message.Subject != message.Subject || record.Message.Body != message.Body {
				t.Fatalf("logged message differs: %+v", record.Message)
			}
			output.Reset()
			message.To = "invalid\ninjected"
			if _, err := sender.Send(t.Context(), message); !errors.Is(err, execution.ErrPermanent) {
				t.Fatalf("invalid message error = %v", err)
			}
			message.To = "local@example.test"
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := sender.Send(ctx, message); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled send error = %v", err)
			}
			if strings.Contains(output.String(), "secret-code") {
				t.Fatal("rejected delivery leaked content")
			}
		})
	}
}

func TestLoadSenderConfiguredSES(t *testing.T) {
	// SDK configuration is local and deterministic; no Send call or AWS account.
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "synthetic")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "synthetic")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_CONFIG_FILE", t.TempDir()+"/missing-config")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", t.TempDir()+"/missing-credentials")
	for _, environment := range []string{"development", "production", "test"} {
		t.Run(environment, func(t *testing.T) {
			var output bytes.Buffer
			sender, err := mail.LoadSender(t.Context(), environment, mail.Options{From: "sender@example.test", Timeout: time.Second}, slog.New(slog.NewJSONHandler(&output, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := sender.(*mail.SES); !ok || output.Len() != 0 {
				t.Fatal("configured sender did not select SES exclusively")
			}
		})
	}
}
