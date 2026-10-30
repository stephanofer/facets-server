package execution_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stephanofer/facets-server/internal/execution"
)

func TestWorkerRunCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	worker := execution.Worker{
		Queue: "notifications", Concurrency: 4, Timeout: time.Second,
		Lease: 2 * time.Second, Poll: time.Second, Log: slog.New(slog.DiscardHandler),
	}
	// A cancelled worker owns and joins all goroutines without touching persistence.
	if err := worker.Run(ctx); err != nil {
		t.Fatal(err)
	}
	worker.Concurrency = 0
	if err := worker.Run(t.Context()); err == nil {
		t.Fatal("unbounded worker configuration accepted")
	}
}
