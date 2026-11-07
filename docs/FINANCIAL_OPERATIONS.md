# Financial operations — block 5 verified

Block 5 is **closed and verified (7 November 2026)**. It supports real manual income,
purchases, internal liquid-account transfers, refunds, reconciliation, consistency
checks, recent selections, search and common financial queries, as well as
corrections and voiding with retained revision evidence. Payment
applications, agreements and card cycles remain in their planned later blocks.

The executable contract is [OpenAPI](openapi.json); the matching requests
are under [`api-client/finance/`](../api-client/finance/).

## Confirming a fact

`POST /v1/spaces/{space_id}/operations` requires a retained `Idempotency-Key` and
`confirm:true`. `applied` always supplies an account and its actual positive
amount in canonical int64 minor-unit strings. Account currency/scale is immutable
and returned in the effects. The server, not the caller, derives effect signs:

- Income increases a liquid account and recognizes income.
- A purchase reduces liquidity or increases an obligation, and recognizes expense
  once. Receivables are not accepted as purchase or income destinations.
- Transfer requires two different liquid accounts in the same space, with operate
  permission on both. `received` is the actual amount credited, **before separately
  identified fees**. Equal currencies require equal principal amounts. Different
  currencies preserve both supplied actual amounts without estimating either one.
- At most one fee on each endpoint is accepted. Every fee requires an expense
  category, has its own effect and economic component, and is committed together
  with both principal effects. No principal becomes income or expense.

Income/purchase `parts` contain up to 100 category allocations in account currency
and must sum exactly to the applied amount. Empty parts means one unclassified
component, not omission of the economic fact. A missing category on a part remains
explicit. `exceptional` does not exclude recognized expense. Transfers have no
principal category, original merchant price, merchant or exceptional flag.

Notes (up to 1,000 characters), optional merchant/counterparty, and up to 20 unique
tags are stored with the revision. Optional `original` preserves a merchant price
and its own currency without changing the applied amount. An optional historical
exchange reference must match the account-to-base direction and effective date;
absence does not produce a 1:1 conversion. Subsequent base-currency changes never
replace the recorded reference. This delivery does not publish converted totals.

All IDs resolve inside the supplied space. Catalog aliases retain original IDs;
detail also returns canonical category/tag identities after merges. Categories
must have the component's direction. New use of archived canonical entries fails.
No financial amount changes when a catalog is merged.

## Dates, balances and transaction boundaries

Effective civil dates are separate from registration timestamps. Future dates in
the space's IANA zone are rejected: expectations are not financial facts. An
effect before its account's tracking date is an antecedent already included in
the opening reference: it is retained with `in_tracking:false` and does not change
the current projection. This applies independently to each transfer endpoint.

Only known opening references have numeric current balances. Estimated references
remain estimated; unknown references remain null even when known effects exist.
Negative balances are retained. Ordinary reads use the stored projection, not a
history scan. `ReconstructedBalance` reconstructs the value from the opening
reference and current in-tracking effects for integration consistency checks;
the operational discrepancy/incidence workflow is described below.

Creation writes the operation, revision 1, typed effects, economic components,
tags, account projections, audit and receipt in a single transaction. Neither
public inputs nor independent mutation endpoints accept arbitrary effect signs or
account balances. No outbox event or external payment is needed to confirm these
manual facts. Runtime access is SELECT/INSERT for financial evidence; only the
`financial_operations.current_revision` pointer has column-scoped UPDATE access.

The existing database/HTTP instrumentation covers timeouts, retries, failures and
request metadata without logging descriptions or amounts. Existing space locks
coordinate current permissions, revocation and writes across processes. Accounts
are acquired in sorted ID order. Each account's effects are accumulated with
exact intermediate arithmetic and a checked int64 final projection, shared by
creation and correction. Any error, including projection overflow or audit failure,
rolls back the complete operation and its receipt.

All category, tag, merchant and counterparty references are resolved in one
bounded SQL query, including alias targets. A 100-part split does not perform
100 catalog round trips. Detail loads effects, components and tags in fixed
queries, not per-row lookups.

