# Shared execution foundation

This block has no financial or identity endpoints. Synthetic integration probes
exist only in test databases, never in migrations or production HTTP routes.

## Run and integrate

```powershell
./scripts/dev.ps1 setup
./scripts/dev.ps1 db-up
./scripts/dev.ps1 db-config
./scripts/dev.ps1 run
# In another PowerShell window:
Invoke-RestMethod http://127.0.0.1:8080/v1/health/ready
```

- `GET /v1/health/live`: process is serving requests.
- `GET /v1/health/ready`: database is available with the expected schema version.
- `GET /v1/openapi.json` and `/v1/openapi.yaml`: generated technical contract.
- `/metrics` is **not** on the client router. Diagnostics listen separately at
  `127.0.0.1:9090/metrics`, with OpenTelemetry metric snapshots and pool statistics.
  Both API and worker modes expose diagnostics; assign each process its own
  loopback `FACETS_DIAGNOSTICS_ADDRESS` (for example port 9091 for a worker).
  Keep this listener inaccessible outside the trusted host/network namespace.
  No profiling endpoint is enabled.

Every response receives a server-generated `X-Request-ID`. Errors use RFC 9457
with `type`, `title`, `status`, stable `code` and `request_id`. Validation errors
do not echo input, driver errors or secrets. Future resource contracts can add
allowlisted affected field names via `fields`; raw Huma validation values must
never pass through. Version and idempotency conflicts have distinct codes.

Development uses HTTP on loopback. Public deployment **requires HTTPS termination
at a trusted ingress**, no bypass to the backend listener, restricted diagnostics,
and PostgreSQL `sslmode=verify-full`. This block is not public-service acceptance.
No client-controlled forwarded headers are used for authority or logging.

Production supplies `FACETS_DATABASE_URL` to runtime processes and
`FACETS_MIGRATION_URL` only to privileged migration/maintenance tasks. Development
scripts derive these independently from ignored `.env` credentials. The Go binary
does not load `.env`. Do not give the migration secret to the HTTP/worker process.
The SMTP adapter requires STARTTLS except for explicitly loopback test delivery.
No email credentials or public send endpoint are installed by this foundation.

## Resource budgets

Defaults are starting limits, not measured capacity promises. Environment values
are validated before opening resources; zero/unbounded values are rejected.

| Resource | Default | Constraint |
| --- | --- | --- |
| PostgreSQL pool per process | 8 | Configurable 2–100; count API **and** worker processes against server capacity. |
| Pool idle/lifetime | 5/30 minutes | No permanently retained connection requirement. |
| SQL/transaction deadline | 5 seconds | Includes pool acquisition, locks, callback and commit. |
| PostgreSQL lock timeout | Half SQL deadline | Statement and idle-in-transaction timeouts also installed. |
| HTTP request/read timeout | 10 seconds | Context propagated to the complete call chain. |
| Header timeout / max bytes | 5 seconds / 16 KiB | Idle keepalive: 60 seconds. |
| In-flight HTTP handlers | 64 | Immediate safe 503 when saturated; no waiting request queue. |
| Request body | 1 MiB | Configurable up to 16 MiB; this block accepts no business request bodies. |
| Shutdown | 15 seconds | Stop accepting HTTP, drain, then force close if necessary. |
| Worker concurrency | 2 per selected queue | Configurable 1–32. Separate worker instances for heavy and notification queues. |
| Job execution / lease | 20/30 seconds | Lease must exceed execution timeout; handlers must honor context. |
| Pending/running jobs per queue | 10,000 | Admission serialized per queue in PostgreSQL; heavy work cannot consume notification capacity. |
| Job payload | 64 KiB | At most 10 attempts; consumers never load the entire queue. |
| Recovery / retention batches | 100 / 1,000 | Bounded database work per invocation. |
| Transaction retries | 3 total attempts | Only aborted serialization/deadlock callbacks, 10/20 ms cancellable waits. |

The configured lease also reserves at least one SQL deadline for completion.
`FACETS_WORKER_QUEUE` selects `notifications` (default) or `heavy`; heavy work
does not consume notification worker slots. Explicit process environment settings
override development `.env` defaults.

Do not perform external effects in transaction callbacks. A commit error is not
automatically retried because its outcome may be unknown. `REPEATABLE READ,
READ ONLY` can be supplied for bounded coherent multi-query views. Financial
resource locking and authorization rules will be added with their actual resources.

## Atomic actions, audit and edits

`execution.Store.Run` receives actor, explicit scope, action type, key and SHA-256
fingerprint of a validated typed command. A composite primary key serializes
concurrent attempts. The receipt, callback writes, audit and enqueued work commit
together; only then does the function return a successful reference. A process
interrupted before commit leaves no receipt/effects. A client that loses its
response after commit recovers the same reference without executing again.

The required authorizer runs inside the transaction, even on replay. Resource
modules must implement current authorization and coordinate its locks with
revocation. Replays reauthorize the saved reference and load current authorized
data; there is no reusable cached sensitive response. The foundation does not
pretend anonymous test actors are real identities.

