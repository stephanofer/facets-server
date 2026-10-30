package telemetry_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/oops"
	"github.com/stephanofer/facets-server/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestSignalsExcludeSensitiveAttributes(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	signals := telemetry.New(log)
	counter, err := signals.Metrics.Meter("test").Int64Counter("test.requests")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(t.Context(), 1)
	snapshot, err := signals.Collect(t.Context())
	if err != nil || len(snapshot.ScopeMetrics) != 1 {
		t.Fatal(snapshot, err)
	}
	parent := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled, Remote: true,
	})
	ctx := trace.ContextWithRemoteSpanContext(t.Context(), parent)
	_, span := signals.Tracer().Start(ctx, "test.operation", trace.WithAttributes(attribute.String("body", "private-secret")))
	span.AddEvent("private-secret")
	span.End()
	technical := oops.In("test").With("password", "private-secret").Wrap(&pgconn.PgError{
		Code: "23505", Message: "private-secret", Detail: "private-secret",
	})
	telemetry.LogError(t.Context(), log, "operation failed", technical)
	if err := signals.Shutdown(context.WithoutCancel(t.Context())); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(logs.String(), "private-secret") {
		t.Fatal("telemetry exposed sensitive content")
	}
	for _, want := range []string{"test.operation", "trace_id", "23505", "stack"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("missing diagnostic %s", want)
		}
	}
}
