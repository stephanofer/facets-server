# Planning

The implementation provides standalone and recurring dues, with or without an
account, explicit privacy, versioned subscription conditions, calendar preview,
bounded generation, movement capacity, manual applications and durable personal
in-app reminders. **Block 6 is closed and verified**; final evidence is recorded
below, separately from the historical delivery checkpoints.
Agreements/card cycles belong to blocks 7–8, not to a duplicate recurrence engine.

The executable contract is [OpenAPI](openapi.json), with twenty-five matching requests
under [`api-client/finance/`](../api-client/finance/). No dependencies were added.

## Expectations and fulfillment

`POST /v1/spaces/{space_id}/dues` creates an expectation, never a financial fact.
It requires explicit confirmation. With `account_id`, the actor must operate that
account, which defines currency, scale, responsibility and privacy. Without an
account, supply `currency` instead; responsibility is the creator's current
membership and visibility initially private. Supplying both is rejected rather
than silently choosing a currency. The direction is
`payment` or `receipt`. There is no separate balance on a due.

Amounts use positive canonical int64 minor-unit strings. `confirmed` and
`estimated` require `amount_minor` and a null `maximum_minor`; `range` requires
both bounds in ascending order; `unknown` requires both null. Zero-amount events
are not payable dues in this slice. Unknown is never converted into zero.

`GET /dues/{due_id}` derives fulfillment from active applications. Confirmed
amounts expose exact `pending_minor` and `excess_minor`. Estimates/ranges/unknown
amounts expose both as null; even matching payments cannot close them without
explicit confirmation of the real amount through `PUT /dues/{due_id}`. Excess
allocations remain visible, not silently redistributed. For example, 185000
applied to a confirmed 200000 leaves 15000 pending.

The date is civil, independent of the payment date. Partial is a fulfillment
status; `overdue` is separately true when still unpaid after the due date in the
space's current IANA zone. `upcoming` means within seven civil days inclusive.
`GET /dues` orders by date/identity, with 1–100 items, an inclusive `from` and
exclusive `until`. Optional `status`, `account_id`, `series_id`, `direction`,
`currency` and `origin` (`standalone`/`recurring`) filters apply in SQL before
pagination, together with all sensitive-relation permissions. `partial` includes
both current and overdue partial dues; inspect `overdue` separately. Recurring
items identify their series, source version, ordinal and nominal slot. Listing
never generates occurrences. The cursor is not a snapshot or authorization token.

Editing requires the expected version, confirmation and a reason. It retains a
revision; renegotiating the date is not snoozing a reminder. `skipped` and
`cancelled` never mean paid. Applications must be explicitly unlinked before
omitting or cancelling their due. Account and direction are immutable in this
slice; create a distinct expectation when the underlying commitment differs.

## Applying a real movement

`GET /operations/{operation_id}/effects/{effect}/application-capacity` returns
the current operation revision, currency/scale, eligibility/direction and exact
`capacity_minor`, `applied_minor`, `available_minor` strings for that effect.
Capacity is the absolute tracked purchase/income/refund amount; voided,
pre-tracking, transfer and adjustment effects are ineligible with zero capacity.
The direction is empty when ineligible. Nonexistent effect ordinals are rejected.
The read holds the same transactional space lock as writes, so revision and
applications explain one consistent state. All active target dues must be
readable, even when the source movement itself is shared. No target identity or
private allocation is disclosed through a capacity response. Read permission
suffices; capacity does not grant operate permission or reserve any amount.

`POST /payment-applications` identifies the operation, its expected revision,
effect ordinal and 1–100 unique target dues with expected versions. Each
allocation supplies:

- `movement_minor`: capacity used in the actual account's currency.
- `due_minor`: actual amount attributed in the expected account's currency.

Equal currencies require equal amounts. Different currencies require both
explicit amounts; the server does not infer an exchange rate or change either
account's balance. The operation remains the only financial fact.

Tracked purchases satisfy payments; tracked income/refunds satisfy receipts.
Internal transfers, adjustments and pre-tracking antecedents are not accepted.
The domain-specific payment types introduced with agreements/cards must extend
this one application engine; they are not simulated as expenses or transfers.

