package execution

import (
	"bytes"
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stephanofer/facets-server/internal/execution/executiondb"
)

func jobInstant(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()}
}

// EnqueueBatch admits one bounded, same-queue outbox batch under the same lock as
// Enqueue. Existing matching identities are preserved, including terminal jobs.
func (s *Store) EnqueueBatch(ctx context.Context, tx pgx.Tx, jobs []Job) error {
	if len(jobs) == 0 {
		return nil
	}
	if len(jobs) > 1000 || s.QueueLimit < 1 {
		return errors.New("invalid job batch")
	}
	queue := jobs[0].Queue
	keys := make([]string, 0, len(jobs))
	fingerprints := make(map[string][32]byte, len(jobs))
	for _, j := range jobs {
		if j.Payload == nil {
			j.Payload = []byte{}
		}
		if j.Queue != queue || queue != "notifications" && queue != "heavy" || j.Kind == "" || len(j.Kind) > 80 || j.DedupKey == "" || len(j.DedupKey) > 256 || j.MaxAttempts < 1 || j.MaxAttempts > 10 || len(j.Payload) > 65536 {
			return errors.New("invalid batch job")
		}
		if _, exists := fingerprints[j.DedupKey]; exists {
			return errors.New("duplicate batch job")
		}
		fp, err := Fingerprint(struct {
			Queue, Kind string
			Payload     []byte
			Attempts    int32
			External    bool
			AvailableAt time.Time `json:",omitzero"`
		}{Queue: j.Queue, Kind: j.Kind, Payload: j.Payload, Attempts: j.MaxAttempts, External: j.ExternalEffect, AvailableAt: j.AvailableAt})
		if err != nil {
			return err
		}
		fingerprints[j.DedupKey] = fp
		keys = append(keys, j.DedupKey)
	}
	q := executiondb.New(tx)
	if err := q.QueueLock(ctx, queue); err != nil {
		return err
	}
	existing, err := q.FindJobs(ctx, keys)
	if err != nil {
		return err
	}
	known := make(map[string]bool, len(existing))
	for _, old := range existing {
		fp := fingerprints[old.DedupKey]
		if !bytes.Equal(old.Fingerprint, fp[:]) {
			return ErrConflict
		}
		known[old.DedupKey] = true
	}
	count, err := q.CountActiveJobs(ctx, queue)
	if err != nil {
		return err
	}
	if int64(len(jobs)-len(existing)) > int64(s.QueueLimit)-count {
		return ErrQueueFull
	}
	v := executiondb.InsertJobsParams{Queue: queue}
	for _, j := range jobs {
		if known[j.DedupKey] {
			continue
		}
		fp := fingerprints[j.DedupKey]
		if j.Payload == nil {
			j.Payload = []byte{}
		}
		v.Ids = append(v.Ids, uuid.New().String())
		v.Kinds = append(v.Kinds, j.Kind)
		v.Keys = append(v.Keys, j.DedupKey)
		v.Fingerprints = append(v.Fingerprints, fp[:])
		v.Payloads = append(v.Payloads, j.Payload)
		v.Attempts = append(v.Attempts, j.MaxAttempts)
		v.External = append(v.External, j.ExternalEffect)
		v.Available = append(v.Available, jobInstant(j.AvailableAt))
	}
	if len(v.Ids) == 0 {
		return nil
	}
	err = q.InsertJobs(ctx, v)
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == "23505" && pgErr.ConstraintName == "jobs_dedup_key_key" {
		return ErrConflict
	}
	return err
}

// CancelPending invalidates a superseded intent without interfering with a
// running consumer's lease. Consumers must still revalidate their current input.
func (s *Store) CancelPending(ctx context.Context, tx pgx.Tx, key string) error {
	return executiondb.New(tx).CancelPendingJob(ctx, key)
}
