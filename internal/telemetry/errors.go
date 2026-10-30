package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/samber/oops"
)

// LogError keeps stack locations and SQLSTATE, not driver messages, detail,
// parameters, DSNs or arbitrary error strings that may contain private data.
func LogError(ctx context.Context, log *slog.Logger, message string, err error) {
	if err == nil {
		return
	}
	attrs := []any{"error_type", reflect.TypeOf(err).String()}
	if e, ok := errors.AsType[interface {
		error
		ErrorCode() string
		RequestID() string
	}](err); ok {
		attrs = append(attrs, "code", e.ErrorCode(), "provider_request_id", e.RequestID())
	}
	if e, ok := errors.AsType[*pgconn.PgError](err); ok {
		attrs = append(attrs, "sqlstate", e.Code)
	}
	if e, ok := oops.AsOops(err); ok {
		type location struct {
			Function, File string
			Line           int
		}
		frames := e.StackFrames()
		locations := make([]location, 0, min(len(frames), 24))
		for _, frame := range frames[:min(len(frames), 24)] {
			locations = append(locations, location{Function: frame.Function, File: filepath.Base(frame.File), Line: frame.Line})
		}
		attrs = append(attrs, "domain", e.Domain(), "code", e.Code(), "stack", locations)
	}
	log.ErrorContext(ctx, message, attrs...)
}
