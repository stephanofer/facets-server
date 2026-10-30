// Package telemetry creates explicit, bounded OpenTelemetry providers.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Signals struct {
	Traces  *sdktrace.TracerProvider
	Metrics *metric.MeterProvider
	Reader  *metric.ManualReader
}

func New(log *slog.Logger) *Signals {
	reader := metric.NewManualReader()
	traces := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1))),
		sdktrace.WithBatcher(
			logExporter{log: log},
			sdktrace.WithMaxQueueSize(256), sdktrace.WithMaxExportBatchSize(64),
			sdktrace.WithBatchTimeout(time.Second), sdktrace.WithExportTimeout(time.Second),
		),
	)
	return &Signals{Traces: traces, Metrics: metric.NewMeterProvider(metric.WithReader(reader)), Reader: reader}
}
func (s *Signals) Tracer() trace.Tracer { return s.Traces.Tracer("facets") }
func (s *Signals) Collect(ctx context.Context) (metricdata.ResourceMetrics, error) {
	var m metricdata.ResourceMetrics
	err := s.Reader.Collect(ctx, &m)
	return m, err
}
func (s *Signals) Shutdown(ctx context.Context) error {
	return errors.Join(s.Traces.Shutdown(ctx), s.Metrics.Shutdown(ctx))
}

// Export only allowlisted metadata. Never serialize span attributes or events,
// since dependency instrumentation may attach URLs, SQL or private values.
type logExporter struct{ log *slog.Logger }

func (e logExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		e.log.InfoContext(
			ctx, "trace span", "operation", span.Name(),
			"trace_id", span.SpanContext().TraceID().String(), "span_id", span.SpanContext().SpanID().String(),
			"duration_ms", span.EndTime().Sub(span.StartTime()).Milliseconds(),
		)
	}
	return nil
}
func (logExporter) Shutdown(context.Context) error { return nil }
