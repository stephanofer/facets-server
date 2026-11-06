# Identity, spaces and access control

Block 3 adds the backend's real identity and multispaced authorization boundary.
The authoritative request/response schemas are in [OpenAPI](openapi.json).
There are no Android screens, financial accounts, payment checkout, support
impersonation or public administrative-registration endpoints in this block.

## Running and upgrading

```powershell
./scripts/dev.ps1 db-up
./scripts/dev.ps1 db-config
./scripts/dev.ps1 run
```

`db-config` preserves existing local configuration, creates `FACETS_ACCESS_KEY`
when absent, applies migrations through **3** explicitly and grants runtime permissions.
Migrations 1 and 2 are unchanged. Migration 3 removes session-level space selection
without revoking existing sessions. The application rejects an incompatible schema and
does not migrate at startup. Upgrade tests retain an existing block-2 receipt;
the execution regression also migrates an empty database through version 3.

The API needs a 32-byte random key encoded as 64 hexadecimal characters. Supply
it outside source control, separately from PostgreSQL backups, and identically
to API and notification workers. Do not replace it casually: existing factors
and pending delivery material need the original key. Key rotation requires a
controlled decrypt/re-encrypt migration and preservation of the old key until
completion. Use independent keys per environment. AES-GCM random-nonce usage
must remain below the standard library's 2^32-encryptions-per-key limit.

For real mail, set `FACETS_MAIL_FROM` to the verified sender and configure the
AWS SDK's normal region/credential chain, then run:

```powershell
./scripts/dev.ps1 worker
```

There is no permissive fallback sender. A notification worker refuses missing
sender/region/key configuration. A heavy-queue worker does not initialize SES.
The API can enqueue without AWS credentials; a running, correctly configured
notification worker is necessary for real users to receive confirmations.
See [SES operations](EMAIL.md) for identity, sandbox, DNS, least-privilege and
production prerequisites. Tests use the actual SDK against local HTTPS, not a
live AWS account or a claim of inbox deliverability.

HTTP is permitted on loopback for development only. Public operation requires
HTTPS at the trusted ingress, no direct backend bypass, protected PostgreSQL
connections and restricted diagnostics. Origin limits use the direct peer,
**not arbitrary forwarded headers**. Account for shared ingress addresses when
configuring limits; this implementation does not establish a forwarded-header
trust policy by itself.

## Identity and credential flows

1. `POST /v1/auth/register` accepts email, password, name, currency, IANA zone and
   locale. It acknowledges a request without revealing whether the mailbox exists.
   An existing identity's password is never replaced by another registration.
2. The durable worker delivers the verification code. `POST /v1/auth/verify`
   consumes it and creates the personal space, owner membership, initial
   capabilities and audit in one transaction. An unverified identity cannot log
   in. Retrying successful verification does not create another space/session.
3. `POST /v1/auth/login` accepts email, password, device label and, when configured,
   TOTP or a recovery code. The result contains the opaque bearer token, session
   reference and absolute expiration. Tokens are returned only to the caller.
4. `/v1/auth/verification` and `/v1/auth/recovery` request replacement email codes
   with a cooldown and generic acknowledgements. Replacement invalidation and
   actual consumption are distinct persisted states. Codes expire, and neither
   a replaced nor expired unused code completes the action.
5. `/v1/auth/password-reset` requires the recovery token, new password **and the
   configured factor**. Recovery does not disable MFA. It consumes the token,
   invalidates remaining recovery requests, revokes sessions and queues a security
   notice atomically.
6. `/v1/auth/reauthenticate` proves password and any configured factor, returns a
   fresh session and revokes the old token. Credential/factor changes, ownership
   transfer, billing claims and removal require sufficiently recent authentication.

Emails are bare ASCII mailboxes, normalized to lowercase; international domains
can use punycode. No provider-specific dot/plus-address rewriting is performed.
Login uses a dummy Argon2 hash for unknown identities. Registration and public
mail requests have a cancellable minimum response duration. These reduce
enumeration differences; they are not a promise of constant network timing.
Rate counters commit independently of failed credential transactions, are shared
across processes, expire and never suspend an identity automatically.

