// Package execution provides transactional action receipts, audit and a durable outbox.
package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"math"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stephanofer/facets-server/internal/database"
	"github.com/stephanofer/facets-server/internal/execution/executiondb"
)

var (
	ErrConflict  = errors.New("idempotency conflict")
	ErrVersion   = errors.New("edit version conflict")
	ErrQueueFull = errors.New("job queue capacity reached")
	ErrLeaseLost = errors.New("job lease lost")
)

type Store struct {
	DB         *database.DB
	QueueLimit int
}
type Action struct {
	Actor, Scope, Kind, Key string
	Fingerprint             [32]byte
}

// Fingerprint hashes typed, validated commands. The caller supplies a stable contract,
// not raw JSON bytes whose whitespace or member order may vary.
func Fingerprint(command any) ([32]byte, error) {
	b, err := json.Marshal(command, json.Deterministic(true))
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(b), nil
}

// Run always calls authorize within the transaction, including on replay. It must
// lock the current permission records shared with revocation; ref is empty before
// execution and contains the persisted result on replay. No response body is cached.
func (s *Store) Run(
	ctx context.Context,
	a Action,
	authorize func(context.Context, pgx.Tx, string) error,
	apply func(context.Context, pgx.Tx) (string, error),
) (string, error) {
	invalidActor := a.Actor == "" || len(a.Actor) > 128
	invalidScope := a.Scope == "" || len(a.Scope) > 128
	invalidKind := a.Kind == "" || len(a.Kind) > 80
	invalidKey := a.Key == "" || len(a.Key) > 128
	if invalidActor || invalidScope || invalidKind || invalidKey || authorize == nil || apply == nil {
		return "", errors.New("invalid action")
	}
	var result string
	err := s.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		if err := authorize(ctx, tx, ""); err != nil {
			return err
		}
		q := executiondb.New(tx)
		n, err := q.InsertReceipt(ctx, executiondb.InsertReceiptParams{
			Actor: a.Actor, Scope: a.Scope, Action: a.Kind, ActionKey: a.Key, Fingerprint: a.Fingerprint[:],
		})
		if err != nil {
			return err
		}
		if n == 0 {
			receipt, err := q.GetReceipt(ctx, executiondb.GetReceiptParams{Actor: a.Actor, Scope: a.Scope, Action: a.Kind, ActionKey: a.Key})
			if err != nil {
				return err
			}
			if !bytes.Equal(receipt.Fingerprint, a.Fingerprint[:]) {
				return ErrConflict
			}
			if err := authorize(ctx, tx, receipt.ResultRef); err != nil {
				return err
			}
			result = receipt.ResultRef
			return nil
		}
		result, err = apply(ctx, tx)
		if err != nil {
			return err
		}
		if result == "" || len(result) > 256 {
			return errors.New("invalid action result reference")
		}
		return q.CompleteReceipt(ctx, executiondb.CompleteReceiptParams{Actor: a.Actor, Scope: a.Scope, Action: a.Kind, ActionKey: a.Key, ResultRef: result})
	})
	if err != nil {
		return "", err
	}
	return result, nil
}

// CheckVersion is used after UPDATE ... WHERE version = expected RETURNING/execrows.
// The actual comparison/increment belongs in the same SQL statement, never a Go mutex.
func CheckVersion(rows, expected int64) error {
	if expected < 1 || expected == math.MaxInt64 || rows != 1 {
		return ErrVersion
	}
	return nil
}

func Audit(ctx context.Context, tx pgx.Tx, actor, scope, event, ref, requestID string) error {
	return executiondb.New(tx).AppendAudit(ctx, executiondb.AppendAuditParams{Actor: actor, Scope: scope, Event: event, ResourceRef: ref, RequestID: requestID})
}

type Job struct {
	Queue, Kind, DedupKey string
	Payload               []byte
	MaxAttempts           int32
	ExternalEffect        bool
}

