package execution

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/execution/executiondb"
	"github.com/stephanofer/facets-server/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ErrUncertain means an external effect may have occurred. Never automatically resend.
var ErrUncertain = errors.New("external result uncertain")
var ErrObsolete = errors.New("job no longer authorized or applicable")
var ErrUnsupported = errors.New("job handler unavailable")

type Handler struct {
	// Check revalidates current recipient, permissions, preferences and resource state.
	Check func(context.Context, []byte) error
	// External adapters return ErrUncertain once an effect may have occurred.
	// Ordinary errors must mean it is safe to attempt the delivery again.
	Execute func(context.Context, string, []byte) (string, error)
}
type Worker struct {
	Store                *Store
	Queue                string
	Concurrency          int
	Timeout, Lease, Poll time.Duration
	Handlers             map[string]Handler // immutable after Run starts
	Log                  *slog.Logger
	Completed            metric.Int64Counter
	Duration, Age        metric.Float64Histogram
}

func (w *Worker) Run(ctx context.Context) error {
	if w.Concurrency < 1 || w.Concurrency > 32 || w.Timeout <= 0 || w.Lease <= w.Timeout || w.Poll <= 0 || (w.Queue != "notifications" && w.Queue != "heavy") {
		return errors.New("invalid worker configuration")
	}
	var wg sync.WaitGroup
	for range w.Concurrency {
		wg.Go(func() {
			ticker := time.NewTicker(w.Poll)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				worked, err := w.Once(ctx)
				if err != nil && ctx.Err() == nil {
					telemetry.LogError(ctx, w.Log, "job processing failed", err)
				}
				if worked && err == nil {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		})
	}
	wg.Wait()
	return nil
}

func (w *Worker) Once(ctx context.Context) (bool, error) {
	var job executiondb.Job
	err := w.Store.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := executiondb.New(tx)
		recovered, err := q.RecoverJobs(ctx)
		if err != nil {
			return err
		}
		for _, j := range recovered {
			if err := Audit(ctx, tx, "worker", "platform", "job.recovered."+j.State, j.ID, ""); err != nil {
				return err
			}
		}
		job, err = q.ClaimJob(ctx, executiondb.ClaimJobParams{
			Queue: w.Queue, LeaseToken: pgtype.Text{String: uuid.New().String(), Valid: true},
			LeaseMs: w.Lease.Milliseconds(),
		})
		// Recovery must commit even when there is no claimable job (notably
		// external work that just transitioned to uncertain).
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return false, err
	}
	if job.ID == "" {
		return false, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, w.Timeout)
	started := time.Now()
	runCtx, span := w.Store.DB.Tracer.Start(runCtx, "job.execute")
	result, runErr := w.execute(runCtx, job)
	if len(result) > 256 {
		result = ""
		if job.ExternalEffect {
			runErr = ErrUncertain
		} else {
			runErr = errors.New("invalid job result")
		}
	}
	span.End()
	cancel()
	state, code := "succeeded", ""
	if runErr != nil {
		state, code = "failed", "execution_failed"
		switch {
		case errors.Is(runErr, ErrUnsupported):
			state, code = "failed", "unsupported_handler"
		case errors.Is(runErr, ErrObsolete):
			state, code = "cancelled", "no_longer_applicable"
		case errors.Is(runErr, ErrUncertain):
			state, code = "uncertain", "external_result_uncertain"
		case job.Attempts < job.MaxAttempts:
			state = "pending"
		}
	}
	// The durable completion must survive request/worker cancellation. It has a
	// separate short deadline; if it fails the lease records recoverable uncertainty.
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), w.Store.DB.Timeout)
	defer finishCancel()
	err = w.Store.DB.Transaction(finishCtx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		n, err := executiondb.New(tx).FinishJob(ctx, executiondb.FinishJobParams{
			ID: job.ID, LeaseToken: job.LeaseToken, State: state, ResultRef: result, ErrorCode: code,
			DelayMs: (time.Second * time.Duration(1<<min(job.Attempts, 8))).Milliseconds(),
		})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrLeaseLost
		}
		return Audit(ctx, tx, "worker", "platform", "job."+state, job.ID, "")
	})
	if err == nil {
		if runErr != nil && !errors.Is(runErr, ErrObsolete) {
			telemetry.LogError(finishCtx, w.Log, "job execution failed", runErr)
		}
		age := max(0, started.Sub(job.CreatedAt.Time).Seconds())
		elapsed := time.Since(started).Seconds()
		w.Log.InfoContext(finishCtx, "job completed", "job_id", job.ID, "kind", job.Kind, "result", state,
			"age_seconds", age, "duration_seconds", elapsed)
		labels := metric.WithAttributes(attribute.String("queue", job.Queue), attribute.String("state", state))
		if w.Completed != nil {
			w.Completed.Add(finishCtx, 1, labels)
		}
		if w.Duration != nil {
			w.Duration.Record(finishCtx, elapsed, labels)
		}
		if w.Age != nil {
			w.Age.Record(finishCtx, age, labels)
		}
	}
	return true, err
}

func (w *Worker) execute(ctx context.Context, j executiondb.Job) (result string, err error) {
	defer func() {
		if recover() != nil {
			result = ""
			if j.ExternalEffect {
				err = ErrUncertain
			} else {
				err = errors.New("job handler panic")
			}
		}
	}()
	h, ok := w.Handlers[j.Kind]
	if !ok || h.Check == nil || h.Execute == nil {
		return "", ErrUnsupported
	}
	if err := h.Check(ctx, j.Payload); err != nil {
		return "", err
	}
	return h.Execute(ctx, j.ID, j.Payload)
}
