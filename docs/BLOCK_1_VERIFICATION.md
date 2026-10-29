# Block 1 verification

## Scope and traceability

This block prepares development only. It introduces no business modules,
production schema, runtime database access, HTTP endpoints or financial rules.

| Development plan requirement | Implementation and verification |
| --- | --- |
| Preserve existing work; initialize Git and conventions | `main`, configured `origin`, `.gitignore`, `.gitattributes`, `.editorconfig`; existing local references excluded, not deleted. |
| Fixed compatible tools and local PostgreSQL | Go 1.27.3 and tool directives/checksums; `environment.json`; digest-pinned PostgreSQL 18.6-trixie in Compose/Testcontainers. |
| Minimal module, configuration and PowerShell tasks | `go.mod`, `cmd/server`, `.env.example`, `.env.test.example`, `scripts/dev.ps1`; local `.env` never consumed by tests. |
| Goose, sqlc, static analysis and testing | Disposable fixture migrations/queries/JSON, versioned generated Go, integration build tag, lint/vet/race/vulnerability gates and generation/tidy drift checks. |
| Reproducible onboarding | `README.md`, shared validation script, pinned-source GitHub Actions workflow and clean-copy validation. |

## Verification record

**Result: passed on 2026-10-29.** Block 1 is closed in `DEVELOPMENT_PLAN.md`.

Verified host: Windows/amd64, PowerShell 7.6.9, Go 1.27.3, Docker Engine 29.9.2
with Linux containers, Compose 5.6.1. The race container uses Go 1.27.3 and
Debian GCC 14.2.0.

| Check | Observed result |
| --- | --- |
| `./scripts/dev.ps1 setup` | Correct pinned tool versions; missing/empty local configuration initialized with a random password. Repeating setup preserved the existing `.env` hash. |
| `db-up`, `db-down`, `db-up` | Healthy PostgreSQL; named data volume preserved across shutdown/restart. Local query returned `facets_local\|180006\|0` (database, version, public-table count). |
| Example credentials | Compose rejected `.env.example` because its password is empty, rather than starting with a fallback password. |
| Formatting, tidy, module integrity | No format/tidy drift; `all modules verified`. |
| sqlc and Goose | SQL compilation, reproducible generation and migration validation passed. |
| Vet and golangci-lint | Integration-tagged analysis passed; `0 issues.` |
| Unit and integration tests | Passed uncached. PostgreSQL 18.6, real Goose up/reapply/down/up, generated pgx reads/writes, SQLSTATE 23505, missing rows and empty recreated fixtures verified. |
| Linux `-race` | Passed with read-only source and GCC from the digest-pinned Go image. |
| `govulncheck -tags=integration -test` | Zero reachable vulnerabilities and zero affected imported packages; module-only OpenPGP advisory assessed below. |
| Artifact | Windows executable built, module/build metadata inspected and scaffold executed; no application dependencies linked beyond the standard library. |
| Clean-copy reproduction | Exported only staged/versioned files to a fresh approved temporary directory; no `.git`, `.env`, agent installation or reference checkouts copied. Fresh `setup` and the complete `validate` script both passed. |
| Git hygiene | Local secrets, artifacts and third-party references excluded; recognizable-secret and whitespace checks passed. |

The main working directory and the clean-copy run both printed
`All block 1 validation gates passed.` The final main-directory transcript is
kept locally at ignored `artifacts/block1-validation.txt`. Go/SQL test and module
sources were identical in the clean copy; the final script additionally logs
the command being executed for diagnostics. Documentation and closure records
were finalized after the runs.

Run validation serially on one host: a simultaneous main/clean-copy attempt hit
golangci-lint's global process lock and was rerun successfully in series. No lock
protection was disabled. The initial integration check exposed a test-only text
scan mismatch for `SHOW server_version_num`; the successful regression uses an
explicit SQL integer conversion.

## Dependency review

The 2026-10-27 vulnerability database reports `GO-2026-5932` at module level for
`golang.org/x/crypto@v0.58.0`: its deprecated `openpgp` package is unsafe by design
and has no fixed version. Verbose `govulncheck -tags=integration -test` reports
**zero affected imported packages and zero reachable vulnerabilities**. An explicit
`go list -deps` of all four pinned tool executables also confirms no OpenPGP
import. No finding is suppressed and no unrelated module upgrade is applied;
OpenPGP must not be introduced. Recheck reachability when dependencies change.

Direct dependency/tool source licenses reviewed from their pinned modules:
pgx, Goose, sqlc and Testcontainers use MIT; `golang.org/x/vuln` uses BSD-3-Clause;
golangci-lint uses GPL-3.0. The linter is an external development tool, not linked
into the application. This is not a license audit of every future release artifact;
redistribution must preserve applicable notices and obligations.

Go module resolution may include telemetry, crypto, SQL-driver and analyzer
dependencies required by the test/tool graphs. They are not direct product
features or proof that runtime observability/authentication has been implemented.
The scaffold binary's module metadata is inspected by the build gate.

## Boundaries for block 2

Runtime configuration validation, distinct application/migration credentials,
HTTP/Huma/OpenAPI, production migrations, pool lifecycle, durable work, financial
primitive types and shared transactional guarantees remain block 2 work.
No client implementation is required to close this backend preparation block.

The CI workflow is configured but cannot be claimed as executed on GitHub until
the repository is pushed and a workflow run completes. The initial commit is local;
no push or GitHub repository-settings changes are part of this session.