Same key/content retrieves the committed ID; changed content conflicts. Replays
check current membership, capability and read access to the result's accounts.
They do not require a formerly active account to stay open. Different keys are
distinct facts, not automatically deduplicated by amount/date similarity.

## Reading without disclosing a private counter-end

`GET /operations/{operation_id}` returns full detail only when every affected
account is currently readable. Otherwise it returns 404 without restricted names,
amounts or shared notes. Membership authorizes neither a private account nor a
historical access grant.

`GET /accounts/{account_id}/history` returns only that account's signed effects,
operation ID/kind/date and tracking flag. It never contains the opposite account
ID, name, amount, shared note, merchant or tags. Thus the authorized account can
still explain its own position after an endpoint becomes private or immobilized.
The cursor identifies operation ID/effect ordinal, has stable ordering and pages
of 1–100 effects. A fee is another effect of the same fact, not another operation.
Every page rechecks current access; a cursor is not an authorization artifact.

Migrations 6–9 extend published migrations 1–5 without modifying them. Migrations 6–7
remain unchanged; 8 adds refunds and 9 adds reconciliation/consistency evidence.
Readiness requires version 9. Local role provisioning must be refreshed explicitly with
`./scripts/dev.ps1 db-config`; startup never migrates automatically.

## Requirement-to-test map for this delivery

| Requirement | Evidence |
| --- | --- |
| Typed inputs, confirmation, bounded parts/tags, exact positive amounts and range | `internal/finance/operations_test.go` |
| Liquid/obligation signs, split recognition, exceptional flag, original/applied distinction, multimonetary principal and fee | `TestFinancialOperationsRecognition` |
| Unknown reference, historical effects, projection reconstruction, identity-preserving catalog merge | `TestFinancialOperationsRecognition` |
| Private counter-end, space boundary, reader denial, author removal and transaction rollback | `TestFinancialOperationsPrivacyAndAtomicity` |
| Concurrent idempotency, content conflict, distinct writes and projection overflow | `TestFinancialOperationsConcurrencyAndRange` |
| Invalid inputs, split sum/overflow, future date, same-currency mismatch, negative balance and cursor pagination | `TestFinancialOperationsValidationAndHistory` |
| TCP response lost after commit and recovery after replacing the service | `TestFinancialOperationLostResponse` |
| OpenAPI and Bruno synchronization | `TestCollectionContract` |

### Verification of this delivery (7 November 2026)

`./scripts/dev.ps1 validate` passed on the final code: unit/contract tests,
reproducible sqlc/OpenAPI, migration checks, real PostgreSQL/HTTP integration,
vet, lint (zero issues), Linux race detection, vulnerability scan and executable
build/version inspection. The integration suite also passed independently.
Govulncheck reported zero reachable and zero imported-package vulnerabilities;
one non-reachable required-module advisory remains. Migration 6 and restricted
local role grants were applied with `db-config`, preserving existing data.

The collection's 71 requests pass `api-client-check`. Bruno CLI smoke was not run
for this delivery; its existing 14-request route is non-financial. The new
financial paths were exercised through real HTTP integration tests, including
lost committed responses. This evidence validates only the implemented delivery;
it does not close block 5 or certify the unimplemented capabilities listed above.

Sure was consulted read-only: `inspiration/sure/app/models/transfer.rb` and
`test/models/transfer_test.rb` for endpoint/tenant invariants and fees, and
`app/models/transaction.rb` for classification/suggestion behavior. No source or
schema was copied. Independent leg deletion, reset idempotency keys, asynchronous
balance synchronization, loan principal treated as budget expense, and blanket
exclusion of exceptional spending were not adopted. The rules above follow this
project's financial design.

## Corrections, consequences and cancellation

