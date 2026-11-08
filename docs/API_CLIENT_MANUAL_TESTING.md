# Manual API testing with Bruno

Open `api-client/` as a collection and select `local`. Start the API and, when
testing mail/reminders, the worker. Use synthetic local data only. Authentication
setup and the non-financial smoke are documented in [the collection guide](../api-client/README.md).

## Navigation and selection

Finance folders group tasks, not automated scenarios. Requests are ordered within
each task; each folder has a Docs tab with prerequisites. Names still match the
OpenAPI operation IDs, so Bruno search also works with backend operation names.
Bodies are indented JSON; numeric versions are unquoted templates, while monetary
minor units remain strings. Dates are deliberately fixed synthetic examples, not
silently changed to today.

Creation selects its returned ID in memory. Financial lists do **not** silently
select the first account, operation, due, series, exchange rate or recovery target.
To select an existing resource, copy its authorized ID into the corresponding
Bruno runtime variable, then run its detail request. For example, select `accountId`
from `finance/accounts/list-financial-accounts`, then run
`finance/accounts/get-financial-account`. Use the same procedure with `operationId`,
`dueId`, `seriesId` or `catalogEntryId`. Do not put runtime IDs/tokens in tracked files.

Changing a selected resource clears its dependent versions/action keys before
the next finance request. Changing space resets finance state. Creating a new
resource does not clear its creation key: repeating creation intentionally recovers
the same action. `list-due-applications` preserves an existing active selection,
otherwise selects its first item; review `paymentApplicationId` before unlinking.
Pagination is explicit: copy `next_cursor` to the query `cursor` parameter and
enable it. Never reuse a cursor after changing filters or space.

## Core scenarios

Paths below are relative to `finance/`. Log in and select the intended space first.
Do not combine all rows into one runner sequence.

| Scenario | Requests, in order | Check manually |
| --- | --- | --- |
| Account | `accounts/propose-account-visibility` → `accounts/create-financial-account` → `accounts/get-financial-account` → `accounts/edit-financial-account` → detail again | Explicit private visibility; PEN 12000 opening minor units; rename leaves it active and does not change balance. |
| Catalog | `catalog/create-catalog-entry` → `catalog/get-catalog-entry` → `catalog/edit-catalog-entry` → detail again | Example creates/renames a tag, not a category. Change list `kind` to `tag` to find it. |
| Expense | Account detail → `operations/create-financial-operation` → `operations/get-financial-operation` → `operations/list-account-operation-history` | Expense is 1200 minor units, one real movement; initial 12000 position becomes 10800 if nothing else was recorded. Empty parts leave classification pending. |
| Correction | Operation detail → `operations/preview-operation-correction` → review → `operations/correct-financial-operation` → detail and history | Same replacement in preview/commit; corrected expense is 1500, revision increases, old evidence remains. Select `operationRevision` from revision summaries to inspect it. |
| Void (separate scenario) | Operation detail → review all effects → `operations/void-financial-operation` → detail/history | Effects stop contributing; evidence remains. Do not void a movement you still need for the payment scenario. |
| Existing-movement payment | `dues/create-due-item` → `dues/get-due-item` → operation detail → `payments/get-movement-application-capacity` → `payments/apply-payment` → due detail → `payments/list-due-applications` | A non-void expense and payment due on the same account/currency. Applying 1200 to a 2000 due is partial; it does not debit the account again. Capacity and apply use the same `effectOrdinal`. |
| New-movement payment (alternative) | Fresh due/detail + actual account → `payments/create-payment-operation` → due detail → applications | Creates and applies one expense atomically. Do not create it separately first or also run apply for the same payment. Operation detail is required before later revision-sensitive actions; its revision is not guessed from the creation response. |
| Unlink | Due detail → applications → review `paymentApplicationId` → `payments/unlink-payment` → due detail/capacity | Reopens fulfillment and releases capacity without reversing the real movement. |
| Recurrence | `series/preview-recurring-series` → `series/create-recurring-series` → `series/get-recurring-series` → `series/generate-recurring-occurrences` → `dues/list-due-items` | Same monthly rule in preview/create. Generate expectations through the exclusive horizon, without debiting money. Detail then `series/edit-recurring-series` tests a pause separately. |
| Reconciliation | Account detail → `reconciliation/preview-account-reconciliation` → review → `reconciliation/reconcile-account` → `reconciliation/list-account-reconciliations` | Same date/observed amount in both bodies; preview supplies the actual calculated balance. `adjust=true` writes an adjustment. Unknown calculation requires explicit `expected_calculated_minor: null` and `adjust: false`. |
| Consistency | `reconciliation/check-account-consistency` → `reconciliation/list-account-consistency-checks` | Records evidence without repairing the projection. |
| Exchange | `exchange/create-exchange-reference` → `exchange/get-exchange-reference` → `exchange/convert-informative-amount` | Historical USD/PEN at 3.75 converts 100 to 375 minor units, informative only. Assertions intentionally match this fixture. |