Optimistic writes use `UPDATE ... WHERE version = expected` and increment the
version **in SQL**. `CheckVersion` translates a non-single-row update or exhausted
version into a distinct conflict. There is no universal CRUD/version table.

`technical_audit` records only actor/scope/event/resource/request references and
time. Application grants allow insert/read, never update/delete/truncate; owner
and privileged operators remain capable of changes, so this is not described as
tamper-proof. Financial history is not stored in this technical audit table.

## Durable work and mail

The `jobs` table is the transactional outbox and queue; no second queue/bus is
needed. Producers enqueue in the same transaction as their facts. Identical
deduplication identities return the original job; changed content conflicts.
Admission has a short database lock and capacity check across processes.

Workers claim one row using `FOR UPDATE SKIP LOCKED` and a unique lease token,
commit the claim, then execute outside the transaction. Completion is fenced by
token **and** unexpired lease. Crashed internal work can be reclaimed; expired
external work becomes `uncertain`, not blindly delivered again. Safe failures
retry after capped exponential waits; exhausted/unsupported work is visibly
failed. Authorization checks run immediately before handling. An obsolete or
revoked resource can cancel its job without delivery. Exceptions/panics do not
terminate the worker silently.

The reusable SMTP adapter sends real RFC-style messages with stable Message-ID,
bounded connection/execution deadlines and optional authenticated STARTTLS.
Errors before DATA can be retried; errors during DATA/final acceptance become
uncertain. A 250 DATA acceptance is success even if the later connection closes.
Message-ID is diagnostic evidence, **not a guarantee of provider deduplication**.
The integration suite uses a real loopback SMTP receiver and verifies both
received content and dropped acknowledgements, without contacting a real mailbox.

The worker executable intentionally registers no permissive mail handlers before
identity exists. Block 3 will register verification/recovery handlers with real
recipient/token validity checks. There is no fake business action or no-op sender
to make this foundation look functional.

Operational modes of the built binary (use trusted OS/database access, not a
client token; do not expose these commands through HTTP):

```powershell
./artifacts/facets-server.exe -mode worker
./artifacts/facets-server.exe -mode job-status -job '<reference>'
./artifacts/facets-server.exe -mode job-retry -job '<reference>' -operator '<real-operator>'
# Privileged task with a separately supplied FACETS_MIGRATION_URL:
./artifacts/facets-server.exe -mode maintenance -operator '<real-operator>'
```

`job-status` excludes payloads, recipient data and dedup keys. Retry is audited and
limited to recent, definitely failed internal jobs; uncertain external effects
must be reconciled before any domain-specific resolution. Administration HTTP
and its identity/permissions belong to blocks 3 and 13, not an anonymous jobs API.

## Retention and recovery

- Technical audit: 12 months. Privileged `maintenance` deletes up to 1,000 expired
  records per invocation and audits the purge. Runtime credentials cannot purge it.
- Terminal job payloads: clear immediately on success/cancellation, or after 7 days
  on failure/uncertainty through maintenance. Failed internal jobs older than that
  cannot be blindly retried. Never put raw credentials in job payloads; token-based
  modules must enqueue references and obtain only still-valid delivery data.
- Action receipts and job deduplication tombstones: retain for the owning scope's
  lifetime, without cached HTTP bodies. No verifier is silently purged while its
  resource is alive. Scope deletion/identity minimization joins the lifecycle of
  actual resources in blocks 3/13; there is no claim of idempotency after deletion.
- JSON operational logs/traces: deployment log retention must be 30 days. These
  are stdout streams, not a second private log database implemented in the app.

Goose runs only as an explicit task, with embedded immutable SQL. Local provisioning
does not delete existing volumes or reinitialize financial data. Migration roles
own the tables; the runtime is checked against excessive privileges. Use forward
corrections after release, never edit a published migration.

## Exact money and calendar

Amounts are signed int64 minor units plus validated ISO currency and standard
scale (`x/text/currency` data). Currency metadata is code, never a locale's cash
rounding rule. API JSON uses strings: `{"amount_minor":"12000","currency":"PEN"}`.
Numeric JSON, noncanonical integers, extra capture precision and arithmetic
overflow fail; nothing silently truncates. Negative and zero amounts remain valid.
Unknown/estimated/confirmed business states will be represented by their resource
contracts, not by silently converting absent amounts to zero.

Presentation conversions use `apd`, bounded precision/exponents, positive finite
rates and half-even rounding **once at the final target minor unit**. They cannot
invent effective account charges. Currency/date/source references are introduced
with actual accounts in block 4. Split residuals are deterministic and preserve
the original total, including negative int64 limits.

Civil dates are validated Gregorian dates, not instants. Zones use embedded IANA
data, never the host's `Local` zone. Month offsets derive from the original nominal
anchor and clip only each result; periods are `[start,end)`. February clipping
does not shift March or subsequent leap years. Reminder time resolution selects
the first repeated instant or the first valid subsequent minute in a gap. Actual
recurrence generation and its duplicate occurrence identities belong to block 6.