Operate permission is required on the expected account and every account of the
actual operation. A different actual account can restrict who can see the due:
the caller must explain that consequence before sending
`confirm_access_restriction:true`; false is rejected for such a link. The read
predicate requires access to the expected account **and all accounts of every
active related operation**, before pagination and before summing visible results.
Space owners/admins get no private-account exception. Revocation applies to old
detail, lists and command replay. In this slice the frontend can identify this
possible reduction from the two account IDs; no audience-preview endpoint exists.
Capacity checks and non-void corrections also require read access to all dues
already using the source movement, so success/failure cannot be used to probe
private allocations. Voiding an authorized source remains possible and releases
its links without disclosing their target identities.

Several applications may cover a due and one effect may cover several dues. The
sum cannot exceed movement capacity, including concurrent requests and overflow
checks. Allocation sets commit atomically. Active applications are limited to
100 per due and per operation to bound detail, validation and voiding work.

`GET /dues/{due_id}/applications` returns active application IDs and original
source-revision evidence. Unlinking requires the due's expected version and a
reason. It appends release evidence, returns capacity and recalculates fulfillment;
it never reverses the source movement. Repeating the original idempotent action
recovers its result. A distinct unlink of an already released application fails.

### Recording and applying together

`POST /payment-operations` accepts `operation` (the normal `OperationCreate`
contract), `allocations` and `confirm_access_restriction`. It creates one actual
expense, income or refund, then applies its primary effect (ordinal 0) to the
selected dues. The operation's explicit `confirm:true` confirms the whole command.
It returns the operation identity; inspect the operation and dues for the effects.
Do not create the operation separately before calling this command.

The case of use participates in one `RunSpaceCommand` transaction, using the
existing operation planner, effects/projection writer and application engine.
A stale target version, invalid direction/currency, insufficient capacity,
revocation or audit failure rolls back the financial fact, balances, applications,
due revisions and receipt together. A lost committed response is recovered using
the same key and content, without another fact or allocation. Replay checks
current access to the resulting operation, all active targets, and all original
target dues even if an application was subsequently unlinked. It does not
revalidate stale original versions or apply the fact again.

The existing permission and currency-attribution rules apply unchanged, including
explicit acknowledgement of a different actual account. Transfers and adjustments
cannot fulfill these commitments; domain-specific agreement/card payments remain
in blocks 7–8. This command does not simulate those payments as expenses.

## Corrections, history and persistence

A correction retains active applications only while their source effect keeps
the account, currency, financial meaning, tracking eligibility and sufficient
capacity. Otherwise it is rejected; explicitly unlink before making that
correction, rather than guessing which obligations lose coverage. Compatible
corrections preserve the original application evidence and use the current fact
for financial reading. Voiding releases every active application and advances
the affected due versions in the same transaction as effects, balances and audit.

All commands use the existing `finance.write` capability, current membership,
transactional space authorization, action receipts and audit. Movement locks
precede target due locks in sorted identity order within the write case of use;
the existing space lock also coordinates account permissions/revocation across
processes. Financial effects are never eventually reconciled with fulfillment.
Success is sent after commit; replays recheck current read authorization.

Migration 10 adds dues, configuration snapshots, immutable applications and
append-only release evidence. Composite foreign keys reject cross-space links.
Runtime evidence grants are SELECT/INSERT, not UPDATE/DELETE. History and receipts
follow the life of their financial context; no cleanup worker purges them. The
space elimination process remains in block 13. Startup now requires migration 15;
use `./scripts/dev.ps1 db-config` explicitly to migrate/provision local roles.
Startup does not apply migrations.

The existing HTTP/SQL instrumentation records bounded request/query metadata and
errors, without payloads or financial amounts. Reminder work uses the same durable
queue and worker metrics, without financial payloads.

## Requirement-to-test map

