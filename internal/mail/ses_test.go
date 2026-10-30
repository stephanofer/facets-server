package mail_test

import (
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/mail"
)

type sendFunc func(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error)

func (f sendFunc) SendEmail(ctx context.Context, in *sesv2.SendEmailInput, opts ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
	return f(ctx, in, opts...)
}

func newSender(t *testing.T, client mail.SESAPI) *mail.SES {
	t.Helper()
	s, err := mail.NewSES(client, mail.Options{From: "sender@example.test", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func validMessage() mail.Message {
	return mail.Message{To: "recipient@example.test", Subject: "Confirmación ✓", Body: "Código: 123", HTML: "<p>Código: 123</p>"}
}

func TestLoadSES(t *testing.T) {
	// Explicit isolated SDK configuration; no developer credentials or AWS calls.
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_DEFAULT_REGION", "")
	if _, err := mail.LoadSES(t.Context(), mail.Options{From: "sender@example.test", Timeout: time.Second}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_REGION", "")
	if _, err := mail.LoadSES(t.Context(), mail.Options{From: "sender@example.test", Timeout: time.Second}); err == nil {
		t.Fatal("missing region accepted")
	}
}

func TestNewSES(t *testing.T) {
	t.Parallel()
	client := sendFunc(func(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
		t.Fatal("unexpected send")
		return nil, nil
	})
	for _, tc := range []struct {
		name    string
		from    string
		timeout time.Duration
	}{
		{"missing sender", "", time.Second},
		{"header injection", "a@example.test\r\nBcc: b@example.test", time.Second},
		{"non ASCII", "á@example.test", time.Second},
		{"missing timeout", "a@example.test", 0},
		{"unbounded timeout", "a@example.test", time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := mail.NewSES(client, mail.Options{From: tc.from, Timeout: tc.timeout}); err == nil {
				t.Fatal("invalid options accepted")
			}
		})
	}
	if _, err := mail.NewSES(nil, mail.Options{From: "a@example.test", Timeout: time.Second}); err == nil {
		t.Fatal("nil client accepted")
	}
}

func TestSESDeliver(t *testing.T) {
	t.Parallel()
	var calls int
	s := newSender(t, sendFunc(func(ctx context.Context, in *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
		calls++
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Second {
			t.Fatal("send missing bounded context")
		}
		if *in.FromEmailAddress != "sender@example.test" || in.Destination.ToAddresses[0] != "recipient@example.test" || in.ReplyToAddresses[0] != "reply@example.test" || len(in.Destination.CcAddresses) != 1 || len(in.Destination.BccAddresses) != 1 {
			t.Fatal("incorrect addressing")
		}
		m := in.Content.Simple
		if *m.Subject.Charset != "UTF-8" || *m.Subject.Data != "Confirmación ✓" || *m.Body.Text.Data != "Código: 123" || *m.Body.Html.Data != "<p>Código: 123</p>" {
			t.Fatal("incorrect UTF-8 content")
		}
		return &sesv2.SendEmailOutput{MessageId: new("provider-reference")}, nil
	}))
	m := validMessage()
	m.ReplyTo, m.Cc, m.Bcc = []string{"reply@example.test"}, []string{"copy@example.test"}, []string{"blind@example.test"}
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Deliver(t.Context(), "outbox-id", payload)
	if err != nil || id != "provider-reference" || calls != 1 {
		t.Fatal(id, err, calls)
	}
	for _, payload := range [][]byte{[]byte(`{`), []byte(`{"to":"a","to":"b"}`), []byte(strings.Repeat("x", 65537))} {
		if _, err := s.Deliver(t.Context(), "id", payload); !errors.Is(err, execution.ErrPermanent) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("invalid payload reached SDK")
	}
}

func TestSESSendValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*mail.Message)
	}{
		{"missing recipient", func(m *mail.Message) { m.To = "" }},
		{"multiple recipients in address", func(m *mail.Message) { m.To = "a@example.test,b@example.test" }},
		{"invalid cc", func(m *mail.Message) { m.Cc = []string{"bad"} }},
		{"invalid bcc", func(m *mail.Message) { m.Bcc = []string{"bad"} }},
		{"invalid reply", func(m *mail.Message) { m.ReplyTo = []string{"bad"} }},
		{"too many recipients", func(m *mail.Message) { m.Cc = make([]string, 50) }},
		{"subject injection", func(m *mail.Message) { m.Subject = "private\r\nBcc: victim@example.test" }},
		{"missing subject", func(m *mail.Message) { m.Subject = "" }},
		{"long subject", func(m *mail.Message) { m.Subject = strings.Repeat("x", 201) }},
		{"missing bodies", func(m *mail.Message) { m.Body, m.HTML = "", "" }},
		{"long body", func(m *mail.Message) { m.Body = strings.Repeat("x", 60001) }},
		{"invalid UTF8", func(m *mail.Message) { m.HTML = string([]byte{255}) }},
		{"NUL body", func(m *mail.Message) { m.Body = "\x00" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSender(t, sendFunc(func(context.Context, *sesv2.SendEmailInput, ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
				t.Fatal("invalid input reached provider")
				return nil, nil
			}))
			m := validMessage()
			tc.change(&m)
			_, err := s.Send(t.Context(), m)
			if !errors.Is(err, execution.ErrPermanent) || strings.Contains(err.Error(), "private") {
				t.Fatal(err)
			}
		})
	}
}

func TestSESSendCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		calls := 0
		s := newSender(t, sendFunc(func(ctx context.Context, _ *sesv2.SendEmailInput, _ ...func(*sesv2.Options)) (*sesv2.SendEmailOutput, error) {
			calls++
			<-ctx.Done()
			return nil, ctx.Err()
		}))
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := s.Send(ctx, validMessage()); !errors.Is(err, context.Canceled) || errors.Is(err, execution.ErrUncertain) || calls != 0 {
			t.Fatal(err, calls)
		}
		if _, err := s.Send(t.Context(), validMessage()); !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, execution.ErrUncertain) || calls != 1 {
			t.Fatal(err, calls)
		}
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func sdkConfig(client aws.HTTPClient) aws.Config {
	return aws.Config{Region: "us-east-1", HTTPClient: client,
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "synthetic", SecretAccessKey: "synthetic"}, nil
		})}
}

func TestSESSendSDKRetriesAndErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, code                    string
		status, attempts              int
		uncertain, permanent, success bool
	}{
		{"accepted", "", 200, 1, false, false, true},
		{"bad request", "BadRequestException", 400, 1, false, true, false},
		{"authorization", "AccessDeniedException", 403, 1, false, true, false},
		{"credentials", "UnrecognizedClientException", 403, 1, false, true, false},
		{"unverified domain", "MailFromDomainNotVerifiedException", 400, 1, false, true, false},
		{"rejected", "MessageRejected", 400, 1, false, true, false},
		{"paused", "SendingPausedException", 400, 1, false, true, false},
		{"quota", "LimitExceededException", 400, 1, false, false, false},
		{"throttled", "TooManyRequestsException", 429, 3, false, false, false},
		{"throttle then success", "TooManyRequestsException", 429, 2, false, false, true},
		{"server failure", "InternalFailure", 500, 1, true, false, false},
		{"lost response", "", 0, 1, true, false, false},
		{"missing id", "", 200, 1, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Scheme != "https" || r.URL.Path != "/v2/email/outbound-emails" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
						t.Error("missing official signed HTTPS request")
					}
					if tc.status == 0 {
						return nil, io.ErrUnexpectedEOF
					}
					status, body := tc.status, `{"__type":"`+tc.code+`","message":"private-recipient@example.test secret-code"}`
					if tc.success && calls == tc.attempts {
						status, body = 200, `{"MessageId":"accepted"}`
					}
					if tc.name == "missing id" {
						body = `{}`
					}
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "X-Amzn-Requestid": {"request-123"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})}
				client := sesv2.NewFromConfig(sdkConfig(httpClient))
				s, err := mail.NewSES(client, mail.Options{From: "sender@example.test", Timeout: 30 * time.Second})
				if err != nil {
					t.Fatal(err)
				}
				id, err := s.Send(t.Context(), validMessage())
				if calls != tc.attempts || (err == nil) != tc.success || errors.Is(err, execution.ErrUncertain) != tc.uncertain || errors.Is(err, execution.ErrPermanent) != tc.permanent {
					t.Fatalf("id=%s error=%v calls=%d", id, err, calls)
				}
				if err != nil && strings.Contains(err.Error(), "private-recipient") {
					t.Fatal("private SDK message leaked")
				}
				if tc.code == "BadRequestException" {
					if _, ok := errors.AsType[*types.BadRequestException](err); !ok {
						t.Fatal("SDK cause not preserved")
					}
					e, ok := errors.AsType[*mail.DeliveryError](err)
					if !ok || e.ErrorCode() != tc.code || e.RequestID() != "request-123" {
						t.Fatal("diagnostics lost", err)
					}
				}
			})
		})
	}
}

func TestSESSendHTTPS(t *testing.T) {
	t.Parallel()
	for _, uncertain := range []bool{false, true} {
		name := "accepted"
		if uncertain {
			name = "lost acknowledgement"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input sesv2.SendEmailInput
				if err := json.UnmarshalRead(r.Body, &input); err != nil {
					t.Error(err)
				}
				if r.TLS == nil || r.Method != http.MethodPost || r.Header.Get("Authorization") == "" || input.Content.Simple == nil {
					t.Error("invalid HTTPS send")
				}
				if uncertain {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					if err := conn.Close(); err != nil {
						t.Error(err)
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"MessageId":"tls-accepted"}`)
			}))
			defer server.Close()
			client := sesv2.NewFromConfig(sdkConfig(server.Client()), func(o *sesv2.Options) { o.BaseEndpoint = new(server.URL) })
			s := newSender(t, client)
			id, err := s.Send(t.Context(), validMessage())
			if uncertain {
				if !errors.Is(err, execution.ErrUncertain) || id != "" || calls.Load() != 1 {
					t.Fatal(id, err, calls.Load())
				}
			} else if err != nil || id != "tls-accepted" || calls.Load() != 1 {
				t.Fatal(id, err, calls.Load())
			}
		})
	}
}
