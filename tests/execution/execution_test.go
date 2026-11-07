//go:build integration

package execution_test

import (
	"context"
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stephanofer/facets-server/internal/app"
	"github.com/stephanofer/facets-server/internal/config"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/execution/executiondb"
	mailer "github.com/stephanofer/facets-server/internal/mail"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestExecutionGuarantees(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	data, err := os.ReadFile("../../environment.json")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		PostgresImage string `json:"postgresImage"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	container, err := postgres.Run(ctx, env.PostgresImage, postgres.WithDatabase("execution_test"), postgres.WithUsername("owner"), postgres.WithPassword(rand.Text()), postgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	if err := app.Migrate(ctx, dsn); err != nil {
		t.Fatal("migration reapplication", err)
	}
	db, err := database.Open(ctx, database.Options{
		URL: dsn, MaxConns: 8, Timeout: 5 * time.Second, Tracer: noop.NewTracerProvider().Tracer("test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Pool.Close()
	if err := db.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckRuntimeRole(ctx); err == nil {
		t.Fatal("superuser accepted as application")
	}
	_, err = db.Pool.Exec(ctx, `CREATE TABLE execution_probe (id text PRIMARY KEY, version bigint NOT NULL DEFAULT 1, value integer NOT NULL DEFAULT 0)`)
	if err != nil {
		t.Fatal(err)
	}
	store := &execution.Store{DB: db, QueueLimit: 100}
	authorize := func(context.Context, pgx.Tx, string) error { return nil } // Synthetic fixture, never a production authorizer.
	var calls atomic.Int64
	apply := func(ctx context.Context, tx pgx.Tx) (string, error) {
		calls.Add(1)
		if _, err := tx.Exec(ctx, `INSERT INTO execution_probe(id) VALUES ('atomic')`); err != nil {
			return "", err
		}
		if err := execution.Audit(ctx, tx, "test-actor", "test-scope", "probe.created", "atomic", ""); err != nil {
			return "", err
		}
		_, err := store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "atomic", Payload: []byte(`{}`), MaxAttempts: 3})
		return "atomic", err
	}
	action := execution.Action{Actor: "test-actor", Scope: "test-scope", Kind: "create-probe", Key: "same-action", Fingerprint: [32]byte{1}}
	// Each scenario starts from its own synthetic fixture state and can be run
	// independently with -run. Only this disposable Testcontainers database is reset.
	reset := func(t *testing.T) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, `TRUNCATE execution_probe,jobs,technical_audit,action_receipts RESTART IDENTITY`); err != nil {
			t.Fatal(err)
		}
	}
	addJob := func(t *testing.T, j execution.Job) string {
		t.Helper()
		var id string
		if err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			id, err = store.Enqueue(ctx, tx, j)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	t.Run("scheduled batch admission and deduplication", func(t *testing.T) {
		reset(t)
		jobs := []execution.Job{
			{Queue: "heavy", Kind: "batch-probe", DedupKey: "batch-immediate", MaxAttempts: 2},
			{Queue: "heavy", Kind: "batch-probe", DedupKey: "batch-future", MaxAttempts: 2, AvailableAt: time.Now().UTC().Add(time.Hour)},
		}
		enqueue := func(values []execution.Job) error {
			return db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { return store.EnqueueBatch(ctx, tx, values) })
		}
		var wg sync.WaitGroup
		errs := make(chan error, 4)
		for range 4 {
			wg.Go(func() { errs <- enqueue(jobs) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var count int
		if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM jobs").Scan(&count); err != nil || count != 2 {
			t.Fatal("batch duplicated", count, err)
		}
		addJob(t, jobs[0]) // Single and bulk enqueue use identical fingerprints.
		worker := execution.Worker{Store: store, Queue: "heavy", Timeout: time.Second, Lease: 5 * time.Second, Log: slog.New(slog.DiscardHandler), Handlers: map[string]execution.Handler{"batch-probe": {Check: func(context.Context, []byte) error { return nil }, Execute: func(context.Context, string, []byte) (string, error) { return "done", nil }}}}
		if worked, err := worker.Once(ctx); err != nil || !worked {
			t.Fatal(worked, err)
		}
		if worked, err := worker.Once(ctx); err != nil || worked {
			t.Fatal("future job claimed early", worked, err)
		}
		if err := enqueue(jobs); err != nil {
			t.Fatal("terminal batch replay", err)
		}
		changed := []execution.Job{jobs[0]}
		changed[0].Payload = []byte("different")
		if err := enqueue(changed); !errors.Is(err, execution.ErrConflict) {
			t.Fatal("batch payload conflict", err)
		}
		bounded := execution.Store{DB: db, QueueLimit: 1}
		err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			return bounded.EnqueueBatch(ctx, tx, []execution.Job{{Queue: "heavy", Kind: "batch-probe", DedupKey: "no-capacity", MaxAttempts: 2}})
		})
		if !errors.Is(err, execution.ErrQueueFull) {
			t.Fatal("batch capacity not enforced", err)
		}
	})
	t.Run("concurrent action and lost response", func(t *testing.T) {
		reset(t)
		var wg sync.WaitGroup
		results := make(chan error, 12)
		for range 12 {
			wg.Go(func() {
				ref, err := store.Run(ctx, action, authorize, apply)
				if err == nil && ref != "atomic" {
					err = errors.New("wrong result")
				}
				results <- err
			})
		}
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		// Discard the committed result exactly as a disconnected HTTP client does.
		if _, err := store.Run(ctx, action, authorize, apply); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 1 {
			t.Fatalf("executions=%d", calls.Load())
		}
		action.Fingerprint[0] = 2
		if _, err := store.Run(ctx, action, authorize, apply); !errors.Is(err, execution.ErrConflict) {
			t.Fatal(err)
		}
		action.Fingerprint[0] = 1
		denied := errors.New("permission revoked")
		if _, err := store.Run(ctx, action, func(context.Context, pgx.Tx, string) error { return denied }, apply); !errors.Is(err, denied) {
			t.Fatal("replay skipped current authorization", err)
		}
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM technical_audit WHERE event='probe.created'`).Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	})
	t.Run("rollback includes audit and outbox", func(t *testing.T) {
		reset(t)
		before := calls.Load()
		failed := action
		failed.Key = "rollback"
		marker := errors.New("rollback marker")
		_, err := store.Run(ctx, failed, authorize, func(ctx context.Context, tx pgx.Tx) (string, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO execution_probe(id) VALUES ('rolled-back')`); err != nil {
				return "", err
			}
			if err := execution.Audit(ctx, tx, "test", "test", "rollback", "rolled-back", ""); err != nil {
				return "", err
			}
			if _, err := store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "rollback", MaxAttempts: 2}); err != nil {
				return "", err
			}
			return "", marker
		})
		if !errors.Is(err, marker) {
			t.Fatal(err)
		}
		for _, query := range []string{`SELECT count(*) FROM execution_probe WHERE id='rolled-back'`, `SELECT count(*) FROM jobs WHERE dedup_key='rollback'`, `SELECT count(*) FROM technical_audit WHERE event='rollback'`, `SELECT count(*) FROM action_receipts WHERE action_key='rollback'`} {
			var count int
			if err := db.Pool.QueryRow(ctx, query).Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
		}
		if calls.Load() != before {
			t.Fatal("unexpected apply")
		}
	})
	t.Run("TCP response lost after commit is replayed without another write", func(t *testing.T) {
		reset(t)
		tcpAction := action
		tcpAction.Key = "tcp-response-loss"
		var writes atomic.Int64
		var drop atomic.Bool
		drop.Store(true)
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ref, err := store.Run(r.Context(), tcpAction, authorize, func(ctx context.Context, tx pgx.Tx) (string, error) {
				writes.Add(1)
				_, err := tx.Exec(ctx, `INSERT INTO execution_probe(id) VALUES ('tcp-operation')`)
				return "tcp-operation", err
			})
			if err != nil {
				t.Error(err)
				http.Error(w, "Synthetic failure", http.StatusInternalServerError)
				return
			}
			if drop.Swap(false) {
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
			if _, err := io.WriteString(w, ref); err != nil {
				t.Error(err)
			}
		})
		server := httptest.NewServer(handler)
		defer server.Close()
		client := &http.Client{Timeout: time.Second}
		response, err := client.Post(server.URL, "application/json", nil)
		if err == nil {
			_ = response.Body.Close()
			t.Fatal("client received a response that was intentionally dropped")
		}
		response, err = client.Post(server.URL, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || string(body) != "tcp-operation" || writes.Load() != 1 {
			t.Fatal(string(body), writes.Load(), readErr, closeErr)
		}
	})
	t.Run("optimistic SQL edits", func(t *testing.T) {
		reset(t)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO execution_probe(id) VALUES ('atomic')`); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var accepted atomic.Int64
		for range 8 {
			wg.Go(func() {
				err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					tag, err := tx.Exec(ctx, `UPDATE execution_probe SET value=value+1,version=version+1 WHERE id='atomic' AND version=$1`, int64(1))
					if err != nil {
						return err
					}
					return execution.CheckVersion(tag.RowsAffected(), 1)
				})
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, execution.ErrVersion) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 1 {
			t.Fatal(accepted.Load())
		}
	})
	t.Run("lease recovery and fencing", func(t *testing.T) {
		reset(t)
		addJob(t, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "atomic", Payload: []byte(`{}`), MaxAttempts: 3})
		q := executiondb.New(db.Pool)
		job, err := q.ClaimJob(ctx, executiondb.ClaimJobParams{Queue: "heavy", LeaseMs: 1000})
		if err == nil {
			t.Fatal("null lease token accepted", job)
		}
		// Simulate a terminated process after a committed claim, not a sleeping worker.
		_, err = db.Pool.Exec(ctx, `UPDATE jobs SET state='running',attempts=1,lease_token='dead-process',lease_until=clock_timestamp()-interval '1 second' WHERE dedup_key='atomic'`)
		if err != nil {
			t.Fatal(err)
		}
		var executed atomic.Int64
		worker := execution.Worker{Store: store, Queue: "heavy", Timeout: time.Second, Lease: 2 * time.Second, Log: slog.New(slog.DiscardHandler), Handlers: map[string]execution.Handler{"probe": {Check: func(context.Context, []byte) error { return nil }, Execute: func(context.Context, string, []byte) (string, error) { executed.Add(1); return "done", nil }}}}
		if worked, err := worker.Once(ctx); err != nil || !worked {
			t.Fatal(worked, err)
		}
		if worked, err := worker.Once(ctx); err != nil || worked {
			t.Fatal(worked, err)
		}
		j, err := q.FindJob(ctx, "atomic")
		if err != nil || j.State != "succeeded" || executed.Load() != 1 || len(j.Payload) != 0 {
			t.Fatal(j.State, err)
		}
		n, err := q.FinishJob(ctx, executiondb.FinishJobParams{ID: j.ID, State: "failed"})
		if err != nil || n != 0 {
			t.Fatal("stale worker changed state", n, err)
		}
	})
	t.Run("bounded SQL cancellation", func(t *testing.T) {
		reset(t)
		short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		defer stop()
		start := time.Now()
		err := db.Transaction(short, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error { _, err := tx.Exec(ctx, "SELECT pg_sleep(5)"); return err })
		if err == nil || time.Since(start) > time.Second {
			t.Fatal("SQL ignored cancellation", err)
		}
	})
	t.Run("mail accepted and uncertain results", func(t *testing.T) {
		reset(t)
		for _, uncertain := range []bool{false, true} {
			client, received := sesCapture(t, uncertain)
			payload, err := json.Marshal(mailer.Message{To: "recipient@example.test", Subject: "Synthetic verification", Body: "Synthetic message only"})
			if err != nil {
				t.Fatal(err)
			}
			key := rand.Text()
			var id string
			err = db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
				var err error
				id, err = store.Enqueue(ctx, tx, execution.Job{Queue: "notifications", Kind: "mail", DedupKey: key, Payload: payload, MaxAttempts: 3, ExternalEffect: true})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			sender, err := mailer.NewSES(client, mailer.Options{From: "sender@example.test", Timeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			worker := execution.Worker{Store: store, Queue: "notifications", Timeout: time.Second, Lease: 2 * time.Second, Log: slog.New(slog.DiscardHandler), Handlers: map[string]execution.Handler{"mail": {Check: func(context.Context, []byte) error { return nil }, Execute: sender.Deliver}}}
			if worked, err := worker.Once(ctx); !worked || err != nil {
				t.Fatal(worked, err)
			}
			select {
			case message := <-received:
				if !strings.Contains(message, "Synthetic message only") || !strings.Contains(message, "recipient@example.test") {
					t.Fatal(message)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("mail not delivered")
			}
			status, err := store.Status(ctx, id)
			want := "succeeded"
			if uncertain {
				want = "uncertain"
			}
			if err != nil || status.State != want {
				t.Fatal(status.State, err)
			}
			if !uncertain && status.ResultRef != "ses-accepted-id" {
				t.Fatal("SES acceptance ID not persisted", status.ResultRef)
			}
			if worked, err := worker.Once(ctx); worked || err != nil {
				t.Fatal("mail resent", worked, err)
			}
		}
	})
	t.Run("expired external work persists uncertainty without other jobs", func(t *testing.T) {
		reset(t)
		var id string
		err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			var err error
			id, err = store.Enqueue(ctx, tx, execution.Job{Queue: "notifications", Kind: "external", DedupKey: "expired-external", MaxAttempts: 3, ExternalEffect: true})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET state='running',attempts=1,lease_token='terminated',lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		worker := execution.Worker{Store: store, Queue: "notifications", Timeout: time.Second, Lease: 2 * time.Second, Log: slog.New(slog.DiscardHandler)}
		if worked, err := worker.Once(ctx); worked || err != nil {
			t.Fatal(worked, err)
		}
		status, err := store.Status(ctx, id)
		if err != nil || status.State != "uncertain" {
			t.Fatal(status.State, err)
		}
		if err := store.Retry(ctx, id, "test-operator"); err == nil {
			t.Fatal("uncertain external work was blindly retried")
		}
	})
	t.Run("dedup survives completion", func(t *testing.T) {
		reset(t)
		id := addJob(t, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "atomic", Payload: []byte(`{}`), MaxAttempts: 3})
		if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET state='succeeded',payload=''::bytea WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		existing, err := executiondb.New(db.Pool).FindJob(ctx, "atomic")
		if err != nil {
			t.Fatal(err)
		}
		err = db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			id, err := store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "atomic", Payload: []byte(`{}`), MaxAttempts: 3})
			if err != nil {
				return err
			}
			if id != existing.ID {
				return errors.New("deduplication lost")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		err = db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "probe", DedupKey: "atomic", Payload: []byte(`changed`), MaxAttempts: 3})
			return err
		})
		if !errors.Is(err, execution.ErrConflict) {
			t.Fatal(err)
		}
	})
	t.Run("competing workers claim only once", func(t *testing.T) {
		reset(t)
		err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "compete", DedupKey: "compete", MaxAttempts: 3})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		var executed atomic.Int64
		worker := execution.Worker{Store: store, Queue: "heavy", Timeout: time.Second, Lease: 2 * time.Second, Log: slog.New(slog.DiscardHandler), Handlers: map[string]execution.Handler{"compete": {Check: func(context.Context, []byte) error { return nil }, Execute: func(context.Context, string, []byte) (string, error) { executed.Add(1); return "done", nil }}}}
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				if _, err := worker.Once(ctx); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if executed.Load() != 1 {
			t.Fatal(executed.Load())
		}
	})
	t.Run("safe retries exhaustion revocation and unsupported work", func(t *testing.T) {
		reset(t)
		for _, scenario := range []string{"retry", "exhausted", "revoked", "unsupported", "permanent"} {
			t.Run(scenario, func(t *testing.T) {
				var id string
				err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					var err error
					id, err = store.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: scenario, DedupKey: scenario, MaxAttempts: 2})
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				var calls int
				handler := execution.Handler{Check: func(context.Context, []byte) error {
					if scenario == "revoked" {
						return execution.ErrObsolete
					}
					return nil
				}, Execute: func(context.Context, string, []byte) (string, error) {
					calls++
					if scenario == "permanent" {
						return "", execution.ErrPermanent
					}
					if scenario == "exhausted" || calls == 1 {
						return "", errors.New("safe internal failure")
					}
					return "recovered", nil
				}}
				handlers := map[string]execution.Handler{scenario: handler}
				if scenario == "unsupported" {
					handlers = map[string]execution.Handler{}
				}
				worker := execution.Worker{Store: store, Queue: "heavy", Timeout: time.Second, Lease: 2 * time.Second, Log: slog.New(slog.DiscardHandler), Handlers: handlers}
				if worked, err := worker.Once(ctx); !worked || err != nil {
					t.Fatal(worked, err)
				}
				status, err := store.Status(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "revoked" {
					if status.State != "cancelled" || calls != 0 {
						t.Fatal(status.State, calls)
					}
					return
				}
				if scenario == "permanent" {
					if status.State != "failed" || status.ErrorCode != "permanent_failure" || status.Attempts != 1 || calls != 1 {
						t.Fatal(status, calls)
					}
					if worked, err := worker.Once(ctx); worked || err != nil {
						t.Fatal("permanent failure retried", worked, err)
					}
					return
				}
				if scenario == "unsupported" {
					if status.State != "failed" || status.ErrorCode != "unsupported_handler" {
						t.Fatal(status)
					}
					return
				}
				if status.State != "pending" || status.Attempts != 1 {
					t.Fatal(status)
				}
				if worked, err := worker.Once(ctx); worked || err != nil {
					t.Fatal("retry ignored backoff", worked, err)
				}
				if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET available_at=clock_timestamp() WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if worked, err := worker.Once(ctx); !worked || err != nil {
					t.Fatal(worked, err)
				}
				status, err = store.Status(ctx, id)
				want := "succeeded"
				if scenario == "exhausted" {
					want = "failed"
				}
				if err != nil || status.State != want {
					t.Fatal(status, err)
				}
				if scenario == "exhausted" {
					if err := store.Retry(ctx, id, "test-operator"); err != nil {
						t.Fatal(err)
					}
					if worked, err := worker.Once(ctx); !worked || err != nil {
						t.Fatal(worked, err)
					}
					if _, err := db.Pool.Exec(ctx, `UPDATE jobs SET state='failed' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	})
	t.Run("aborted transaction retries the entire callback", func(t *testing.T) {
		reset(t)
		attempts := 0
		err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			attempts++
			if _, err := tx.Exec(ctx, `INSERT INTO execution_probe(id) VALUES ('retried-transaction')`); err != nil {
				return err
			}
			if attempts == 1 {
				return &pgconn.PgError{Code: "40001"}
			}
			return nil
		})
		if err != nil || attempts != 2 {
			t.Fatal(attempts, err)
		}
	})
	t.Run("privileged bounded retention preserves dedup identities", func(t *testing.T) {
		reset(t)
		addJob(t, execution.Job{Queue: "heavy", Kind: "unsupported", DedupKey: "unsupported", MaxAttempts: 2})
		if err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			return execution.Audit(ctx, tx, "test", "test", "expired.audit", "expired", "")
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, `UPDATE technical_audit SET occurred_at=clock_timestamp()-interval '13 months' WHERE event='expired.audit'; UPDATE jobs SET state='failed',updated_at=clock_timestamp()-interval '8 days',payload='expired private payload' WHERE dedup_key='unsupported'`); err != nil {
			t.Fatal(err)
		}
		if err := store.Maintain(ctx, "test-operator"); err != nil {
			t.Fatal(err)
		}
		job, err := executiondb.New(db.Pool).FindJob(ctx, "unsupported")
		if err != nil || len(job.Payload) != 0 || len(job.Fingerprint) != 32 {
			t.Fatal(job, err)
		}
		if err := store.Retry(ctx, job.ID, "test-operator"); err == nil {
			t.Fatal("expired payload work retried")
		}
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM technical_audit WHERE event='expired.audit'`).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
	})
	t.Run("capacity is enforced transactionally", func(t *testing.T) {
		reset(t)
		small := &execution.Store{DB: db, QueueLimit: 1}
		var accepted atomic.Int64
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					_, err := small.Enqueue(ctx, tx, execution.Job{Queue: "heavy", Kind: "capacity", DedupKey: rand.Text(), MaxAttempts: 1})
					return err
				})
				if err == nil {
					accepted.Add(1)
				} else if !errors.Is(err, execution.ErrQueueFull) {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 1 {
			t.Fatal(accepted.Load())
		}
		// A saturated heavy queue cannot consume notification admission capacity.
		if err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
			_, err := small.Enqueue(ctx, tx, execution.Job{
				Queue: "notifications", Kind: "notification-capacity", DedupKey: "reserved-notification", MaxAttempts: 1,
			})
			return err
		}); err != nil {
			t.Fatal("heavy queue blocked notifications", err)
		}
	})
	t.Run("cross queue deduplication still conflicts safely", func(t *testing.T) {
		reset(t)
		var accepted, conflicts atomic.Int64
		var wg sync.WaitGroup
		for _, queue := range []string{"heavy", "notifications"} {
			wg.Go(func() {
				err := db.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
					_, err := store.Enqueue(ctx, tx, execution.Job{Queue: queue, Kind: "probe", DedupKey: "global-key", MaxAttempts: 1})
					return err
				})
				switch {
				case err == nil:
					accepted.Add(1)
				case errors.Is(err, execution.ErrConflict):
					conflicts.Add(1)
				default:
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if accepted.Load() != 1 || conflicts.Load() != 1 {
			t.Fatal(accepted.Load(), conflicts.Load())
		}
	})
	t.Run("restricted role and real HTTP shutdown", func(t *testing.T) {
		reset(t)
		password := rand.Text()
		_, err := db.Pool.Exec(ctx, `CREATE ROLE execution_app LOGIN PASSWORD '`+password+`'; GRANT CONNECT ON DATABASE execution_test TO execution_app; GRANT USAGE ON SCHEMA public TO execution_app; GRANT SELECT,INSERT,UPDATE ON jobs,action_receipts TO execution_app; GRANT SELECT,INSERT ON technical_audit TO execution_app; GRANT USAGE ON SEQUENCE technical_audit_id_seq TO execution_app; GRANT SELECT ON goose_db_version TO execution_app`)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword("execution_app", password)
		runtimeDB, err := database.Open(ctx, database.Options{
			URL: u.String(), MaxConns: 2, Timeout: time.Second, Tracer: noop.NewTracerProvider().Tracer("test"),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer runtimeDB.Pool.Close()
		if err := runtimeDB.CheckRuntimeRole(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := runtimeDB.Pool.Exec(ctx, `DELETE FROM technical_audit`); err == nil {
			t.Fatal("application can delete audit")
		}
		if _, err := runtimeDB.Pool.Exec(ctx, `UPDATE technical_audit SET event='tampered'`); err == nil {
			t.Fatal("application can update audit")
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		c, err := config.Load(func(k string) string {
			switch k {
			case "FACETS_DATABASE_URL":
				return u.String()
			case "FACETS_ADDRESS":
				return address
			case "FACETS_ACCESS_KEY":
				return strings.Repeat("21", 32)
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
		serveCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- app.Run(serveCtx, c, "serve", "", "", slog.New(slog.DiscardHandler), io.Discard) }()
		client := &http.Client{Timeout: time.Second}
		deadline := time.Now().Add(5 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			response, err := client.Get("http://" + address + "/v1/health/ready")
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == 200 {
					ready = true
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("shutdown did not finish")
		}
		if !ready {
			t.Fatal("real server never became ready")
		}
		workerCtx, stopWorker := context.WithCancel(ctx)
		// The heavy worker needs no mail provider. Notification consumers are
		// exercised separately by the access integration suite with SES over TLS.
		c.WorkerQueue = "heavy"
		workerDone := make(chan error, 1)
		go func() {
			workerDone <- app.Run(workerCtx, c, "worker", "", "", slog.New(slog.DiscardHandler), io.Discard)
		}()
		metricsReady := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			response, err := client.Get("http://" + c.DiagnosticsAddress + "/metrics")
			if err == nil {
				body, readErr := io.ReadAll(response.Body)
				closeErr := response.Body.Close()
				if readErr == nil && closeErr == nil && response.StatusCode == http.StatusOK && strings.Contains(string(body), "database.query.duration") {
					metricsReady = true
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
		stopWorker()
		select {
		case err := <-workerDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("worker shutdown did not finish")
		}
		if !metricsReady {
			t.Fatal("worker metrics never became available")
		}
	})
}

func sesCapture(t *testing.T, uncertain bool) (*sesv2.Client, <-chan string) {
	t.Helper()
	messages := make(chan string, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || r.URL.Path != "/v2/email/outbound-emails" || r.Header.Get("Authorization") == "" {
			t.Error("invalid SES request", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case messages <- string(data):
		default:
			t.Error("unexpected duplicate SES request")
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
		_, _ = io.WriteString(w, `{"MessageId":"ses-accepted-id"}`)
	}))
	t.Cleanup(server.Close)
	client := sesv2.NewFromConfig(aws.Config{Region: "us-east-1", HTTPClient: server.Client(),
		Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "synthetic", SecretAccessKey: "synthetic"}, nil
		})}, func(o *sesv2.Options) { o.BaseEndpoint = new(server.URL) })
	return client, messages
}