| Requirement | Evidence |
| --- | --- |
| Certainty, positive exact amounts, overflow, range/null consistency | `TestDueAmount`, `TestPaymentApplicationValidation` |
| Exact partial/paid/excess, uncertain remaining, date/status separation | `TestDueView` |
| Real purchase/receipt versus transfer, adjustment and antecedent | `TestApplicableEffect`, `TestDuesValidationAndLifecycle` |
| Advisory exact capacity, eligibility, int64 boundaries, release/correction/void | `TestMovementCapacity`, `TestMovementCapacityLifecycle` |
| Atomic creation/application, stale target, overflow/currency/direction validation | `TestPaymentOperationValidation`, `TestPaymentOperationAtomicity` |
| Concurrent composed replay, stale distinct action, rollback on audit failure | `TestPaymentOperationConcurrentRetry`, `TestPaymentOperationPermissionsAndRollback` |
| Capacity privacy, explicit restriction confirmation and replay after revocation | `TestDuesPrivacyAndCurrency`, `TestPaymentOperationPermissionsAndRollback` |
| Atomic receipts/refunds, explicit cross-currency attribution, read-only capacity | `TestPaymentOperationReceiptsAndCurrency` |
| No fact on due creation/date, replay, 1850/2000, confirmed real amount, correction, void/unlink | `TestDuesFulfillmentAndCorrections` |
| Concurrent capacity, atomic multi-target allocation, version conflict and audit rollback | `TestDuesAtomicDistributionAndConcurrency` |
| Private linked payment, explicit currency attribution, restriction confirmation, authorized pagination | `TestDuesPrivacyAndCurrency` |
| Date filters/cursors, reader denial, revocation of detail/list/replay | `TestDuesValidationAndLifecycle`, `TestDuesPrivacyAndCurrency` |
| Lost committed TCP responses and recovery after service replacement | `TestFinancialCompletionLostResponses/due`, `/payment`, `/payment-operation` |
| Collection synchronization | `TestCollectionContract` |

Sure was consulted read-only: `app/models/recurring_allocation.rb`,
`recurring_occurrence.rb`, `recurrence_rule.rb`,
`test/models/recurring_occurrence_test.rb` and
`test/controllers/recurring_allocations_controller_test.rb`. Its nullable movement
links, tolerance-based closing, auto-matching and snooze-as-due behavior were not
adopted. No source or schema was copied.

## Initial slice verification (7 November 2026)

Focused unit/contract tests and PostgreSQL/HTTP integration passed, including
private-capacity probing and lost committed responses. The collection contains
91 synchronized requests. Bruno CLI 4.3.0 Safe Mode passed the existing smoke
(14 requests, 38 assertions) and an additional account → movement → due → edit →
application → unlink → void route (16 requests, 36 assertions). The local database
was upgraded to migration 10 and restricted grants refreshed without deleting
existing data. The temporary smoke server was stopped afterward.

`./scripts/dev.ps1 validate` passed on the final source: all unit/contract tests,
reproducible sqlc/OpenAPI, migration validation, complete PostgreSQL/HTTP
regression, vet, lint with zero issues, Linux race detection, vulnerability scan
and executable build/version inspection. Govulncheck reported zero reachable
and zero imported-package vulnerabilities, with one non-reachable advisory in
required modules. The separate module-cache integrity task was not repeated;
no module dependencies changed.

These checks validate this slice, not the complete block. Block 6 remains open;
its remaining work is listed above and in the development plan.

## Capacity and atomic-command continuation (7 November 2026)

Focused finance/contract tests and PostgreSQL/HTTP integration passed for the new
capacity and composed-command cases, including concurrent replay, rollback after
financial writes, current permissions, explicit currency attribution and recovery
after a lost committed response and service replacement. No dependency or schema
change was needed; published migrations were not modified.

The collection now contains 93 synchronized requests. Bruno CLI 4.3.0 Safe Mode
passed the standard smoke (14 requests, 38 assertions) and the new account → due →
atomic payment → replay → capacity → unlink → capacity → void → capacity route
(14 requests, 36 assertions). Only synthetic example amounts were used. The
temporary local server used a separate diagnostic port and was stopped afterward.

`./scripts/dev.ps1 validate` passed on this continuation: format, unit/contract
tests, module tidy, reproducible sqlc/OpenAPI, migrations, complete PostgreSQL/HTTP
regression, vet, lint (zero issues), Linux race detection, vulnerability scan and
executable build/version inspection. Govulncheck reported zero reachable and zero
imported-package vulnerabilities, with one non-reachable required-module advisory.
The separate module-cache integrity task was not repeated; dependencies did not
change. These results validate the delivered slice, not the pending capabilities.