Passwords accept long Unicode phrases without composition rules, truncation or
periodic-rotation requirements: at least 15 characters, at most 1,024 UTF-8 bytes.
Argon2id stores version, parameters and a fresh 16-byte salt with each hash.
KDF admission is bounded; overload rejects rather than queueing unbounded work.
The bundled common/compromised-password corpus is local; passwords are never
sent to an external screening service. This is a finite blocklist, not a claim
to recognize every compromised password.

Corpus: SecLists `Passwords/Common-Credentials/10k-most-common.txt`, commit
`913b327317496d062bcc7cace524aaad8a693be2`, SHA-256
`68782d6a4a19a4768d5f15dd66bd534e7a33055cc755411e33f16d18c50fdcce`.
Its [MIT license](../internal/access/SECLISTS-LICENSE.txt) is included; those
public known-password examples are deliberately not operational credentials.
The unit suite pins the digest and checks the entire corpus against the policy.
Direct dependencies retain the agreed x/crypto 0.58.0 (BSD-3-Clause) and add
pquerna/otp 1.5.0 (Apache-2.0); its barcode dependency is MIT licensed.

## Sessions and MFA

- Sessions have 32 random bytes, with independent customer/staff prefixes, and
  only SHA-256 verifiers in PostgreSQL. Authentication requires the correct realm,
  active identity, unrevoked session, inactivity deadline and absolute deadline
  **on every protected request**. A device label is not an identity proof.
- `/v1/me/sessions` lists only the caller's active sessions. Logout revokes the
  current session; `/v1/me/sessions/revoke` accepts an explicit session ID and
  `all` boolean. Another user's reference cannot revoke that user's sessions.
- MFA setup returns an `otpauth` URI after recent authentication. The old factor,
  if any, remains effective until the new secret's code is confirmed. Pending
  enrollment expires. Confirmation revokes all sessions and returns ten fresh
  recovery codes once; a lost response can be resolved by TOTP login and explicit
  regeneration, not by storing/replaying plaintext recovery codes.
- TOTP uses 30-second steps, a ±1-step acceptance window and an atomic last-used
  step. Recovery codes contain 32 random bytes, are hashed and consumed once under
  the same identity lock. Concurrent attempts cannot reuse a factor.
- AES-GCM with a standard-library random nonce encrypts factors, binding each to
  its identity as authenticated data. Factor ciphertext is not a password hash.
  Client MFA disable/regeneration requires recent authentication and notifications;
  internal MFA cannot be disabled through the API.
- `/v1/me/email` verifies the new mailbox. Confirmation additionally requires the
  same recently authenticated identity, preserves MFA and revokes sessions.
  Notices may go to the former mailbox for security, without financial content.
- `/v1/me/preferences` updates name, locale, zone and optional-notification
  preference with an expected version. Invitation delivery rechecks that preference.
  Explicit verification/recovery and security notices are not optional reminders.

Credential changes revoke sessions, so retrying with a now-revoked token is not
authorization to recover sensitive output. Login, enrollment secrets and recovery
codes are deliberately not cached as HTTP responses. The client must preserve
confirmation/result state and reauthenticate when needed.

## Spaces, collaboration and permissions

`/v1/spaces` creates additional spaces and lists current memberships with stable
UUID cursors (maximum page size 100). The listing applies current effective read
capabilities in SQL before pagination; restricted/expired spaces are not exposed
through a separate unchecked listing. Financial context is explicit in routes.
The client stores its selected space locally. There is no selection endpoint or
selected-space state in the session/profile. Every space-scoped request identifies
the space explicitly and revalidates access; client selection never grants permission.

Space settings include purpose, base presentation currency, zone, locale,
nominal period day and proposed visibility for new accounts. Changing purpose
does not create another space, migrate data, share private accounts or change
commercial rights. Calendar clipping uses the existing calendar module; changes
to preferences do not rewrite financial facts.

Roles are per membership. Owners manage ownership; owners/admins manage space
settings and collaboration. Only owners can grant the admin role. Admins cannot
remove/demote an owner or another admin. Members/readers cannot manage invitations.
Managing a space grants **no financial-account access**. The resource policy
keeps read, operate and administer distinct, and clamps readers to read-only even
when an explicit grant would otherwise permit operation.