Search and summaries are in `insights/`; optional filters start disabled. The
summary separates original currencies and includes only currently authorized data.
Adjust example assertions when intentionally changing a fixture's rule/rate.

## Access, reminders and boundary cases

- **Account sharing:** read account/sharing before changing audience or transferring
  responsibility. Use another authorized membership, never a fabricated ID. Owners
  and admins do not bypass private-account authorization.
- **Accountless plans:** remove `account_id`, add `currency: "PEN"` in the due/series
  creation body, and start a new creation action. Detail captures `planningResourceId`.
  Read `planning-access/get-planning-access` before its explicit edit. Sharing a
  plan does not share a private payment or account.
- **Recovery:** after a same-identity rejoin, list recoverable references and
  explicitly choose both recovery ID and version. Listing alone restores nothing.
- **Due reminders:** due detail → `reminders/get-due-reminder` → configure. Zero is
  a valid unconfigured reminder version. Read again before a distinct snooze;
  explicitly set `reminderSnoozeUntil` to a future RFC3339 instant within 366 days.
  Series reminder defaults must be configured before generating future dues.
  Worker delivery appears in the authorized agenda, not as device push/email.
- **Version conflict:** read a resource, update it from another session, then submit
  the stale command. Expect conflict, not silent overwrite. Refresh only before
  preparing a new action.
- **Capacity/rollback:** intentionally overallocate or use an invalid target version
  on synthetic data. Verify failure leaves balances/applications unchanged. Cross-account
  allocations require deliberate access-restriction confirmation; cross-currency
  allocations require both actual amounts, not an inferred rate.
- **Negative tests:** default assertions expect success. For deliberate invalid
  requests, inspect the error status/code and unchanged state; a failed success
  assertion is expected. Do not commit temporary invalid fixtures or fixed replay keys.

## Retrying versus starting a new action

Retained-key commands keep the same action key, original body and original versions.
After a timeout/lost response, retry **without** refreshing detail/preview, selecting
another resource, changing the body or clearing the key. A successful retry must
recover the same result, not create another movement. Reading a newer version is
not part of retrying an already committed command.

For a distinct action, first refresh/review the relevant details, clear only that
action's runtime key, edit/review its body, then send. The key names are visible in
request headers/pre-request scripts (`operationActionKey`, `dueEditKey`,
`paymentApplyKey`, `seriesGenerateKey`, `reminderConfigureKey`, etc.). Reminder and
planning-access commands deliberately retain their submitted versions after success.
Other commands using `{{$randomUUID}}` create a new action on every click; use an
explicit fixed key temporarily only when intentionally testing their replay contract.

## Verification commands

```powershell
./scripts/dev.ps1 api-client-check
./scripts/dev.ps1 check -Packages ./tests/apiclient
node --test scripts/test-api-client.cjs
./scripts/dev.ps1 api-client-smoke
```

The optional JavaScript tests use Node and the YAML parser bundled with the globally
installed `@usebruno/cli@4.3.0`; they add no project dependency. They execute collection
scripts against synthetic responses to check selection isolation, captured fields,
prerequisites and retries. They do not replace Bruno Safe Mode or live backend tests.
The smoke needs the running API and a verified local customer, and deliberately
does not mutate financial resources. Run the selected financial scenarios manually
against the local API; neither the static contract check nor mocked script tests
prove transaction behavior or authorization at runtime.
