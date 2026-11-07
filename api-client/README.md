# Facets API client

Curated OpenCollection YAML collection: **48 requests**, one filename per
`operationId` in [`docs/openapi.json`](../docs/openapi.json). Use a current Bruno
Desktop with OpenCollection YAML support; CLI checks use **4.3.0**. Scripts use
Safe Mode; no npm packages, filesystem access, or Developer Mode are needed by
the collection.

## First use

1. Start the API and notification worker using [local operation](../docs/LOCAL_DEVELOPMENT.md).
2. From the repository root, copy the synthetic development configuration:

   ```powershell
   Copy-Item api-client/.env.example api-client/.env
   ```

3. In Bruno Desktop, **open collection** `api-client/` (do not import OpenAPI
   over it). Select the **local** environment. Bruno loads `api-client/.env`,
   not the server's root `.env`.
4. Run `auth/register`. Read the full 43-character verification code in the
   worker's `development mail delivery` log, set `VERIFICATION_CODE` in the
   collection `.env`, then run `auth/verify-email` and `auth/customer-login`.
   `request-verification` is an optional resend, not a required extra step.
5. Run `account/customer-profile`, `spaces/list-spaces`, `spaces/get-space`,
   `spaces/create-space`, `spaces/edit-space`, `spaces/space-capabilities`,
   `spaces/list-members`, and `invitations/invite-member` as needed. Tokens,
   IDs, versions, and idempotency keys require no copying.

Example values are synthetic, ASCII, and accepted by the current password,
currency, locale, and zone policies. They are **not production credentials**.
Mail codes and MFA factors deliberately start empty: no fabricated code can
satisfy the backend. Enter real local codes only in the ignored `.env` or
runtime variables, never in versioned environment/request files. If Desktop
does not pick up a changed `.env`, reload the collection before authenticating.

## State and prerequisites

