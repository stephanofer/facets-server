// Package database owns bounded PostgreSQL connections and transaction lifecycles.
package database

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samber/oops"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

type DB struct {
	Pool    *pgxpool.Pool
	Timeout time.Duration
	Tracer  trace.Tracer
	Retries metric.Int64Counter
}

type Options struct {
	URL      string
	MaxConns int32
	Timeout  time.Duration
	Tracer   trace.Tracer
	Meter    metric.Meter
}

func Open(ctx context.Context, o Options) (*DB, error) {
	if o.URL == "" || o.MaxConns < 2 || o.Timeout <= 0 || o.Tracer == nil {
		return nil, errors.New("invalid database configuration")
	}
	c, err := pgxpool.ParseConfig(o.URL)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	c.MaxConns, c.MinConns = o.MaxConns, 0
	c.MaxConnLifetime, c.MaxConnIdleTime = 30*time.Minute, 5*time.Minute
	c.ConnConfig.ConnectTimeout = o.Timeout
	meter := o.Meter
	if meter == nil {
		meter = noop.NewMeterProvider().Meter("database")
	}
	duration, err := meter.Float64Histogram("database.query.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	errorsCount, err := meter.Int64Counter("database.query.errors")
	if err != nil {
		return nil, err
	}
	retries, err := meter.Int64Counter("database.transaction.retries")
	if err != nil {
		return nil, err
	}
	c.ConnConfig.Tracer = queryTracer{tracer: o.Tracer, duration: duration, errors: errorsCount}
	c.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(o.Timeout.Milliseconds(), 10)
	c.ConnConfig.RuntimeParams["lock_timeout"] = strconv.FormatInt(max(1, o.Timeout.Milliseconds()/2), 10)
	c.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = strconv.FormatInt(o.Timeout.Milliseconds(), 10)
	p, err := pgxpool.NewWithConfig(ctx, c)
	if err != nil {
		return nil, oops.In("database").Wrapf(err, "create pool")
	}
	db := &DB{Pool: p, Timeout: o.Timeout, Tracer: o.Tracer, Retries: retries}
	if err := db.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return db, nil
}

// Ready verifies the actual migration sequence, not just network reachability.
func (d *DB) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	var version int64
	if err := d.Pool.QueryRow(ctx, "SELECT COALESCE(max(version_id),0) FROM goose_db_version WHERE is_applied").Scan(&version); err != nil {
		return oops.In("database.ready").Code("migrations_unavailable").Wrap(err)
	}
	if version != 1 {
		return oops.In("database.ready").Code("migration_version_mismatch").Errorf("database migration version mismatch")
	}
	return nil
}

func (d *DB) CheckRuntimeRole(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	var unsafe bool
	err := d.Pool.QueryRow(ctx, `SELECT r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolbypassrls OR
	 has_schema_privilege(current_user,'public','CREATE') OR
 EXISTS (SELECT 1 FROM pg_class c WHERE c.relname IN ('jobs','technical_audit','action_receipts') AND pg_has_role(current_user,c.relowner,'USAGE')) OR
 has_table_privilege(current_user,'technical_audit','UPDATE,DELETE,TRUNCATE')
 FROM pg_roles r WHERE r.rolname=current_user`).Scan(&unsafe)
	if err != nil {
		return oops.In("database.privileges").Code("privileges_unavailable").Wrap(err)
	}
	if unsafe {
		return oops.In("database.privileges").Code("excessive_runtime_privileges").Errorf("runtime database role has excessive privileges")
	}
	return nil
}

type queryStartKey struct{}
type queryTracer struct {
	tracer   trace.Tracer
	duration metric.Float64Histogram
	errors   metric.Int64Counter
}

func (q queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, _ = q.tracer.Start(ctx, "database.query")
	return context.WithValue(ctx, queryStartKey{}, time.Now())
}
func (q queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if start, ok := ctx.Value(queryStartKey{}).(time.Time); ok {
		q.duration.Record(ctx, time.Since(start).Seconds())
	}
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		q.errors.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "query_failed")))
		trace.SpanFromContext(ctx).SetStatus(codes.Error, "query_failed")
	}
	trace.SpanFromContext(ctx).End()
}

func (d *DB) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	return oops.In("database").Wrapf(d.Pool.Ping(ctx), "ping database")
}

// Transaction retries only aborted transactions. Callbacks must contain no external effects.
// A commit error is never automatically retried: its outcome may be unknown.
func (d *DB) Transaction(ctx context.Context, opts pgx.TxOptions, fn func(context.Context, pgx.Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, d.Timeout)
	defer cancel()
	ctx, span := d.Tracer.Start(ctx, "database.transaction")
	defer span.End()
	for attempt := 0; attempt < 3; attempt++ {
		err, committed := d.attempt(ctx, opts, fn)
		if err == nil {
			return nil
		}
		p, ok := errors.AsType[*pgconn.PgError](err)
		if committed || !ok || (p.Code != "40001" && p.Code != "40P01") || attempt == 2 {
			return oops.In("database").Wrapf(err, "transaction failed")
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
		d.Retries.Add(ctx, 1)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("transaction retry limit reached")
}

func (d *DB) attempt(ctx context.Context, opts pgx.TxOptions, fn func(context.Context, pgx.Tx) error) (err error, commitAttempted bool) {
	tx, err := d.Pool.BeginTx(ctx, opts)
	if err != nil {
		return err, false
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		rollbackErr := tx.Rollback(cleanup)
		if rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			err = errors.Join(err, rollbackErr)
		}
	}()
	if err = fn(ctx, tx); err != nil {
		return err, false
	}
	return tx.Commit(ctx), true
}