At that checkpoint, series, accountless privacy, financial subscriptions, complete
agenda filters and reminders were still pending. It did not close block 6.

## Account-linked series

`POST /series/preview` accepts `recurrence`, `from` and `until` and returns dates,
zero-based ordinals and slots without persisting anything. It requires current
space access, not a write permission. The half-open window is at most 366 civil
days. Coincident clipped dates remain two independent entries.

`POST /series` configures name, amount/certainty, expected account, direction and
recurrence, with explicit confirmation and an action key. It creates no dues or
movements. `GET /series` is authorized before cursor pagination; `GET /series/{id}`
returns current conditions and `generated_until`. Account/currency/direction are
immutable. Current account permissions and responsibility govern access and
operation, including immobilization and revocation; owners get no exception.

| Frequency | Configuration |
| --- | --- |
| `weekly` | Original start plus calendar weeks. |
| `biweekly` | Every two calendar weeks, not twice a month. |
| `monthly` | Original nominal start day, clipped without drifting. |
| `twice_monthly` | `first_day` and `second_day` are distinct ascending nominal days, 1–31. |
| `yearly` | Original month/day; February 29 returns in leap years. |
| `every_weeks` / `every_months` | Explicit `interval`, 1–1200. |

`interval` is zero for the named fixed frequencies. Nominal day fields are zero
except for `twice_monthly`. `end` is optional and inclusive; `count:0` means no
count limit. Otherwise count is 1–100000, measured from the rule's start, including
each independent nominal slot and excluding slots before start in the first
partial month. End and count both limit the result when supplied together.
Changing a window never renumbers occurrences or restarts the count. No floating
point, elapsed-hour increments, detection or automatic payment matching is used.

### Generation and recovery

`POST /series/{id}/generate` requires expected configuration `version`, `until`,
`confirm:true` and an action key. The series must be active and its account
operable. It extends from the durable `generated_until` checkpoint, initially the
rule's start, by at most 366 civil days. Longer catch-up uses successive bounded
commands. An already covered horizon is a no-op, even with another action key.
The server does not silently advance horizons on reads or run a recurring sweep.

Generation holds the existing transactional space authorization and series lock.
Bulk writes persist common dues, due revisions, unique occurrence identities,
checkpoint, audit and action receipt together. There are three bounded bulk
inserts, not three SQL round trips per occurrence. A failure leaves no partial
window; replay after a lost committed response recovers the result without
generating again. Unique `(space, series, version, ordinal)` keys provide a
database backstop, including coincident slots. Generation never creates financial
operations or changes balances.

Generated dues use the existing detail, correction, omission and application
commands. Omit one occurrence with `PUT /dues/{id}` and `state:"skipped"`; active
applications must be explicitly unlinked first. Regenerating does not recreate an
omitted occurrence. There is no second fulfillment or payment model for series.

### Changes and lifecycle

`PUT /series/{id}` requires expected version, a full replacement configuration,
`effective_from`, `state`, confirmation, reason and action key. Preview the proposed
rule before confirming. States are `active`, `paused`, `ended` and `cancelled`.
Pausing stops generation; resume uses `active` with an explicit effective date.
Ended/cancelled series cannot be resumed. These actions never cancel a provider
contract, pay anything or remove already existing obligations.

This implementation conservatively preserves **all materialized dues**, not just
confirmed or paid ones. New conditions must start on or after the previous
exclusive generation boundary and on or after the proposed anchor. To change an
existing obligation, explicitly edit its common due instead. A later effective
date deliberately leaves the intervening interval ungenerated, including a pause;
resume never backfills it silently. Keeping the original anchor keeps its nominal
day and repetition count; changing it explicitly starts the replacement rule's
count from that new anchor. Version history explains both rules and boundaries.

`GET /series/{id}/versions` exposes historical conditions and reasons with current
authorization and numeric version cursors. Only the active generation checkpoint
is mutable; the runtime role cannot update historical conditions or delete
series/occurrence evidence. Migrations 11–12 add the three series tables and a
deferred foreign key requiring a persisted current version at commit, without
modifying migrations 1–10. Receipts and series history follow the financial
context's life, not the technical-log retention period.

### Evidence and remaining block scope