Invitations have pending, accepted, rejected, cancelled and expired states.
Creating, resending, cancelling and deciding use `Idempotency-Key`. Resending
invalidates the replaced code. Before accepting, the verified recipient can call
`POST /v1/invitations/preview` to inspect destination/role and the effect of
accounts shared with the whole space. Acceptance requires explicit acknowledgement
of that effect. It rechecks sender identity, current membership/role and effective
space capacity. Pending invitations do not reserve seats. Concurrent acceptance
locks the space and confirms membership, seat consumption, state and audit together.
Preview revalidates pending grants, or the exact accepted membership episode.
Cancelled/expired invitations and accepted invitations whose membership ended do
not disclose current space metadata, even if their old code is retained.

`GET /v1/spaces/{space_id}/departure` previews the current identity's ownership,
billing and access consequences. Removing a member ends that membership episode;
it neither deletes the identity nor touches other memberships/history. The owner
must transfer ownership first, and the billing contact must be explicitly resolved.
`/ownership` changes the owner without changing billing. `/billing/claim` is an
explicit, recently authenticated claim by the receiving owner/admin; it inherits
**no payment method or payment authorization**. No paid contract is created here.

Rejoining creates a new membership ID, not a reactivation of the old episode.
Removal/role/ownership changes advance the space access epoch. `ScopeSnapshot`
revalidation blocks old artifacts even after rejoining. No artifact/financial
resource tables are invented in this block.

### Integration requirements for subsequent modules

Use `AuthenticateCustomer`, `AuthorizeSpace` and any capability consumption inside
the transaction retained through commit. Do not reuse an `Actor` as authorization
across requests. Identity/session locks are obtained before the space lock; space
locks serialize revocation, edits, admissions and entitlement changes across
processes. Action receipts always authorize current access on replay.
Sensitive receipt replay also rechecks recent authentication and action-specific
role constraints; a previously authorized administrator cannot replay a removal
after being demoted to reader. A lost response does not preserve revoked powers.

Bind explicit resource grants to the membership **episode**, with space-scoped
relations, not just a user ID. Apply account/resource predicates in SQL before
pagination/aggregation. A scope epoch is an additional invalidation fence, not a
substitute for checking every sensitive relationship of a derived artifact.

Block 4 must extend departure atomically with the real accounts it introduces:
private responsibility is immobilized without disclosure/transfer, shared
responsibility is reassigned explicitly or immobilized. Later modules similarly
extend departure and artifact dependencies for their own resources. This is the
planned incremental integration, not an empty financial implementation in block 3.

## Noncommercial capabilities and the later commercial transition

The agreed development profile is persisted independently for every space.
Defaults are **10 active members including the owner**, with **30 days** of
validity, and explicit `read`, `manage`, `invite`, `members` capabilities. These
are development budgets, **not a free plan, trial, price or renewal promise**.
Configuration changes affect newly created spaces only. There is no automatic
renewal, cross-space benefit or allow-all authorization branch.

Missing, restricted, expired or exhausted rights deny the affected action. A
profile cannot make an inactive identity/member active, bypass roles or disclose
private resources. Administrative/security identity operations remain separate
from commercial capability checks. Current development expiry can require a
privileged renewal before space operations; production post-cancellation policies
remain a block-13 decision rather than an implied access guarantee.

`ReplaceEntitlements` is a transaction participant for block 13, not a customer
endpoint or payment verifier. Its caller must authorize/verify the commercial
event and place it inside an idempotent action. It checks expected version,
serializes with admissions, preserves consumption and billing responsibility,
disables omitted grants and audits the source reference. Lower limits do not
remove members or delete data. Each space has one current effective record with
source, profile, validity, state and provenance reference. Commercial history,
provider confirmations, checkout, cancellation and payment mandates remain owned
by block 13. No commercial confirmation creates a financial movement.

Production configuration forbids automatic development-profile provisioning.
Production signup is not enabled by silently granting a development plan: its
initial commercial rights must be integrated in block 13 before public launch.

## Configuration and operational bounds

All settings use the `FACETS_` prefix and are validated before serving. The binary
does not load `.env`; the PowerShell scripts load ignored local configuration.
No zero value denotes unlimited time, seats, attempts, memory or sessions.