## Observability and verification

Providers are injected, never process-global service locators. OpenTelemetry
records HTTP counts/latency/status, SQL latency/errors/retries and worker completion;
diagnostics include pool acquisition duration and saturation. TraceContext is
propagated; sampled spans export allowlisted metadata to structured logs using a
bounded batch processor. Exported spans never contain SQL, attributes/events,
URLs, bodies or private descriptions. Operational error logging retains safe
stack locations/SQLSTATE rather than serializing raw driver errors.

`./scripts/dev.ps1 validate` is the closure gate: formatting, tidy/module integrity,
sqlc/OpenAPI drift, Goose validation, vet/lint/security, deterministic tests, real
PostgreSQL concurrency/recovery/SMTP/HTTP integration, Linux race detector,
govulncheck and artifact inspection. Docker failures do not skip integration.
The tests use no real financial data and no local `.env` credentials.

### Requirement-to-evidence map

| Foundation | Executable evidence |
| --- | --- |
| Configuration and privacy | `TestLoad`, `TestTechnicalContract`, `TestSignalsExcludeSensitiveAttributes`. |
| Money, precision and calendar | `TestAmounts`, `TestConversionAndWire`, `FuzzMinorRoundTrip`, `TestDateAndAnchors`, `TestZoneResolution`, `FuzzDate`. |
| Atomic receipt/write/audit/outbox | `TestExecutionGuarantees/concurrent_action_and_lost_response` and `/rollback_includes_audit_and_outbox`. |
| Lost HTTP response after real commit | `/TCP_response_lost_after_commit_is_replayed_without_another_write`: real TCP connection dropped, then command replayed. This route exists only in the test fixture. |
| Edits, deadlines and transaction retries | `/optimistic_SQL_edits`, `/bounded_SQL_cancellation`, `/aborted_transaction_retries_the_entire_callback`. |
| Durable recovery, fencing and uncertainty | `/lease_recovery_and_fencing`, `/expired_external_work_persists_uncertainty_without_other_jobs`. |
| Deduplication and bounded concurrent admission | `/dedup_survives_completion`, `/competing_workers_claim_only_once`, `/capacity_is_enforced_transactionally`, `/cross_queue_deduplication_still_conflicts_safely`. |
| Retry/exhaustion, revocation and unsupported handlers | `/safe_retries_exhaustion_revocation_and_unsupported_work`. |
| Real mail and uncertain acknowledgement | `/mail_accepted_and_uncertain_results`: actual SMTP dialogue and captured message; no network mailbox provider. |
| Audit protection and retention | `/privileged_bounded_retention_preserves_dedup_identities`, `/restricted_role_and_real_HTTP_shutdown`. |
| Real execution, diagnostics and shutdown | `/restricted_role_and_real_HTTP_shutdown`: restricted role, real HTTP readiness, worker diagnostics, cancellation and joined shutdown. |
| HTTP composition, backpressure and failure paths | `TestRegisteredCommandsUseTheSharedBoundary`, `TestSaturatedHTTPDoesNotQueueRequests`, `TestCancellationAndPanic`, `TestWorkerRunCancellation`. |

Integration scenarios initialize their own synthetic fixture state and can be
selected independently using Go's `-run`; they do not rely on a previous scenario
having created an action or job. The original block 1 environment suite remains
part of regression validation.

Module composition registers typed operations on `API.Huma` before starting
HTTP, using `ErrorResponses` for the common error schema. The shared middleware
is not limited to health-check verbs: test-only registered commands verify POST,
sanitized validation, streaming body limits and stable operation labels. No such
synthetic command is registered by the production executable.

### Closure evidence — 2026-10-29

- `./scripts/dev.ps1 validate`: all gates passed, including PostgreSQL integration,
  Linux `-race`, sqlc/OpenAPI reproducibility, module integrity and artifact inspection.
- Selected lease/deduplication/retention scenarios passed without running their
  preceding integration scenarios.
- Short fuzz runs passed: `FuzzMinorRoundTrip` (44,319 executions) and `FuzzDate`
  (119,097 executions), 3 seconds and 2 workers each. These are additional checks,
  not a proof that every input or production load has been exhausted.
- A clean `git checkout-index` export (no ignored `.env`, local tooling/reference
  trees or existing build output) built with `-mod=readonly -trimpath`; its binary
  reported `facets-server 0.2.0`.
- `govulncheck -show verbose -tags=integration -test ./...`: zero reachable
  vulnerabilities and zero vulnerabilities in imported packages. Module-only notice
  [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) concerns the unmaintained
  `golang.org/x/crypto/openpgp` package. The project does not import OpenPGP; there
  is no fixed version for that deprecated package. Keep the agreed x/crypto pin,
  use only the supported primitives needed by the product, and rescan on changes.

The block is an execution foundation, not authorization to launch publicly or a
claim that the product's future financial/identity features already exist.