- Customer Bearer auth is defined at collection level. `internal/` overrides
  it with staff Bearer auth. Public requests explicitly disable authentication
  by omitting the `http.auth` field (Bruno's canonical None mode, not inheritance).
  The literal `auth: none` produces an unsupported-auth warning; `auth: null`
  parses but is not valid against the published OpenCollection JSON Schema.
- Scripts use `bru.setVar`, not persistent environment writes: session tokens,
  IDs, versions, and changed credentials stay in memory. Closing Bruno loses
  them; log in again. Changing environment alone is not a session reset: log
  out first and log in against the new environment. Do not copy runtime tokens
  into environment YAML or export secret-bearing responses/reports.
- Login resets customer resource selection. `list-spaces` selects its first
  item; `create-space` selects the new space. Space-scoped requests discover
  the first accessible space automatically when none is selected. Verification
  creates a personal space; create a separate family/group space for experiments.
- `get-space` captures version and caller membership. `edit-space` and
  `preferences` fetch missing versions automatically. A successful edit
  invalidates the cached version. A real concurrent update still returns
  `409 version_conflict`: re-read the resource and retry deliberately.
- `list-members` selects a non-owner, preferring someone other than the caller;
  it leaves `memberId` empty when only the owner exists. Role/removal/ownership
  requests discover that target automatically, but cannot invent another member.
  Owners cannot be removed or demoted, and billing must be resolved separately.
- `invite-member` captures the invitation ID, **not its secret**. Invitation
  listing selects the first pending item. Resend/cancel discover that ID if
  missing. Resend rotates the secret and is rate-limited; use the latest worker
  mail. A duplicate pending invitation can return `409 resource_conflict`.
- To accept an invitation, register/verify its recipient, then log in with that
  recipient's credentials and set `INVITATION_CODE` from their mail. Run
  `preview-invitation` then `decide-invitation`. The inviter cannot accept for
  someone else. Recipient login replaces the customer session in this collection;
  log back in as owner and list spaces/members to administer the new membership.
- List endpoints accept optional `limit` (1–100) and `cursor` query parameters;
  use `next_cursor` to inspect further pages. Automatic selection uses only the
  returned page. An empty list clears its corresponding selection.
- Sensitive commands require recent authentication. On
  `403 reauthentication_required`, run the realm's reauthenticate request. It
  rotates the token and session ID automatically. Roles, member capacity and
  commercial capabilities are still enforced; a valid example is not a bypass.
- Password/email changes, MFA confirmation/disable, logout and current-session
  revocation invalidate sessions. Scripts clear the affected token. New
  credentials are reused in memory; update `.env` before restarting Bruno.

Every command requiring `Idempotency-Key` uses `{{$randomUUID}}`. Each click
is a **new action**, not a retry of an uncertain prior action. To test replay
semantics, deliberately replace that header temporarily with the same fixed
key and identical body; never regenerate the key for an actual retry. Do not
commit temporary replay keys or secret-bearing edits.

## MFA and staff

Staff is provisioned through the server's privileged offline `staff-create`
mode, **not** through registration or this collection. Provision an account
with MFA, then set `STAFF_EMAIL`, `STAFF_PASSWORD`, and `STAFF_MFA_CODE` before
`internal/staff-login`. The example staff password does not create an account.

Customer MFA is optional. Enrollment returns an `otpauth` URI; add it to an
authenticator and submit its six-digit TOTP to the confirm request. Staff MFA
setup replaces an existing enrollment; it cannot bootstrap access. Store
returned recovery codes securely. **A successful TOTP step or recovery code
cannot be reused** for login/reauthentication; wait for a new step or use an
unused recovery code. Recovery codes do not replace the TOTP needed to confirm
new enrollment. These manual possession checks are intentional security boundaries.

## Smoke run

After customer verification, filter the Desktop runner by **smoke**, use the
**local** environment, and run sequentially. For terminal execution, install the
optional CLI explicitly (`npm install -g @usebruno/cli@4.3.0`), keep the local
server running, and from the repository root run:

```powershell
./scripts/dev.ps1 api-client-smoke
```

The 11 tagged requests cover health, customer login/profile/sessions, spaces,
capabilities, members, invitations, and departure preview. They do not modify
financial resources; login does create a session. Smoke expects the selected
customer to have owner/admin invite access to the first returned space. Do not
use parallel execution: these requests share runtime state.

The task first checks contract synchronization, then runs `bru run --env local
--tags smoke --bail --sandbox safe` from `api-client/`, preserving its exit status.
It does not install tools, start servers, provision accounts, capture mail codes,
or create reports containing credentials. It reads this collection's `.env`, not
the server's. Go `check`/`validate` never require Node.js or Bruno CLI.

Integration tests already cover authentication, authorization, persistence and
the composed HTTP/worker mail flow (`tests/access/`, especially
`runtime_test.go`). This smoke only adds execution of the actual collection's
requests, shared state, JavaScript and assertions; it is not another business-rule
test suite. Repeated runs create login sessions and can hit normal rate limits.

**Do not run all 48 requests as one scenario.** They include mutually exclusive
flows, password changes, logout, cancellation, MFA enrollment, and staff actions.
Use individual requests or an intentionally selected sequence on synthetic data.

## Contract maintenance

The shared post-response script rejects the `$schema` discovery field and
schema `Link` headers in both successful and error responses. Schema discovery
endpoints are disabled; [`docs/openapi.json`](../docs/openapi.json) remains the
integration contract, and server-side input validation remains enabled.

Keep request filenames equal to OpenAPI operation IDs and folders aligned with
tags. When the contract changes, review examples against `internal/access/`
validation and authorization, update scripts/assertions, and inspect the diff.
An OpenAPI import alone is not a replacement for this curated collection.

Run `./scripts/dev.ps1 api-client-check` after editing the contract or collection.
It is also mandatory in `check` (even with `-Packages`) and `validate`, using Go
and the YAML parser already pinned in `go.mod`, without CLI/network/server access.
It rejects missing, orphaned or duplicate requests, mismatched methods/paths,
auth mode, required parameters, malformed/structurally stale body examples, and
missing success-status assertions.

Whenever an endpoint changes, update its request, examples, scripts and assertions
and test the affected flow in Bruno. The static check does not verify every schema
constraint, response field, JavaScript behavior or runtime template value; those
remain part of reviewing and testing the change. Use the smoke for its supported
path, not as a substitute for testing other affected requests.