| Requirement | Evidence |
| --- | --- |
| All frequencies, nominal clipping, leap years, two coincident slots, count/end and bounded windows | `TestRecurrenceWindow`, `TestRecurrenceValidation`, `FuzzRecurrenceWindow` |
| No movements on configuration/generation, existing payment/omission engine, pause/resume, price history, cancellation | `TestSeriesGenerationAndLifecycle` |
| Overlapping concurrent generation, version races, audit rollback and recovery | `TestSeriesConcurrencyAndRollback` |
| Current account privacy, reader denial, cross-space denial, revocation of detail/history/replay | `TestSeriesPrivacyAndValidation` |
| Committed TCP reply lost and service replacement | `TestFinancialCompletionLostResponses/series`, `/series-generation` |
| Agenda filters before pagination and matching detail fulfillment | `TestDuesFiltersBeforePagination` |
| Upgrade from migration 10 preserves dues/applications; missing current series version rejected at commit | `TestSeriesMigrationPreservesDueApplications` |

Sure was additionally consulted read-only in `app/models/recurrence_rule.rb`,
`test/models/recurrence_rule_test.rb` and
`app/jobs/generate_recurring_occurrences_job.rb`. Its rule coherency and serialized
generation were considered, without adopting its legacy implicit rules, worker
stack or unrelated nth-weekday frequencies. No source/schema was copied.

At that checkpoint, accountless privacy, subscription metadata/equivalents and
reminders remained pending. The continuation below supersedes that inventory;
blocks 7–14 were not started.

### Series delivery verification (7 November 2026)

`./scripts/dev.ps1 validate` passed on the final source: format, unit/contract
tests, module tidy without changes, reproducible sqlc/OpenAPI, migration
validation, full PostgreSQL/HTTP regression, vet, lint with zero issues, Linux
race detection and executable build/version inspection. Govulncheck reported
zero reachable and zero imported-package vulnerabilities, with one non-reachable
required-module advisory. Module-cache integrity was not rerun; dependencies did
not change. The explicit route allowlist was updated for all seven series routes
without weakening the contract test.

The calendar fuzz campaign passed 162752 executions over five seconds with two
workers. Bruno CLI 4.3.0 Safe Mode passed the standard smoke (14 requests, 38
assertions) and the series route through account creation, preview, creation,
generation/replay, agenda, pause and history (14 requests, 35 assertions). All
100 collection requests match OpenAPI. Migrations 11–12 and restricted local
grants were applied without deleting existing data. The temporary smoke server
was stopped before the final validation. These results validate this delivery,
not the still-incomplete block 6.

## Accountless privacy and conservation

Accountless dues/series return `unassigned:true`, an empty `account_id`, their
original currency/scale and `resource_id`. No dummy account or balance is created.
Occurrences share their series' access resource, not unrelated ACL copies.
`GET /dues?unassigned=true` filters before pagination.

`GET/PUT /planning-resources/{resource_id}/access` separates consultation from
administration. Only the responsible member administers. Sharing is explicit:
`private`, `selected` reader membership IDs, or `space`. Neither space ownership
nor administration grants a private-data exception. Shared readers cannot edit,
generate, apply payments or delegate administration. Updates require version,
confirmation, reason and recent authentication. A transfer requires a currently
authorized, active, non-reader recipient.

The original responsible membership episode governs immobilization. Departure
preserves resources, occurrences, applications, history and receipts without
publishing anything. Authorized shared consultation remains possible, but writes
require an active responsible member. A new membership restores neither
responsibility nor selected grants. With recent authentication,
`GET /planning-resources/recoverable` exposes only the same identity's ended
responsibility references; `POST /{id}/recover` explicitly binds responsibility to
its new membership. Other grants remain bound to their original episodes. History
follows the space lifecycle; no cleanup purges it or moves it to another space.

Applying a movement requires administration of the accountless resource and
operation permission on every actual account. It always requires
`confirm_access_restriction:true`: the actual account is a new sensitive relation.
Detail, agenda, totals and replay require resource access **and all active payment
account permissions**. Sharing planning never shares its payment. Unlinking and
voiding reuse the common fulfillment engine.

## Suggested metadata and financial subscriptions

