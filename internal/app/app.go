// Package app composes runtime resources explicitly, with no package-level connections.
package app

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/stephanofer/facets-server/internal/access"
	"github.com/stephanofer/facets-server/internal/config"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution"
	"github.com/stephanofer/facets-server/internal/finance"
	"github.com/stephanofer/facets-server/internal/httpapi"
	"github.com/stephanofer/facets-server/internal/mail"
	"github.com/stephanofer/facets-server/internal/telemetry"
	"github.com/stephanofer/facets-server/migrations"
	"go.opentelemetry.io/otel/metric"
)

func Migrate(ctx context.Context, dsn string) (err error) {
	if dsn == "" {
		return errors.New("FACETS_MIGRATION_URL is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return errors.New("invalid migration configuration")
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Minute)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockTimeout(1, 30), lock.WithUnlockTimeout(1, 5))
	if err != nil {
		return err
	}
	files, err := migrations.Files()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithSessionLocker(locker))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}

func OpenAPI(w io.Writer) error {
	signals := telemetry.New(slog.New(slog.DiscardHandler))
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = signals.Shutdown(ctx)
	}()
	api, err := httpapi.New(httpapi.Options{Ping: func(context.Context) error { return nil }, Log: slog.New(slog.DiscardHandler), Tracer: signals.Tracer(), Meter: signals.Metrics.Meter("facets"), Timeout: time.Second, MaxBody: 1 << 20})
	if err != nil {
		return err
	}
	access.RegisterHTTP(api.Huma, nil, slog.New(slog.DiscardHandler))
	finance.RegisterHTTP(api.Huma, nil, slog.New(slog.DiscardHandler))
	return json.MarshalWrite(w, api.Schema, json.Deterministic(true))
}

func Run(ctx context.Context, c config.Config, mode, id, operator string, log *slog.Logger, out io.Writer) error {
	needsIdentity := mode == "serve" || (mode == "worker" && c.WorkerQueue == "notifications")
	if needsIdentity {
		if err := c.Access.ValidateRuntime(); err != nil {
			return errors.New("invalid access runtime configuration; FACETS_ACCESS_KEY is required")
		}
	}
	signals := telemetry.New(log)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := signals.Shutdown(cleanup); err != nil {
			log.Error("telemetry shutdown failed", "code", "telemetry_shutdown_failed")
		}
	}()
	dsn := c.DatabaseURL
	if mode == "maintenance" {
		dsn = c.MigrationURL
	}
	db, err := database.Open(ctx, database.Options{
		URL: dsn, MaxConns: c.PoolMax, Timeout: c.SQLTimeout,
		Tracer: signals.Tracer(), Meter: signals.Metrics.Meter("facets"),
	})
	if err != nil {
		return err
	}
	defer db.Pool.Close()
	if err := db.Ready(ctx); err != nil {
		return err
	}
	store := &execution.Store{DB: db, QueueLimit: c.QueueLimit}
	if mode == "maintenance" {
		if err := store.Maintain(ctx, operator); err != nil {
			return err
		}
		return access.MaintainSecrets(ctx, db)
	}
	if err := db.CheckRuntimeRole(ctx); err != nil {
		return err
	}
	switch mode {
	case "job-status":
		status, err := store.Status(ctx, id)
		if err != nil {
			return err
		}
		return json.MarshalWrite(out, struct {
			ID, State, ResultRef, ErrorCode string
			Attempts                        int32
		}{status.ID, status.State, status.ResultRef, status.ErrorCode, status.Attempts})
	case "job-retry":
		if operator == "" {
			return errors.New("operator is required")
		}
		return store.Retry(ctx, id, operator)
	case "worker":
		handlers := map[string]execution.Handler{}
		if c.WorkerQueue == "notifications" {
			identities, err := access.New(store, c.Access)
			if err != nil {
				return err
			}
			sender, err := mail.LoadSender(ctx, c.Environment, mail.Options{From: c.MailFrom, Timeout: min(10*time.Second, c.JobTimeout)}, log)
			if err != nil {
				return err
			}
			handlers["access.mail"] = identities.MailHandler(sender)
			handlers["planning.reminder"] = finance.New(identities, store).ReminderHandler()
		}
		counter, err := signals.Metrics.Meter("facets").Int64Counter("jobs.completed")
		if err != nil {
			return err
		}
		duration, err := signals.Metrics.Meter("facets").Float64Histogram("jobs.duration", metric.WithUnit("s"))
		if err != nil {
			return err
		}
		age, err := signals.Metrics.Meter("facets").Float64Histogram("jobs.age", metric.WithUnit("s"))
		if err != nil {
			return err
		}
		w := execution.Worker{Store: store, Queue: c.WorkerQueue, Concurrency: c.Workers, Timeout: c.JobTimeout, Lease: c.Lease, Poll: time.Second, Handlers: handlers, Log: log, Completed: counter}
		w.Duration, w.Age = duration, age
		return runWorker(ctx, c, &w, db, signals)
	case "serve":
		return serve(ctx, c, db, signals, log)
	default:
		return errors.New("unknown runtime mode")
	}
}

