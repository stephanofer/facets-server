# Financial organization API

Block 4 introduces positions, access configuration, catalogs and manual exchange
references. It does not introduce everyday financial operations, agreements,
card statements or reconciliation; those use the positions in subsequent blocks.
The executable contract is [OpenAPI](openapi.json); examples are in
[`api-client/finance/`](../api-client/finance/).

## Positions and opening references

- Every account belongs to exactly one space and currency. `kind` identifies
  cash/bank/savings/wallet, obligations, or the origin of a receivable. `position`
  groups them without creating another balance. Card and agreement details are
  deliberately absent.
- `opening.date` is a civil date. `amount_minor` is a canonical signed int64
  string or `null`; `quality` is `unknown`, `estimated` or `confirmed`. Unknown
  requires null, not zero. Negative amounts are retained. The currency's standard
  scale is returned explicitly.
- Initially the current balance equals the reference. Creating it is not income,
  expense, a payment or an outbox event. Ordinary configuration cannot change
  currency, kind, opening reference or the derived balance. Financial corrections
  and reconciliation belong to the operation rules of block 5.
- Institution/alias text and optional four last digits are sufficient. No full
  account/card numbers are requested. Optional `counterparty_id` must resolve to
  an available counterparty in the same space; consolidation preserves its
  original identifier and its canonical target.
- `PUT /accounts/{account_id}` replaces the descriptive configuration and state.
  Omitted optional descriptive fields are cleared. Archive/close preserve all
  amounts, references and authorized historical access; they prevent operation.

## Access, responsibility and departure

Call `GET /accounts/proposal` before presenting creation. It uses the space's
preference and current membership, without revealing financial data or modifying
existing accounts. Creation always requires an explicit `sharing` object.

`private` permits only the current responsible membership. `space` additionally
permits all current/future members at `space_permission`. `selected` permits only
the named membership episodes at their individual read/operate permission.
The caller becomes responsible at creation. Specific grants are bounded to 100,
validated in one query and replaced in one batch, not one query per grantee.

The responsible member administers configuration, sharing and responsibility;
being an owner/admin of the space or having operate permission does not substitute
for this. Readers never operate or administer, even with an existing grant.
Responsibility transfers require recent authentication, a version, explicit
confirmation and a currently authorized non-reader target. Share first if the
target cannot currently see a private account.

Ending the responsible membership immediately makes the account effectively
`immobilized`; demotion of its responsible member to reader also prevents
operation. This state is derived from the original responsibility relationship,
so there is no maintenance-job window or reactivation on rejoining. `stored_state`
retains whether it was active, archived or closed. The account stays in its space
with identical positions and permissions; there is no forced zero or exposure to
space administrators. Resolve responsibility before departure when possible;
otherwise it remains immobilized until explicit regularization by its responsible
identity. No other member may claim a private account.

`GET /accounts/recoverable` requires recent authentication and returns only opaque
IDs and versions for the caller's former responsibilities, not financial content.
`POST /accounts/{account_id}/recovery` validates the same principal identity,
current non-reader membership, version and explicit confirmation before rebinding
responsibility. The stored archive/close state is preserved. Other ended grants
are not restored. Space-wide sharing still applies to new members by definition.

Listings authorize in SQL before cursor pagination. Details never disclose a
restricted account's existence. Account-sharing changes and responsibility
changes invalidate the existing space-scope artifacts conservatively. Scope
snapshots are not a substitute for checking sensitive account dependencies.

## Catalogs

Categories, merchants, counterparties and tags are space metadata, not global
directories. All current members may read them; owner/admin can manage them.
Names are case-insensitively unique among active peers. Categories have an
immutable income/expense direction, icon, color and at most two levels. Parents
must be active roots of the same direction and space. Archive active children
before archiving their parent; no children disappear implicitly.

New spaces seed seven short categories in their creation transaction. Stable
`default_key` values are independent of names or translations. Existing spaces
can explicitly initialize defaults through `POST /catalog/defaults`; existing
edited, archived and merged defaults are not overwritten. Custom active names
that collide with a missing default return a conflict rather than replacing data.

Merges require confirmation and both expected versions. Kinds/directions must
match and the target must be active. Children move to the target only when the
two-level invariant remains valid. Duplicate active names or incompatible depth
roll back the entire merge. Original rows remain archived aliases; aliases are
flattened and the original merge links remain in `catalog_merges`. Historical
references are not deleted or silently nullified. Future consumers use
`ResolveCatalog` within their authorized transaction rather than duplicating
alias logic. Reclassification and merging can change grouping of historical data,
never amounts or financial recognition.