Series conditions accept optional `counterparty_id`, `suggested_category_id` and
`subscription`. Catalog references must belong to this space, resolve to active
entries of the right kind, and match the payment/receipt classification direction.
Merged references retain their original evidence. Suggestions never classify or
create actual operations automatically. Occurrences expose their source version's
metadata, not replacement conditions.

`subscription` describes a personal financial contract, never a space entitlement.
It requires `plan`; `trial_until`, `renewal_on`, `cancel_by` and `cancellation_on`
are optional, manually recorded civil dates. Trial/renewal cannot precede the
anchor; a known cancellation deadline cannot follow renewal. Receipt series cannot
be subscriptions. Stopping tracking does not cancel the provider contract. Price
changes and conditions use ordinary immutable series versions.

Detail's `last_visible_charge` is the latest authorized active application's actual
current movement effect, date and currency. Restricted occurrences are excluded
before choosing the latest. Absence means no currently authorized evidence, not
that the provider never charged. Lists avoid per-series charge lookups; detail
performs one additional query.

`equivalents` are comparative minor-unit strings in the original currency, not
spending or cash flow. The explicit convention is **52 weeks and 12 months per
year**: weekly 52, biweekly 26, twice-monthly 24, monthly 12, yearly 1 occurrences
per comparative year; `every_weeks` uses 52/interval, `every_months` 12/interval.
Monthly uses annual frequency/12, not an already rounded annual result. Integer
arithmetic rounds each final output once, half to even. Ranges retain both bounds;
unknown stays null; int64 overflow yields `out_of_range` and null for the affected
equivalent. Trials, finite counts and end dates do not turn a steady-cadence
comparison into a forecast.

## Personal reminders and durable agenda inbox

The channel is `in_app`: PostgreSQL persists availability in the agenda, not proof
that a device displayed, received a push, or read a message. No push provider,
device-token infrastructure or mail adapter is introduced. No reminder creates a
payment, contacts a counterparty or changes a balance. Clients can poll the
authorized agenda; implementing a client is not a backend closure dependency.

`GET/PUT /dues/{id}/reminder` reads/configures the current member's **own** reminder.
Consult permission suffices, including readers; this uses current `read` capability,
not financial write authority. Version zero creates a configuration; subsequent
updates require its version. A due version, confirmation and reason protect against
unnoticed changes. Timing is 0–366 civil days before the due, hour/minute and an
explicit IANA zone. Calendar resolution reuses the first-valid-minute and
first-repeated-instant rules. Explicitly configuring a past time requests delivery
as soon as work runs.

`POST /dues/{id}/reminder/snooze` requires the reminder version and an explicit
future RFC3339 instant within 366 days. It versions the reminder and invalidates
old pending work, without changing the due date/version or fulfillment. The
original action can be replayed even after that instant passes. Renegotiating the
due date makes a reminder `stale`; it is neither silently moved nor delivered.
Review and reconfigure it explicitly.

`GET/PUT /series/{id}/reminder` manages a personal default for **future generation**.
Materialized reminders stay individually editable. Up to ten active membership
preferences per series are admitted under the space lock. Rejoining does not
restore ended membership defaults. Generation checks recipient identity,
membership, capability, global notification preference and series access. Past
reminder times are not backfilled. Dues, reminders, configuration evidence, outbox
jobs, checkpoint and receipt commit atomically. Bulk reminder/job inserts avoid
per-occurrence database round trips. Job batches are capped at 1000 and share
queue admission/deduplication with single jobs. Queue exhaustion or insertion
failure rolls back the complete generation; a shorter horizon can reduce admission.

The notifications worker handles `planning.reminder`; payloads contain only opaque
space/reminder/version references. Delivery revalidates recipient episode, identity,
global preference, current space capability, resource and all sensitive relations,
reminder revision, enabled flag, date, time and unpaid fulfillment. Revoked,
superseded, paid, skipped or cancelled work becomes obsolete. Delivery is an
idempotent transaction with a unique reminder/version receipt and audit. Recovery
after losing worker completion cannot duplicate the inbox item. No transaction
waits for an external service. Failures use existing bounded retries, leases,
status inspection and audited operational retry.

