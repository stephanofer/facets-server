package access_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

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