| Setting | Default / enforced bound |
| --- | --- |
| `ACCESS_KEY` | Required for API/notification workers; 32 bytes, hexadecimal. |
| `PASSWORD_MEMORY_KIB` / `PASSWORD_ITERATIONS` | 19,456 / 2; ranges 19,456–262,144 / 2–10. |
| `PASSWORD_CONCURRENCY` | 2; 1–16, memory × concurrency at most 1 GiB of KDF workspaces. |
| `SESSION_IDLE` / `SESSION_ABSOLUTE` | 24h / 720h; at most 30 / 90 days; idle cannot exceed absolute. |
| `STAFF_IDLE` / `STAFF_ABSOLUTE` | 15m / 8h; at most 1h / 24h. |
| `SESSION_ACTIVITY_INTERVAL` | 1m; at least 1s and less than either inactivity deadline. |
| `MAX_SESSIONS` | 20; 1–100 per identity, serialized across processes. |
| `IDENTITY_TOKEN_TTL` / `INVITATION_TTL` | 30m / 168h; at most 1h / 30 days. |
| `RESEND_DELAY` / `REAUTH_TTL` | 1m / 5m; resend at least 30s and below token TTL; reauth at most 15m. |
| `ATTEMPT_WINDOW` | 5m; 30s–1h. |
| `IDENTITY_ATTEMPTS` / `ORIGIN_ATTEMPTS` | 10 / 100 per action family/window; bounds 1–100 / 1–10,000. |
| `PUBLIC_RESPONSE_FLOOR` | 200ms; 50ms–1s, cancellable. |
| `DEVELOPMENT_PROFILE` | `development` outside production; explicit configuration forbidden in production. |
| `DEVELOPMENT_MEMBERS` / `DEVELOPMENT_VALIDITY` | 10 / 720h; 1–10,000 members, at most 366 days. |
| `MAIL_FROM` | Explicit verified SES sender; no fallback. |

Notification leases reserve execution plus **two** SQL deadlines for secret
cleanup and durable completion. With the existing defaults: 20s + 5s + 5s = 30s.
Token-bearing endpoint bodies are limited to 16 KiB in addition to the shared
HTTP limits. TOTP protocol values and token entropy are fixed, not weakening knobs.

## Internal identities

There is no public internal signup. Initial staff provisioning is an explicit
privileged OS/database operation, producing no financial or support permissions:

```powershell
# Restricted file: email, password, totp_secret (base32), code (current TOTP).
# Keep inputs and recovery-code output in an OS-protected, ignored directory.
Get-Content -Raw ./secrets/staff-bootstrap.json |
    ./artifacts/facets-server.exe -mode staff-create -operator '<actual-operator>' |
    Set-Content ./secrets/staff-recovery.json
```

Supply the separately authorized `FACETS_MIGRATION_URL` and identity encryption
key to this operation; never expose it through HTTP or give migration credentials
to the runtime. Input is bounded stdin, not command-line secrets. Provisioning
requires a current TOTP backed by a valid 20–64-byte base32 secret and audits the real operator; it cannot overwrite an
existing staff identity or reset a factor. The accepted enrollment step is spent:
use the next available step or a recovery code for initial login.

Staff login/session/MFA routes are under `/v1/internal`; customer tokens are
rejected there and vice versa, even for the same email/person. Support grants,
staff faculties and exceptional two-approver recovery are block 13. There is no
ordinary support route to bypass lost factors and no implicit global-read role.

## Secret retention and maintenance

Outbox payloads contain a random delivery **reference**, never the usable token,
email body or factor. Token verifiers and short-lived encrypted delivery material
are separate. The worker revalidates recipient, state, token, sender membership,
capabilities and optional preferences immediately before sending, without holding
a transaction over SES. Acceptance clears delivery ciphertext. Obsolete delivery
clears it where the database is available; expiry maintenance is the backstop.
Uncertain external outcomes are not blindly retried.

Run the existing privileged maintenance task on a schedule, with a real operator.
Each invocation clears at most 1,000 expired delivery secrets/recipients, expired
token recipients, pending MFA enrollments and rate buckets per category. It does
not delete current factors, history or membership episodes. Token verifier
tombstones and action references remain for their identity/space lifetime;
approved deletion is integrated across all resources in block 13. Security audit
retention remains 12 months and operational logs 30 days. Runtime grants cannot
update/delete audit or perform arbitrary purges. Back up the encryption key
separately; database recovery without it cannot restore usable MFA factors.

## Evidence and performance baseline

