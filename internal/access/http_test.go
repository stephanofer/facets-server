package access_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestAuthorizationInputRuntime(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, authorization string
		want                int
	}{
		{name: "missing header remains optional", want: http.StatusOK},
		{name: "bearer token is preserved", authorization: "Bearer opaque-session", want: http.StatusOK},
		{name: "maximum length is accepted", authorization: strings.Repeat("a", 200), want: http.StatusOK},
		{name: "oversized header is rejected", authorization: strings.Repeat("a", 201), want: http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, err := httpapi.New(httpapi.Options{
				Ping: func(context.Context) error { return nil }, Log: slog.New(slog.DiscardHandler),
				Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"),
				Timeout: time.Second, MaxBody: 1 << 20,
			})
			if err != nil {
				t.Fatal(err)
			}
			called := false
			huma.Register(api.Huma, huma.Operation{OperationID: "test-authorization", Method: http.MethodGet, Path: "/test-authorization"},
				func(_ context.Context, in *access.AuthorizationInput) (*httpapi.Health, error) {
					called = true
					if in.Authorization != tc.authorization {
						t.Errorf("Authorization = %q, want %q", in.Authorization, tc.authorization)
					}
					return new(httpapi.Health), nil
				})
			req := httptest.NewRequest(http.MethodGet, "/test-authorization", nil)
			req.Header.Set("Authorization", tc.authorization)
			rec := httptest.NewRecorder()
			api.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.want || called != (tc.want == http.StatusOK) {
				t.Fatalf("status=%d handler called=%t, want status=%d body=%s", rec.Code, called, tc.want, rec.Body.String())
			}
		})
	}
}

func TestRegisterHTTPClientSpaceSelection(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.DiscardHandler)
	api, err := httpapi.New(httpapi.Options{
		Ping: func(context.Context) error { return nil }, Log: log,
		Tracer: tracenoop.NewTracerProvider().Tracer("test"), Meter: noop.NewMeterProvider().Meter("test"),
		Timeout: time.Second, MaxBody: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	access.RegisterHTTP(api.Huma, nil, log)
	if _, ok := api.Schema.Paths["/v1/spaces/{space_id}/select"]; ok {
		t.Fatal("space selection remains in OpenAPI")
	}
	if _, ok := api.Schema.Components.Schemas.Map()["Profile"].Properties["active_space"]; ok {
		t.Fatal("selected space remains in the profile schema")
	}
	for _, path := range []string{"/v1/auth/reauthenticate", "/v1/internal/auth/reauthenticate"} {
		if operation := api.Schema.Paths[path]; operation == nil || operation.Post == nil {
			t.Fatalf("reauthentication route missing: %s", path)
		}
	}
	rec := httptest.NewRecorder()
	api.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/spaces/00000000-0000-4000-8000-000000000001/select", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("removed selection route: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