func serve(ctx context.Context, c config.Config, db *database.DB, signals *telemetry.Signals, log *slog.Logger) error {
	api, err := httpapi.New(httpapi.Options{Ping: db.Ready, Log: log, Tracer: signals.Tracer(), Meter: signals.Metrics.Meter("facets"), Timeout: c.RequestTimeout, MaxBody: c.MaxBody})
	if err != nil {
		return err
	}
	store := &execution.Store{DB: db, QueueLimit: c.QueueLimit}
	identities, err := access.New(store, c.Access, finance.SeedDefaults)
	if err != nil {
		return err
	}
	access.RegisterHTTP(api.Huma, identities, log)
	finance.RegisterHTTP(api.Huma, finance.New(identities, store), log)
	listener, err := net.Listen("tcp", c.Address)
	if err != nil {
		return errors.New("HTTP listener unavailable")
	}
	// Diagnostics are deliberately on a separate loopback listener, never on the
	// client router. Network isolation is mandatory when deployed in containers.
	diagnostics, err := net.Listen("tcp", c.DiagnosticsAddress)
	if err != nil {
		return errors.Join(errors.New("diagnostics listener unavailable"), listener.Close())
	}
	metrics := diagnosticHandler(db, signals)
	server := &http.Server{Handler: api.Handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.RequestTimeout, WriteTimeout: c.RequestTimeout + time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelError)}
	diagnosticServer := &http.Server{Handler: metrics, ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4096}
	results := make(chan error, 2)
	go func() { results <- server.Serve(listener) }()
	go func() { results <- diagnosticServer.Serve(diagnostics) }()
	log.Info("server started", "address", listener.Addr().String())
	consumed := 0
	select {
	case <-ctx.Done():
	case err = <-results:
		consumed++
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	shutdownErr := server.Shutdown(shutdown)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, server.Close())
	}
	diagnosticErr := diagnosticServer.Shutdown(shutdown)
	if diagnosticErr != nil {
		diagnosticErr = errors.Join(diagnosticErr, diagnosticServer.Close())
	}
	for range 2 - consumed {
		result := <-results
		if !errors.Is(result, http.ErrServerClosed) {
			err = errors.Join(err, result)
		}
	}
	log.Info("server stopped")
	return errors.Join(err, shutdownErr, diagnosticErr)
}

func diagnosticHandler(db *database.DB, signals *telemetry.Signals) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		snapshot, err := signals.Collect(r.Context())
		if err != nil {
			http.Error(w, "Metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		stats := db.Pool.Stat()
		_ = json.MarshalWrite(w, struct {
			Metrics                 any `json:"metrics"`
			PoolTotal, PoolAcquired int32
			PoolWaitNanos           int64
		}{Metrics: snapshot, PoolTotal: stats.TotalConns(), PoolAcquired: stats.AcquiredConns(), PoolWaitNanos: stats.AcquireDuration().Nanoseconds()})
	})
}

func runWorker(ctx context.Context, c config.Config, w *execution.Worker, db *database.DB, signals *telemetry.Signals) error {
	listener, err := net.Listen("tcp", c.DiagnosticsAddress)
	if err != nil {
		return errors.New("worker diagnostics listener unavailable")
	}
	server := &http.Server{Handler: diagnosticHandler(db, signals), ReadHeaderTimeout: time.Second,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4096}
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	results := make(chan error, 2)
	go func() { results <- server.Serve(listener) }()
	go func() { results <- w.Run(workerCtx) }()
	consumed := 0
	select {
	case <-ctx.Done():
	case err = <-results:
		consumed++
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer cancel()
	closeErr := server.Shutdown(shutdown)
	if closeErr != nil {
		closeErr = errors.Join(closeErr, server.Close())
	}
	for range 2 - consumed {
		result := <-results
		if !errors.Is(result, http.ErrServerClosed) {
			err = errors.Join(err, result)
		}
	}
	return errors.Join(err, closeErr)
}

func Version(w io.Writer) error { _, err := fmt.Fprintln(w, "facets-server 0.4.0"); return err }