`POST /operations/{operation_id}/correction-preview` accepts `{version, reason,
replacement}`. `replacement` has the complete creation shape, including
`confirm:true`; that flag describes the proposed capture, not permission for the
preview to write. Preview creates no revision, receipt or balance effect. It
returns the current version, descriptive/financial classification, previous/new
effective dates and before/after balances of **all** affected accounts. Unknown
opening references remain null. Another operation can change balances between
preview and confirmation; preview is not a reservation or permanent snapshot.

`PUT /operations/{operation_id}` accepts the same body plus a retained
`Idempotency-Key`. The complete replacement preserves operation identity, original
author and origin. The operation kind cannot be changed. The expected version is
the current revision; stale concurrent edits produce `409 version_conflict`.
Same key/body recovers the committed result even after another revision, while
changed content produces `409 idempotency_conflict`. The result reference is the
operation ID; get detail separately to read its latest revision.

The server identifies descriptive changes (note, merchant/counterparty, category
splits, exceptional flag and tags) versus financial changes (date, applied accounts
or amounts, fees, original price or exchange reference). Both retain complete
revision evidence, reason, revision author and timestamp. Creation and correction
use the same planner and revision writer. A continuing category/tag/merchant may
remain archived; a new archived classification is rejected. An unchanged exchange
reference in the same currency/date preserves its original presentation context,
including after a space changes its base currency. A different date/reference must
pass normal reference validation.

Every old and new account requires current operate permission and operational
state; read, space administration and historical responsibility are insufficient.
All accounts are locked in sorted ID order under the existing space authorization
lock. The transaction subtracts old in-tracking contributions and adds new ones,
advances the revision, updates projections and records audit/receipt together.
Intermediates use bounded exact integer arithmetic; only the final per-account
projection must fit int64. There is no full-history scan for a correction.
Crossing the tracking date changes applicability, not the opening reference.
Correcting January in October preserves January as the effective fact; it does not
create an October cash flow.

`POST /operations/{operation_id}/void` requires `{version, reason, confirm:true}`
and a retained key. It cancels the complete operation, including both transfer
principals and all fees. A new void revision retains the canceled data as evidence,
marked `voided:true`; those effects/components are not current financial facts.
Reconstruction and account effect history exclude voided operations. No rows are
deleted, original receipt keys are not cleared and no current-date reversal is
invented. A voided operation cannot be edited or reactivated; replay of its
successful command still recovers its identity under current read authorization.

`GET /operations/{operation_id}/revisions` pages summaries by increasing revision
(1–100 items; numeric cursor). Current detail must be readable and each historical
revision's own accounts are authorized **in SQL before pagination**. This matters
when a correction moved a fact away from an account that is now private. The list
does not disclose the hidden revision's author, reason or date.
`GET /operations/{operation_id}/revisions/{revision}` returns full retained evidence
only with current read access to every account of that particular revision. A
current readable revision does not grant access to private former accounts.

### Additional requirement-to-test map

| Requirement | Evidence |
| --- | --- |
| Exact replacement/void arithmetic, tracking boundary, int64 intermediate/final limits, unknown balance | `internal/finance/operation_corrections_test.go` |
| Descriptive/financial revisions, read-only preview, version conflict, changed-content conflict, date/account correction, cancellation and paginated history | `TestOperationCorrectionsAndVoiding` |
| Transfer legs/fees together, audit rollback, concurrent same-key replay, competing edits and overflow rejection | `TestOperationCorrectionAtomicityAndConcurrency` |
| Historical private account after replacement, current revocation, reader denial and SQL-authorized pagination | `TestOperationRevisionPrivacy` |
| Lost TCP response after correction/void commit and recovery after replacing the service | `TestOperationChangeLostResponse` |
| Existing operation/receipt preserved from migration 6 to 7 | `TestOperationCorrectionsMigration` |
| Archived reference/base-currency retention, unknown balances, immutable history and limited pointer grant | `TestOperationCorrectionRetainedReferences` |

Sure's `app/models/transfer.rb` and `test/models/transfer_test.rb` were consulted
again for lifecycle behavior. Its independent-leg deletion and idempotency-key
reset are deliberately not adopted: this server keeps one atomic operation and
retained receipts. No source or schema was copied.

