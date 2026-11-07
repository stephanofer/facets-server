// Package httpapi owns the versioned technical HTTP contract and safe boundary errors.
package httpapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/stephanofer/facets-server/internal/execution"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type requestKey struct{}
type routeKey struct{}

func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestKey{}).(string); return id }

type Problem struct {
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Status    int      `json:"status"`
	Code      string   `json:"code"`
	RequestID string   `json:"request_id"`
	Fields    []string `json:"fields,omitempty"`
}

func (p *Problem) Error() string             { return p.Title }
func (p *Problem) GetStatus() int            { return p.Status }
func (p *Problem) ContentType(string) string { return "application/problem+json" }

func Translate(ctx context.Context, err error) *Problem {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, execution.ErrConflict):
		status, code = 409, "idempotency_conflict"
	case errors.Is(err, execution.ErrVersion):
		status, code = 409, "version_conflict"
	case errors.Is(err, execution.ErrQueueFull):
		status, code = 503, "capacity_unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		status, code = 504, "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		status, code = 408, "request_cancelled"
	}
	return problem(ctx, status, code)
}
func problem(ctx context.Context, status int, code string) *Problem {
	return &Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, RequestID: RequestID(ctx)}
}

type Health struct {
	Body struct {
		Status string `json:"status"`
	}
}
type API struct {
	Handler http.Handler
	Schema  *huma.OpenAPI
	Huma    huma.API // Register typed module routes during composition, before serving.
}
type Options struct {
	Ping        func(context.Context) error
	Log         *slog.Logger
	Tracer      trace.Tracer
	Meter       metric.Meter
	Timeout     time.Duration
	MaxBody     int64
	MaxRequests int
}

func New(o Options) (*API, error) {
	if o.Ping == nil || o.Log == nil || o.Tracer == nil || o.Meter == nil || o.Timeout <= 0 || o.MaxBody < 1 {
		return nil, errors.New("invalid HTTP configuration")
	}
	if o.MaxRequests == 0 {
		o.MaxRequests = 64
	}
	if o.MaxRequests < 1 || o.MaxRequests > 1024 {
		return nil, errors.New("invalid HTTP concurrency")
	}
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Facets server", "0.3.0")
	cfg.CreateHooks = nil // Disable schema discovery metadata, not validation or OpenAPI.
	cfg.Info.Description = "Backend for the Facets finance tracker, managing customer accounts, secure access, and shared financial spaces."
	cfg.Servers = []*huma.Server{{URL: "http://127.0.0.1:8080", Description: "Local development server"}}
	cfg.Tags = []*huma.Tag{
		{Name: "Health", Description: "Service liveness and readiness probes."},
		{Name: "Auth", Description: "Customer registration, authentication, and password recovery."},
		{Name: "Account", Description: "Authenticated customer profile, preferences, sessions, and security settings."},
		{Name: "Spaces", Description: "Shared spaces, capabilities, membership, ownership, and billing responsibility."},
		{Name: "Invitations", Description: "Space invitations and recipient previews and decisions."},
		{Name: "Internal", Description: "Staff authentication, sessions, and account security."},
	}
	cfg.OpenAPIPath = "/v1/openapi"
	cfg.DocsPath = ""
	cfg.SchemasPath = ""
	cfg.RejectUnknownQueryParameters = true
	cfg.Transformers = []huma.Transformer{func(ctx huma.Context, _ string, v any) (any, error) {
		if e, ok := v.(*huma.ErrorModel); ok {
			return problem(ctx.Context(), e.Status, statusCode(e.Status)), nil
		}
		return v, nil
	}}
	api := humago.New(mux, cfg)
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		if name, ok := ctx.Context().Value(routeKey{}).(*string); ok {
			*name = ctx.Operation().OperationID
			trace.SpanFromContext(ctx.Context()).SetName("http." + *name)
		}
		next(ctx)
	})
	register := func(path, id, summary string, handler func(context.Context, *struct{}) (*Health, error)) {
		responses := ErrorResponses(api, 400, 405, 413, 422, 500, 503)
		huma.Register(api, huma.Operation{OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"Health"}, Responses: responses}, handler)
	}
	register("/v1/health/live", "liveness", "Check liveness", func(context.Context, *struct{}) (*Health, error) {
		r := new(Health)
		r.Body.Status = "ok"
		return r, nil
	})
	register("/v1/health/ready", "readiness", "Check readiness", func(ctx context.Context, _ *struct{}) (*Health, error) {
		if err := o.Ping(ctx); err != nil {
			return nil, problem(ctx, 503, "not_ready")
		}
		r := new(Health)
		r.Body.Status = "ready"
		return r, nil
	})
	// Route misses are still RFC 9457, never the standard text/plain response.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if path := api.OpenAPI().Paths[r.URL.Path]; path != nil {
			allowed := []string{}
			for method, operation := range map[string]*huma.Operation{
				"GET": path.Get, "HEAD": path.Head, "POST": path.Post, "PUT": path.Put,
				"PATCH": path.Patch, "DELETE": path.Delete, "OPTIONS": path.Options, "TRACE": path.Trace,
			} {
				if operation != nil {
					allowed = append(allowed, method)
				}
			}
			if path.Get != nil && path.Head == nil {
				allowed = append(allowed, "HEAD")
			}
			slices.Sort(allowed)
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			writeProblem(w, problem(r.Context(), http.StatusMethodNotAllowed, "method_not_allowed"))
			return
		}
		writeProblem(w, problem(r.Context(), http.StatusNotFound, "not_found"))
	})
	count, err := o.Meter.Int64Counter("http.requests")
	if err != nil {
		return nil, err
	}
	duration, err := o.Meter.Float64Histogram("http.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	return &API{Handler: boundary(mux, o, count, duration), Schema: api.OpenAPI(), Huma: api}, nil
}