// Enqueue must share the caller's transaction. A short database admission lock
// enforces capacity across processes and serializes deduplication before insertion.
func (s *Store) Enqueue(ctx context.Context, tx pgx.Tx, j Job) (string, error) {
	if j.Payload == nil {
		j.Payload = []byte{}
	}
	invalidQueue := j.Queue != "notifications" && j.Queue != "heavy"
	invalidKind := j.Kind == "" || len(j.Kind) > 80
	invalidKey := j.DedupKey == "" || len(j.DedupKey) > 256
	invalidAttempts := j.MaxAttempts < 1 || j.MaxAttempts > 10
	if invalidQueue || invalidKind || invalidKey || invalidAttempts || len(j.Payload) > 65536 || s.QueueLimit < 1 {
		return "", errors.New("invalid job")
	}
	fp, err := Fingerprint(struct {
		Queue, Kind string
		Payload     []byte
		Attempts    int32
		External    bool
	}{Queue: j.Queue, Kind: j.Kind, Payload: j.Payload, Attempts: j.MaxAttempts, External: j.ExternalEffect})
	if err != nil {
		return "", err
	}
	q := executiondb.New(tx)
	if err := q.QueueLock(ctx, j.Queue); err != nil {
		return "", err
	}
	old, err := q.FindJob(ctx, j.DedupKey)
	if err == nil {
		if !bytes.Equal(old.Fingerprint, fp[:]) {
			return "", ErrConflict
		}
		return old.ID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	count, err := q.CountActiveJobs(ctx, j.Queue)
	if err != nil {
		return "", err
	}
	if count >= int64(s.QueueLimit) {
		return "", ErrQueueFull
	}
	id := uuid.New().String()
	err = q.InsertJob(ctx, executiondb.InsertJobParams{
		ID: id, Queue: j.Queue, Kind: j.Kind, DedupKey: j.DedupKey, Fingerprint: fp[:],
		Payload: j.Payload, MaxAttempts: j.MaxAttempts, ExternalEffect: j.ExternalEffect,
	})
	// Different queue admission locks can race over a globally scoped key.
	// Queue is part of the fingerprint, so that collision is always a conflict.
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" && pgErr.ConstraintName == "jobs_dedup_key_key" {
		return "", ErrConflict
	}
	return id, err
}

func (s *Store) Status(ctx context.Context, id string) (executiondb.GetJobRow, error) {
	ctx, cancel := context.WithTimeout(ctx, s.DB.Timeout)
	defer cancel()
	return executiondb.New(s.DB.Pool).GetJob(ctx, id)
}

// Retry permits only known failed internal work. Uncertain external delivery
// requires reconciliation, not a blind retry. The operator is never a client actor.
func (s *Store) Retry(ctx context.Context, id, operator string) error {
	return s.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := executiondb.New(tx)
		job, err := q.GetJob(ctx, id)
		if err != nil {
			return err
		}
		if err := q.QueueLock(ctx, job.Queue); err != nil {
			return err
		}
		count, err := q.CountActiveJobs(ctx, job.Queue)
		if err != nil {
			return err
		}
		if count >= int64(s.QueueLimit) {
			return ErrQueueFull
		}
		n, err := q.RetryJob(ctx, id)
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("job is not safe to retry")
		}
		return Audit(ctx, tx, operator, "platform", "job.retry", id, "")
	})
}

// Maintain is an explicit privileged task, never called by the application role.
// Each invocation removes at most 1000 rows/payloads per category.
func (s *Store) Maintain(ctx context.Context, operator string) error {
	if operator == "" {
		return errors.New("maintenance operator required")
	}
	return s.DB.Transaction(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		q := executiondb.New(tx)
		if _, err := q.PurgeJobPayloads(ctx); err != nil {
			return err
		}
		if _, err := q.PurgeTechnicalAudit(ctx); err != nil {
			return err
		}
		return Audit(ctx, tx, operator, "platform", "retention.purge", "technical", "")
	})
}