### Acceptance boundary

Obligations and payment applications remain in block 6; this block does not claim
to recalculate those future resources. The continuation below implements the
remaining block-5 capabilities. Final regression and real collection/API acceptance
evidence are recorded below; earlier verification entries certify only their
explicitly described deliveries.

### Verification of this continuation (7 November 2026)

`./scripts/dev.ps1 validate` passed on the final source after a focused regression
exposed and fixed a creation-only intermediate overflow: at the int64 ceiling, a
transfer's received principal plus an offsetting destination fee has a valid final
balance and must not be rejected based on insertion order. Creation and correction
now share projection arithmetic and writes. The regression failed before the fix
and passed afterward against real PostgreSQL.

The final validation covered unit/HTTP-contract tests, unchanged modules,
reproducible sqlc/OpenAPI, migration checks, PostgreSQL/HTTP integration including
the additional tests above, vet, lint with zero issues, Linux race detection,
govulncheck and executable build/version inspection. Govulncheck reported zero
reachable/imported-package vulnerabilities and one non-reachable required-module
advisory. Migration 7 and column-scoped grants were applied locally with
`db-config`, preserving data. Published migration 6 was not modified.

The collection now contains 76 synchronized requests. Bruno CLI 4.3.0 Safe Mode
passed the existing 14-request smoke (38 assertions) and a separate 12-request
financial scenario (33 assertions): login, synthetic account/expense creation,
detail, correction preview/confirmation, revision listing, voiding, updated detail
and account history. The financial folder also resolved the space through its
nested request. Specific revision detail and recovery after lost responses were
verified by real HTTP integration tests. The temporary local server was stopped
after the CLI run. This verifies the delivered continuation, not block-5 closure.

The optional, separate `verify` cache-integrity task exceeded its initial
120-second command limit; the background rerun completed successfully with
`all modules verified` (1,045.64 seconds). This verifies the local module cache,
not block-5 completeness, and does not replace `validate`.

## Refunds

Creation and replacement accept `kind:refund`. The actual credit increases a liquid
account or reduces an obligation (including a negative credit balance), never a
receivable. Positive input `parts` use expense categories; the server stores
negative expense components. The credit and recognition use the refund's actual
date, not a correction to the purchase date. Empty parts remain explicitly
unclassified. Historical credits respect each account's tracking boundary.

Optional `refund:{operation_id,amount_minor}` attributes an amount in the original
purchase account's currency. `applied` independently records the real credit;
different currencies never imply a 1:1 conversion. Equal currencies require equal
amounts. The source must be a readable, non-void expense in the same space dated
no later than the refund. Confirmed linked refunds cannot collectively exceed its
amount, including simultaneous distinct commands. Voiding or reducing a refund
releases its capacity; changing a purchase cannot orphan active refunds, reduce
it below the attributed total, change its currency or move it past their dates.
Resolve linked refunds explicitly before voiding that purchase. Unlinked refunds
are allowed when the purchase is unavailable; they are not fictitious income.

Each revision retains the original purchase revision as linkage evidence. Detail
omits the link when its source accounts are no longer readable; the authorized
receiving account still explains its own effect. Editing/voiding a linked refund
requires access to that retained source, not just the receiving account. Replays
return only the result identity under current account access. No source amount or
reference is exposed by a masked relation. Archived original purchase accounts do
not prevent recording a new refund to an operational destination.

## Reconciliation and affected history

`POST /accounts/{account_id}/reconciliation-preview` compares `{date,observed_minor}`
against the reconstructed **end of that date**. It writes nothing. Dates must be
within the account's tracking period and no later than today in the space's zone.
Unknown references produce null calculated balance/difference, not zero.