Agenda responses include only the caller's reminders. `reminder=scheduled`,
`delivered` or `stale` filters apply with permissions before pagination. Opening an
old delivered item still requires current access. Delivery failure never reverses
a financial fact. Disabling global notifications suppresses pending delivery;
changing a series default does not rewrite materialized reminders. Configuration,
delivery evidence and receipts follow the space context. Terminal job payloads use
the existing cleanup policy.

Personal reminder command replay also checks the original membership episode,
not just the current identity and a still-shared due or series. Rejoining cannot
turn an old configuration receipt into a successful action for a new episode,
even if the new membership subsequently creates its own preference. The series
preference receipt records that episode; its public result remains the series
reference for navigation.

### Continuation evidence

| Requirement | Tests |
| --- | --- |
| Accountless defaults, explicit sharing, no owner exception, private actual payments and pagination | `TestAccountlessPrivacyAndApplications` |
| No dummy positions, scales, conservation, rejoining without restoration, explicit recovery | `TestAccountlessConservationAndSeries` |
| Typed catalog references, cross-space rejection, historical conditions/prices and private last charge | `TestSeriesMetadataAndSubscriptions` |
| All comparative frequencies, rounding, range/unknown/overflow and contract dates | `TestSubscriptionEquivalents`, `TestFinancialSubscriptionValidation` |
| Civil reminder offsets, missing/repeated times, zones and underflow | `TestReminderTiming` and existing calendar tests |
| Durable inbox, concurrent delivery deduplication, snooze/replay, stale dates and no money movement | `TestRemindersDurableDeliveryAndSnooze` |
| Preferences, reader settings, removal/rejoining and current access on replay | `TestReminderRevocationAndPreferences` |
| Generated defaults, future jobs, outbox rollback and replay | `TestSeriesRemindersAndAtomicRollback` |
| Single/bulk fingerprint compatibility, concurrent admission, scheduling, terminal replay, conflict and capacity | `TestExecutionGuarantees/scheduled_batch_admission_and_deduplication` |
| Lost committed TCP replies and replacement service, without duplicating access resources or reminder jobs | `TestFinancialCompletionLostResponses/accountless-due`, `/reminder` |

Migrations 13–15 extend the published schema without modifying 1–12. The migration
regression upgrades from 10 preserving dues, applications and balances. Accountless
rows must be resolved explicitly before downgrading; migrations do not delete them
to force compatibility. Runtime history/delivery evidence grants are SELECT/INSERT
only. No dependency was added.

Additional Sure consultation was read-only: `app/models/recurring_transaction.rb`
(`monthly_equivalent_amount`), `recurring_price_change.rb`, `recurring_occurrence.rb`
and `recurring_allocation.rb`. Approximate normalization, automatic detection,
nullable payment facts and snooze-as-effective-due-date were not adopted. This
backend uses explicit rational comparison, actual movements and independent
reminder schedules. No source or schema was copied.

### Final closure verification (7 November 2026)

`./scripts/dev.ps1 validate` passed on stable final source: format, uncached
unit/contract tests, module tidy without differences, reproducible sqlc/OpenAPI,
migration validation, complete PostgreSQL/HTTP regression, vet, lint (zero issues),
Linux race detection, vulnerability scan and executable build/version inspection.
The access integration package completed in 231.726 seconds within its five-minute
budget; this is test execution evidence, not a production latency measurement.
Govulncheck reported zero reachable and zero imported-package vulnerabilities,
with one non-reachable required-module advisory. No dependency changed; the
separate module-cache integrity task was not rerun.

All 109 collection requests match OpenAPI. Bruno CLI 4.3.0 Safe Mode passed the
standard smoke (14 requests, 38 assertions) and the completion route (23 requests,
30 assertions plus after-response checks): unassigned expected income, private
access configuration, personal reminder/replay/snooze, unchanged financial date,
personal subscription/comparative cost, future reminder defaults, atomic occurrence
generation, authorized agenda, versioned price/cancellation and preserved existing
obligations. The route was repeated against final source after membership-episode
replay hardening. Only synthetic local examples were used; the temporary server
was stopped afterward.

Migrations 13–15 and restricted local grants were applied without deleting existing
data. Block 6's backend gate is complete. Agreements and card-specific sources
remain in their assigned blocks 7–8; no subsequent block or client was started.
