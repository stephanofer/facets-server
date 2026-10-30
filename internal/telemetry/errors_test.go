package telemetry_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stephanofer/facets-server/internal/mail"
	"github.com/stephanofer/facets-server/internal/telemetry"
)

type rejectedMail struct{}

func (rejectedMail) SendEmail(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	return nil, &awshttp.ResponseError{ResponseError: &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400}},
		Err:      &types.BadRequestException{Message: new("secret-code recipient@example.test")},
	}, RequestID: "request-123"}
}

func TestLogErrorMailDiagnostics(t *testing.T) {
	t.Parallel()
	sender, err := mail.NewSES(rejectedMail{}, mail.Options{From: "sender@example.test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sender.Send(t.Context(), mail.Message{To: "recipient@example.test", Subject: "secret-subject", Body: "secret-body"})
	if err == nil {
		t.Fatal("expected rejection")
	}
	var logs bytes.Buffer
	telemetry.LogError(t.Context(), slog.New(slog.NewJSONHandler(&logs, nil)), "job execution failed", fmt.Errorf("handler: %w", err))
	for _, private := range []string{"secret-code", "recipient@example.test", "sender@example.test", "secret-subject", "secret-body"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("mail diagnostics leaked private data")
		}
	}
	for _, want := range []string{"BadRequestException", "request-123", "provider_request_id"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing diagnostic %s", want)
		}
	}
}