`POST /accounts/{account_id}/reconciliations` requires an action key, reason,
`confirm:true`, observed amount, date, `adjust` and `expected_calculated_minor`
(nullable). Confirmation rechecks permissions, operational state and the reviewed
calculation. A changed calculation produces `409 version_conflict`; a repeated
committed action recovers the same reconciliation identity without applying twice.
With `adjust:false`, a known unresolved difference stays visible and balances do
not change. Unknown observations can be retained but cannot supply a fabricated
numeric adjustment or turn an unknown opening reference into confirmed zero.

With `adjust:true` and a nonzero known difference, the transaction records a
separate `adjustment` operation, its signed position effect, projections,
comparison evidence, audit and receipt. The adjustment has no economic components;
neither opening reference nor previous evidence changes. Difference and final
projection are range-checked. Equal balances need no zero-valued operation.
Corrections to a mistaken adjustment use complete voiding and another explicit
comparison, not edits to its retained reconciliation evidence.

`GET /accounts/{account_id}/reconciliations` pages retained evidence: original
calculated/observed/difference, date, author, reason, adjustment identity and
`needs_review`. Backdated creation, financial replacement or voiding marks every
comparison whose end-of-date contribution changed, atomically with the fact. A
descriptive correction or later fact does not falsely alter the earlier evidence.
Correction previews identify affected accounts with
`reconciled_history_affected`. A newer comparison preserves earlier warnings as
historical evidence; it does not silently certify or erase affected history.

## Operational consistency evidence

`POST /accounts/{account_id}/consistency-checks` requires `confirm:true`, current
account access, a non-reader membership, `finance.write` and an action key. It can
check archived/immobilized accounts without operating them. It records the
projection and the reconstruction from opening plus current tracked effects in
one authorized transaction. A mismatch is a durable discrepancy incidence, not an
instruction to rewrite the projection. Null and numeric values remain distinct;
reconstruction uses exact numeric arithmetic even when a corrupted projection
cannot explain the total. Runtime cannot update/delete this evidence.

`GET /accounts/{account_id}/consistency-checks` pages those records under current
account authorization. Both values, the discrepancy flag, author and timestamp
are available for diagnosis. Resolution is a deliberate investigation, not an
automatic balance repair or privileged access to someone else's private account.

## Search, recent selections and common readings

`GET /operations` filters current non-void facts by half-open dates, account,
absolute effect amount range, currency, category, merchant, counterparty, tag and
kind, with literal text search over notes/merchant/counterparty names. Catalog
aliases resolve after merges. All account permissions are applied in SQL **before
pagination**. Restricted compound operations are absent; account history remains
the safe view of their permitted endpoint. Pages contain 1–100 summaries, ordered
by effective date then identity; the cursor is `date:operation_id`. Primary amount
and currency identify effect zero, not an estimated conversion or necessarily the
endpoint matched by the filters. Cursors do not grant access or reserve a snapshot.

`GET /operation-suggestions` returns up to 20 recent manual choices by the current
actor, optionally restricted to a merchant and operation kind. Accounts must remain
operational/operable; archived canonical catalog choices are excluded. A split does
not propose its first category for the entire next purchase. Each suggestion has
its source identity and selection timestamp. Nothing is pre-applied, rewritten or
treated as a recurrent. An empty history returns no invented examples.

`GET /financial-summary?from=...&to=...` is the common case for three readings:

- **Position:** current liquid, obligation and receivable subtotals per currency,
  including authorized archived/immobilized history. Unknown balances contribute
  only to incompleteness, not a confirmed zero; estimated positions remain flagged.
- **Economic:** income and net expense from current tracked components in the
  half-open effective interval. Purchases with cards count once; refunds reduce
  expense on their own dates; transfer principals and adjustments do not count.
  Optional `category_id` applies to these components only, resolving aliases.
- **Cash:** signed tracked effects on liquid accounts, separated by effect kind.
  Transfer inflow/outflow and adjustments remain distinguishable from income,
  purchases and refunds. Adjustments explain position, not real cash transactions.