// ErrorResponses gives module registrations the same Problem schema as runtime
// error translation. Call during composition, never while serving requests.
func ErrorResponses(api huma.API, statuses ...int) map[string]*huma.Response {
	schema := api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[Problem](), true, "")
	responses := make(map[string]*huma.Response, len(statuses))
	for _, status := range statuses {
		responses[fmt.Sprint(status)] = &huma.Response{
			Description: http.StatusText(status),
			Content:     map[string]*huma.MediaType{"application/problem+json": {Schema: schema}},
		}
	}
	return responses
}

func statusCode(status int) string {
	switch status {
	case 400, 422:
		return "invalid_request"
	case 413:
		return "body_too_large"
	case 503:
		return "not_ready"
	default:
		return "http_error"
	}
}
func writeProblem(w http.ResponseWriter, p *Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.MarshalWrite(w, p)
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (w *recorder) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *recorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}
func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func boundary(next http.Handler, o Options, count metric.Int64Counter, duration metric.Float64Histogram) http.Handler {
	semaphore := make(chan struct{}, o.MaxRequests)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := uuid.New().String()
		ctx := context.WithValue(r.Context(), requestKey{}, id)
		ctx, cancel := context.WithTimeout(ctx, o.Timeout)
		defer cancel()
		// Only known route names become labels or logs. Never log paths, queries or headers.
		route := "unmatched"
		switch r.URL.Path {
		case "/v1/health/live":
			route = "liveness"
		case "/v1/health/ready":
			route = "readiness"
		case "/v1/openapi.json", "/v1/openapi.yaml":
			route = "openapi"
		}
		ctx = propagation.TraceContext{}.Extract(ctx, propagation.HeaderCarrier(r.Header))
		ctx = context.WithValue(ctx, routeKey{}, &route)
		ctx, span := o.Tracer.Start(ctx, "http."+route)
		defer span.End()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		rw := &recorder{ResponseWriter: w}
		defer func() {
			if recover() != nil {
				if rw.status == 0 {
					writeProblem(rw, problem(ctx, 500, "internal_error"))
				}
				o.Log.ErrorContext(ctx, "HTTP handler panic", "request_id", id)
			}
			status := rw.status
			if status == 0 {
				status = 200
			}
			attrs := metric.WithAttributes(attribute.String("route", route), attribute.Int("status", status))
			count.Add(ctx, 1, attrs)
			duration.Record(ctx, time.Since(start).Seconds(), attrs)
			o.Log.InfoContext(ctx, "HTTP request", "request_id", id, "trace_id", trace.SpanContextFromContext(ctx).TraceID().String(), "operation", route, "status", status, "duration_ms", time.Since(start).Milliseconds())
		}()
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
		default:
			writeProblem(rw, problem(ctx, 503, "capacity_unavailable"))
			return
		}
		if r.ContentLength > o.MaxBody {
			writeProblem(rw, problem(ctx, 413, "body_too_large"))
			return
		}
		r.Body = http.MaxBytesReader(rw, r.Body, o.MaxBody)
		if strings.Contains(r.URL.Path, "\x00") {
			writeProblem(rw, problem(ctx, 400, "invalid_request"))
			return
		}
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}