Prior account configuration/grants and catalog configurations are captured in
structured revision tables before changes. The current row plus these prior
versions explain configuration history; old ACLs are never used for access.
This is configuration audit, not event sourcing or a second financial ledger.
Immutable history and exchange tables have no runtime UPDATE/DELETE privilege.
They follow the resource's life, independently of the 12-month technical audit.

## Exchange and base presentation

Manual rates express **target major units / source major unit**, with currency
pair, civil date, `historical`/`current` context, source, author, submission time,
digit count and `half_even` rounding. Rate strings are finite positive decimal
notation of at most 80 characters; exponent notation is deliberately rejected.
Corrections append a new reference with `replaces`, preserving the previous pair,
date and context. Old references remain readable and reproducible.

Informative conversion requires the caller's explicit reference ID. Historical
conversion requires the exact reference date; current valuation accepts an
explicit reference on or before the requested date. Contexts and directions are
never interchanged or inferred. Same-currency conversion requires no rate. For
different currencies without a reference the result is `complete:false`,
`amount_minor:null`, `reason:missing_exchange_reference`, never a 1:1 equivalent.
Overflow fails rather than clamping. Rounding occurs once at target minor units.
Conversions never write effective account amounts.

Changing the existing space setting `currency` changes base presentation only.
It does not relabel accounts, change opening references, replace recorded exchange
provenance, or convert stored amounts. Clients must explain that any subsequent
presentation uses the new base and still needs valid reference rates. Consolidated
reports and their missing-rate breakdowns are delivered in the report blocks;
this block does not publish a misleading consolidated account total.

## Composition, migration and limits

Migration 4 adds accounts and their configuration history. Migration 5 adds
catalogs, immutable exchange provenance and scoped counterparty relations.
Published migrations 1–3 remain unchanged. Run migrations explicitly using
`./scripts/dev.ps1 db-config` locally; runtime readiness requires version 5 and
never applies migrations at startup. Local provisioning grants the application
only its required table operations.

`finance.write` is a persisted action gate, not a per-account commercial quota.
New development profiles include it. Migration 4 explicitly adds it once to
existing non-commercial profiles without changing validity, usage, billing or
commercial grants. Missing/restricted/expired capabilities deny writes. Catalog
and exchange administration also use the existing `manage` capability. Real
commercial provisioning remains block 13, without a permissive fallback.

HTTP request bodies are bounded to 32 KiB; lists have stable ID cursor ordering
and 1–100 items. SQL timeouts/retries and the shared space lock coordinate
authorization, member removal, version checks and writes across processes. The
existing execution receipt revalidates current authorization on replay and keeps
the same committed ID without storing sensitive response bodies. Clients must
reuse their action key after an uncertain result, not generate a new one.

The composed server injects `finance.SeedDefaults` explicitly into space creation.
There is no global registry, runtime plugin discovery or hidden package init.
`AuthorizeAccount` is the transaction participant for subsequent operations;
authenticate and retain the same transaction, lock the space before accounts and
acquire multiple account locks in stable ID order. Old actors or cached flags are
never evidence of current permission.

## Requirement-to-test map

| Requirement | Evidence |
| --- | --- |
| Exact reference, unknown/null, negative/range and sharing validation | `internal/finance/accounts_test.go` |
| Space/account privacy, roles, pagination, first invitation, departure/rejoining and explicit recovery | `TestFinancialAccountsPrivacyAndLifecycle` |
| Concurrent action receipts, versions, archive, protected fields and capability denial | `TestFinancialAccountsCommandsAndConcurrency` |
| Defaults, category depth, cross-space references, identity-preserving merges and exchange provenance | `TestFinancialOrganizationAndExchange` |
| Transaction rollback, lost committed HTTP response, service replacement and concurrent edits | `TestFinancialRecoveryAndAtomicity` |
| In-flight resource authorization, scope invalidation and ended grantee episodes | `TestFinancialPermissionRevocation` |
| Explicit responsibility transfer without billing changes or replay access bypass | `TestFinancialResponsibilityTransfer` |
| Rate parsing and malformed/non-finite representations | `internal/finance/exchange_test.go` |
| Composed routes, security schemes, required path parameters and reproducible schemas | `internal/app/app_test.go` |
| Every request synchronized with the contract | `tests/apiclient/`, `./scripts/dev.ps1 api-client-check` |

Sure was consulted as a read-only design reference: `app/models/account.rb`
(scoped access without administrator exemption), `app/models/category.rb` and
`test/models/category_test.rb` (depth, tenant boundaries and merge behavior), plus
`test/models/account_test.rb` (state/failure handling). No Sure code/schema was
copied. Its destructive category/account behavior and broader product model were
not adopted: this backend preserves identities and financial history.