Account/currency filters apply to all three. SQL authorization precedes each
aggregation: a private transfer counter-end never exposes its identity, amount or
metadata, while the visible account still contributes its own effect. The response
states `authorized_accounts_only`, `in_tracking_only`, original currencies and
update time; no private excluded counts or false all-space/consolidated totals are
returned. Accumulated amounts are range-checked before returning. These queries
remain the backend authority for future consumers; full presentation conversions,
comparisons and reports remain in block 11, not parallel financial definitions.

### Additional requirement-to-test map

| Requirement | Evidence |
| --- | --- |
| Refund recognition, real date, liquid/obligation signs, partial/cross-currency links, antecedents, correction and void capacity | `TestOperationRefunds` |
| Concurrent refund capacity, source privacy/redaction and reader denial | `TestRefundConcurrencyAndPrivacy` |
| Read-only reconciliation preview, immutable comparisons, separate adjustment, stale calculation, changed-date warning, discrepancy without silent repair | `TestAccountReconciliationAndConsistency` |
| Reconciliation rollback, competing confirmations, private accounts, unknown reference, invalid dates | `TestReconciliationConcurrencyPrivacyAndRollback` |
| Filters, literal text, stable pagination, restricted compound operations, recent split choices, aliases, three readings, incomplete positions | `TestOperationQueries` |
| Committed refund/reconciliation/check response lost over TCP, replacement service and HTTP retry | `TestFinancialCompletionLostResponses` |
| Empty-date/page validation | `internal/finance/operation_queries_test.go` |

Sure was consulted read-only in `app/models/transaction.rb` (recent manual choices
and name suggestions), `app/models/account/reconciliation_manager.rb` and
`test/models/account/reconciliation_manager_test.rb` (comparison/preview and
repeated reconciliation). This server does not adopt mutable valuation evidence,
unknown-to-zero fallbacks, asynchronous projection updates or goal/payment
matching. No source/schema was copied and no dependency was added.

## Final block-5 acceptance — 7 November 2026

`./scripts/dev.ps1 validate` passed on the final source, including unchanged-input
verification: formatting, PowerShell task regression, uncached unit/contract tests,
`go mod tidy -diff`, reproducible sqlc/OpenAPI, collection synchronization, migration
validation, the complete PostgreSQL/HTTP regression, vet, lint with zero issues,
Linux race detection, govulncheck and executable build/version inspection. The
financial suite also passed focused refund/reconciliation/query/recovery runs.
The full PostgreSQL regression passed after updating the test-only reset's explicit
table list for the new foreign keys; no production cascade or weakened constraint
was introduced to bypass that failure.

Govulncheck reported zero reachable and zero imported-package vulnerabilities,
with one non-reachable required-module advisory. `go.mod` and `go.sum` did not
change. The separate module-cache `verify` task was not repeated in this
continuation; its earlier successful evidence remains recorded above, distinct
from this final code-validation run.

Migrations 8–9 and restricted runtime grants were applied locally with `db-config`,
preserving existing data. Startup requires schema 9 and does not migrate it.
The regression covers installation/upgrades, including retained operations and
receipts through the published correction schema, and restores test runtime grants
explicitly when a migration test drops/recreates new tables.

The collection contains 84 synchronized requests. Bruno CLI 4.3.0 Safe Mode passed
the real local 14-request smoke with 38 assertions. A separate 20-request financial
scenario, using copies of the curated requests and explicitly supplied synthetic
amounts, passed 58 assertions: account/purchase/refund creation, detail,
reconciliation preview/confirmation, refund correction preview/confirmation,
search, recent choices, financial summary, retained reconciliation warnings,
revision history, complete refund voiding, consistency check and account history.
It verified net expense independently from position, retained the original
comparison after history changed, and confirmed matching projection/reconstruction
of `10700` minor PEN after voiding. One preliminary CLI assertion compared a
monetary string to a parsed numeric literal; the temporary scenario was corrected
to compare strings without changing the API or weakening server checks.

The temporary local server was stopped before final validation; the temporary
credential copy was removed and no secret-bearing CLI reports were generated.
This closes block 5, not the financial features assigned to later blocks. Block 6
has not started, and no client or new dependency was introduced.