`./scripts/dev.ps1 validate` is the closure gate. All access integration scenarios
use PostgreSQL 18.6, real HTTP and the restricted runtime role. SES uses the real
SDK over a signed local HTTPS fixture. No real identities, mailboxes or financial
data are used. Scenarios can be selected independently; each resets only its
disposable fixture database.

| Requirement | Evidence |
| --- | --- |
| Concurrent registration, verification and one personal space | `TestAccess/verified_registration...` |
| Independent sessions, expiration, realm separation and safe login failures | `TestAccess/session_revocation...`, `/separate_internal_identity...` |
| MFA replay, factor-preserving reset, disable revocation and one-use recovery | `TestTOTPReplayWindow`, `TestAccess/MFA_replay...`, `TestAccessRecovery/concurrent_recovery-code...` |
| Context per session, versions, cursor isolation and action fingerprints | `TestAccess/space_contexts...` |
| Serialized seats and future commercial-state replacement | `TestAccess/concurrent_invitations...` |
| Ownership, billing and rejoining without old grants | `TestAccess/membership_removal...`, `TestAccessLifecycle/inviter_role...` |
| Replaced/cancelled/expired invitations and current preferences | `TestAccessLifecycle/resend...`, `TestAccessRecovery/notification_preference...` |
| Revocation after command start and derived-scope invalidation | `TestAccessLifecycle/revocation_commits...`, `/artifact_scope...` |
| Current action-specific authorization on receipt replay | `TestAccessLifecycle/replayed_commands...` |
| Missing/expired capabilities isolated by space | `TestAccessLifecycle/expired_and_absent...` |
| Recent-auth rotation, email proof, preference versions, durable abuse counters | `TestIdentityLifecycle` |
| Registration/outbox rollback, secret purge and replacement verification | `TestIdentityLifecycle/failed_mail...`, `TestAccessRecovery/expired_verification...` |
| Actual TCP response lost after commit, then API replay without duplicate space | `TestAccessRecovery/real_HTTP_response_loss...` |
| Process-local service replacement preserves sessions and queued delivery | `TestAccessRecovery/service_restart...` |
| Encryption binding/tamper, KDF bounds, common passwords and reader policy | `TestCryptography`, `TestPolicy`, `TestResourcePermission` |

Initial Argon2 calibration, Go 1.27.3, Windows/amd64, i7-11370H, default 19 MiB,
2 iterations, parallelism 1; five serial one-second benchmark repetitions:

```powershell
go test -run '^$' -bench BenchmarkPasswordHash -benchmem -count=5 -benchtime=1s ./internal/access
```

Observed 23.7–28.5 ms/hash, about 19.93 MB allocated/hash, 34–35 allocations.
Two admitted hashes bound concurrent KDF workspaces to approximately 38 MiB,
**not total process RSS**. This is a reproducible starting measurement, not a
production throughput/p95 claim or evidence for reducing the OWASP security
minimum. Recalibrate under deployment CPU/memory and realistic abuse/load before
public service. The broader load and recovery acceptance remains block 14.

### Closure evidence — 2026-10-30

- The final `./scripts/dev.ps1 validate` passed every gate, including PostgreSQL
  integration, Linux `-race`, reproducible sqlc/OpenAPI, Goose validation,
  module integrity, vet/lint and artifact inspection (`facets-server 0.3.0`).
- `govulncheck -tags=integration -test ./...` found zero reachable vulnerabilities
  and zero vulnerabilities in imported packages. Its one module-only notice is
  the previously documented `GO-2026-5932` for unused x/crypto/OpenPGP.
- Replay authorization, concurrent revocation and restart scenarios also passed
  independently, without prerequisite scenarios populating their fixture state.
- Local `db-config` applied migration 2 and restricted grants without deleting
  existing data. A clean staged export built with `-mod=readonly -trimpath`;
  its executable served readiness against the migrated local PostgreSQL and
  rejected anonymous `/v1/me` with the expected 401 Problem Details contract.
- An accepted invitation's old preview fails after membership removal and still
  fails after rejoining under a new membership episode. Internal provisioning
  rejects malformed or undersized TOTP secrets.

No live mail, payment provider, cloud infrastructure or client application was
created by these tests. Real notification-worker deployment still requires the
verified SES sender, credentials and region described above. Block 4 is not started.
