package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestTechnicalContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, path, body string
		dbError                  bool
		want                     int
		code                     string
	}{
		{name: "live", method: "GET", path: "/v1/health/live", want: 200},
		{name: "ready", method: "GET", path: "/v1/health/ready", want: 200},
		{name: "unready does not expose DB error", method: "GET", path: "/v1/health/ready", dbError: true, want: 503, code: "not_ready"},
		{name: "unknown query sanitized", method: "GET", path: "/v1/health/live?password=private-secret", want: 422, code: "invalid_request"},
		{name: "not found", method: "GET", path: "/private-secret", want: 404, code: "not_found"},
		{name: "method", method: "POST", path: "/v1/health/live", want: 405, code: "method_not_allowed"},
		{name: "oversize", method: "GET", path: "/v1/health/live", body: strings.Repeat("x", 33), want: 413, code: "body_too_large"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			api, err := httpapi.New(httpapi.Options{Ping: func(context.Context) error {
				if tc.dbError {
					return errors.New("private-secret")
				}
				return nil
			}, Log: slog.New(slog.NewJSONHandler(&logs, nil)), Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"), Timeout: time.Second, MaxBody: 32})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer private-secret")
			req.Header.Set("X-Request-ID", "private-secret")
			rec := httptest.NewRecorder()
			api.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.code != "" {
				var p httpapi.Problem
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
					t.Fatal(err)
				}
				if p.Code != tc.code || p.RequestID == "" || p.RequestID != rec.Header().Get("X-Request-ID") || !strings.Contains(rec.Header().Get("Content-Type"), "application/problem+json") {
					t.Fatalf("bad problem %+v", p)
				}
			}
			if strings.Contains(logs.String(), "private-secret") || strings.Contains(rec.Body.String(), "private-secret") {
				t.Fatal("private input leaked")
			}
		})
	}
}
func TestCancellationAndPanic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ping func(context.Context) error
		want int
	}{
		{"deadline", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, 503},
		{"panic", func(context.Context) error { panic("private-secret") }, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, err := httpapi.New(httpapi.Options{Ping: tc.ping, Log: slog.New(slog.DiscardHandler), Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"), Timeout: time.Millisecond, MaxBody: 32})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			api.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/health/ready", nil))
			if rec.Code != tc.want || strings.Contains(rec.Body.String(), "private-secret") {
				t.Fatal(rec.Code, rec.Body.String())
			}
		})
	}
	for _, tc := range []struct {
		name string
		err  error
		code string
	}{{"version", execution.ErrVersion, "version_conflict"}, {"idempotency", execution.ErrConflict, "idempotency_conflict"}, {"deadline", context.DeadlineExceeded, "deadline_exceeded"}, {"internal", errors.New("private-secret"), "internal_error"}} {
		t.Run(tc.name, func(t *testing.T) {
			p := httpapi.Translate(t.Context(), tc.err)
			if p.Code != tc.code {
				t.Fatal(p)
			}
		})
	}
}

func TestSaturatedHTTPDoesNotQueueRequests(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	release := make(chan struct{})
	api, err := httpapi.New(httpapi.Options{
		Ping: func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		Log: slog.New(slog.DiscardHandler), Tracer: tracenoop.NewTracerProvider().Tracer("test"),
		Meter: noop.NewMeterProvider().Meter("test"), Timeout: time.Second, MaxBody: 32, MaxRequests: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		api.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/health/ready", nil))
	}()
	<-entered
	response := httptest.NewRecorder()
	api.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/health/live", nil))
	close(release)
	<-done
	if response.Code != 503 || !strings.Contains(response.Body.String(), "capacity_unavailable") {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestRegisteredCommandsUseTheSharedBoundary(t *testing.T) {
	t.Parallel()
	api, err := httpapi.New(httpapi.Options{
		Ping: func(context.Context) error { return nil }, Log: slog.New(slog.DiscardHandler),
		Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"),
		Timeout: time.Second, MaxBody: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	type input struct {
		Body struct {
			Label string `json:"label" maxLength:"8"`
		}
	}
	type output struct {
		Body struct {
			Status string `json:"status"`
		}
	}
	huma.Register(api.Huma, huma.Operation{
		OperationID: "test-command", Method: http.MethodPost, Path: "/test-fixture", MaxBodyBytes: 32,
		Responses: httpapi.ErrorResponses(api.Huma, 400, 413, 422, 500),
	}, func(context.Context, *input) (*output, error) {
		result := new(output)
		result.Body.Status = "accepted"
		return result, nil
	})
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{name: "registered POST", body: `{"label":"example"}`, want: 200},
		{name: "validation sanitized", body: `{"label":"private-secret"}`, want: 422},
		{name: "chunked limit", body: strings.Repeat(" ", 33), want: 413},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/test-fixture", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request.ContentLength = -1 // Exercise the streaming limit, not only the length header.
			response := httptest.NewRecorder()
			api.Handler.ServeHTTP(response, request)
			if response.Code != tc.want || response.Header().Get("X-Request-ID") == "" || strings.Contains(response.Body.String(), "private-secret") {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}
