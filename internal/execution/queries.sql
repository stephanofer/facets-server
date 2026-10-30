-- name: InsertReceipt :execrows
INSERT INTO action_receipts (actor, scope, action, action_key, fingerprint, result_ref)
VALUES ($1,$2,$3,$4,$5,'') ON CONFLICT DO NOTHING;

-- name: GetReceipt :one
SELECT fingerprint, result_ref FROM action_receipts
WHERE actor=$1 AND scope=$2 AND action=$3 AND action_key=$4 FOR UPDATE;

-- name: CompleteReceipt :exec
UPDATE action_receipts SET result_ref=$5 WHERE actor=$1 AND scope=$2 AND action=$3 AND action_key=$4;

-- name: AppendAudit :exec
INSERT INTO technical_audit (actor, scope, event, resource_ref, request_id) VALUES ($1,$2,$3,$4,$5);

-- name: QueueLock :exec
SELECT pg_advisory_xact_lock(18182026, CASE WHEN sqlc.arg(queue)::text='notifications' THEN 1 ELSE 2 END);

-- name: CountActiveJobs :one
SELECT count(*) FROM jobs WHERE queue=$1 AND state IN ('pending','running');

-- name: InsertJob :exec
INSERT INTO jobs (id,queue,kind,dedup_key,fingerprint,payload,max_attempts,external_effect)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8);

-- name: FindJob :one
SELECT * FROM jobs WHERE dedup_key=$1;

-- name: GetJob :one
SELECT id, queue, kind, state, attempts, max_attempts, result_ref, error_code, created_at, updated_at
FROM jobs WHERE id=$1;

-- name: RecoverJobs :many
WITH expired AS (
 SELECT id FROM jobs WHERE state='running' AND lease_until < clock_timestamp()
 ORDER BY lease_until,id FOR UPDATE SKIP LOCKED LIMIT 100
)
UPDATE jobs j SET
state=CASE WHEN external_effect THEN 'uncertain' WHEN attempts >= max_attempts THEN 'failed' ELSE 'pending' END,
error_code='lease_expired', lease_until=NULL, lease_token=NULL, updated_at=clock_timestamp()
FROM expired WHERE j.id=expired.id RETURNING j.id,j.state;

-- name: ClaimJob :one
WITH next AS (
 SELECT candidate.id FROM jobs candidate WHERE candidate.queue=$1 AND candidate.state='pending' AND candidate.available_at <= clock_timestamp()
 ORDER BY candidate.available_at,candidate.id FOR UPDATE SKIP LOCKED LIMIT 1
)
UPDATE jobs j SET state='running', attempts=attempts+1, lease_token=$2,
lease_until=clock_timestamp() + sqlc.arg(lease_ms)::bigint * interval '1 millisecond', updated_at=clock_timestamp()
FROM next WHERE j.id=next.id RETURNING j.*;

-- name: FinishJob :execrows
UPDATE jobs SET state=$3, result_ref=$4, error_code=$5,
payload=CASE WHEN $3 IN ('succeeded','cancelled') THEN ''::bytea ELSE payload END,
available_at=clock_timestamp() + sqlc.arg(delay_ms)::bigint * interval '1 millisecond',
lease_token=NULL, lease_until=NULL, updated_at=clock_timestamp()
WHERE id=$1 AND lease_token=$2 AND state='running' AND lease_until > clock_timestamp();

-- name: RetryJob :execrows
UPDATE jobs SET state='pending', attempts=0, error_code='', available_at=clock_timestamp(), updated_at=clock_timestamp()
WHERE id=$1 AND state='failed' AND NOT external_effect AND updated_at > clock_timestamp()-interval '7 days';

-- name: PurgeJobPayloads :execrows
UPDATE jobs SET payload=''::bytea WHERE id IN (
 SELECT id FROM jobs WHERE state IN ('succeeded','cancelled','failed','uncertain')
 AND updated_at <= clock_timestamp()-interval '7 days' AND octet_length(payload)>0
 ORDER BY updated_at,id LIMIT 1000
);

-- name: PurgeTechnicalAudit :execrows
DELETE FROM technical_audit WHERE id IN (
 SELECT id FROM technical_audit WHERE occurred_at < clock_timestamp()-interval '12 months'
 ORDER BY occurred_at,id LIMIT 1000
);
