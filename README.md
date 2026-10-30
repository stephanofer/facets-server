# Facets server

Central Go backend for personal and shared financial tracking. This repository
contains the server only; Android is developed separately.

**Current scope: block 1 — development environment.** There are no business
endpoints, production tables, HTTP listener, credentials service or financial
calculations yet. The executable prints a scaffold message and exits. Runtime
configuration, restricted database roles, HTTP/OpenAPI, connection pools and
durable work are introduced in block 2, not simulated here.

## Prerequisites

- PowerShell 7.4 or newer (the team's verified environment uses 7.6.9).
- Git with your own author identity configured.
- Go with toolchain downloads enabled. Scripts select **Go 1.27.3** for every task;
  a compatible older launcher can download it without changing the system Go.
- Docker running **Linux containers**. Verified baseline: Engine **29.9.2** and
  Compose **5.6.1**. `doctor` reports installed versions; other host versions must
  pass the complete validation, not just a Docker version check.
- Network access to the Go proxy/checksum service, GitHub and Docker Hub on first
  use; enough disk for module, compiler and container caches.

No Make, Bash, global Go tool installs, optional application libraries, or Windows
C compiler is required. The race detector runs in the locked Debian Go image,
which includes GCC. Docker can execute its Linux processes from PowerShell.

## Start from a clean checkout

```powershell
git clone https://github.com/stephanofer/facets-server.git
Set-Location facets-server
./scripts/dev.ps1 setup
./scripts/dev.ps1 db-up
./scripts/dev.ps1 validate
./scripts/dev.ps1 run
```

`setup` preserves a nonempty `.env`; otherwise it initializes it from `.env.example`
with a random local-only database password. It downloads the pinned tools and
images. Never commit `.env`, use these credentials in production, or put real
financial information into development/test fixtures. Protect local files with
your operating system's user access controls.

Local PostgreSQL is bound to **127.0.0.1:5432**. Change `POSTGRES_PORT` in `.env` if
that port is occupied. Data persists in the Compose volume; `db-down` stops the
stack **without deleting the volume**. The PostgreSQL 18 image mounts its data
volume at `/var/lib/postgresql`, not the pre-18 data directory.

The bootstrap database identity is for local development only. It is not an
application/migration privilege model and no runtime code uses it in this block.
Changing initialization credentials in `.env` does not change an existing volume's
database credentials; do not delete data to work around that mismatch.

## Daily commands

All tasks are `./scripts/dev.ps1 <task>`:

| Task | Purpose |
| --- | --- |
| `doctor` | Report Go/Docker/Compose and require Linux containers. |
| `setup` | Prepare local configuration, pinned tools and images. |
| `db-up` / `db-down` | Start healthy local PostgreSQL / stop without deleting data. |
| `generate` | Regenerate the sqlc smoke fixture (never edit generated Go). |
| `format` / `lint` | Format owned Go files / run configured static analysis. |
| `test` | Run deterministic tests without Docker or the integration build tag. |
| `integration` | Run uncached integration tests with fresh Testcontainers PostgreSQL. |
| `race` | Run uncached unit tests with `-race` in pinned Linux Go/GCC. |
| `build` / `run` | Build and inspect the executable / execute the scaffold. |
| `validate` | Run every applicable block 1 gate; fail on any error. |

Validation checks formatting, tidy drift, module integrity, sqlc compilation and
generation drift, Goose migration syntax, vet, lint/security checks, unit tests,
real PostgreSQL migrations/SQL, race detection, reachable vulnerabilities
(`govulncheck`, including integration tests), and the built artifact. Generation
drift fails validation but leaves regenerated files available for review.
Run lint/validation tasks serially on the same host: golangci-lint deliberately
uses a global process lock, including across separate checkouts.
OpenAPI generation starts when an actual API is introduced in block 2; there is
no empty or invented contract to validate in block 1.

The first run may take several minutes compiling tool dependencies. Tools are
pinned with `tool` directives in `go.mod`, verified by `go.sum`, and run through
`go tool`; they are not linked into the application. The workflow in
`.github/workflows/validate.yml` runs the same script on pull requests and `main`.
GitHub-hosted runner versions are not an immutable host image: the workflow
reports them while PostgreSQL, race toolchain and action sources are pinned.

## Test isolation and reproducibility

`environment.json` pins PostgreSQL **18.6-trixie**, Go's race image and Ryuk by
version and registry digest. A test checks that Compose and Testcontainers share
the same PostgreSQL reference and that Go versions agree. Go tools/dependencies
are locked in `go.mod`/`go.sum`.

Integration tests never load `.env`, never use the local database, and never skip
when Docker is unavailable. They create a disposable database with random host
ports, apply/reapply/rollback/reapply Goose fixtures, assert PostgreSQL 18.6,
read/write through generated pgx/sqlc code, and test duplicate/missing rows. All
fixture data is synthetic. Cleanup is registered with Testcontainers; Ryuk is
enabled for interrupted-process cleanup.

Testcontainers 0.44.0 embeds Ryuk's version tag without a supported image override.
The integration script first pulls its locked digest and assigns the expected
local tag to that image, so the library uses the pinned image without patching
the dependency or disabling cleanup. Use the script as the canonical test entry.

The race task has a read-only source mount, named Go module/build caches, no Docker
socket and no database access. It checks in-memory races, not SQL concurrency.
Database concurrency scenarios will arrive with the features that require them.

## Repository conventions

- `cmd/server`: minimal executable scaffold.
- `internal/`: added by responsibility when implementation needs it; no empty
  module/layer trees are created in advance.
- `migrations/`: future global sequence of immutable production Goose migrations.
- `tests/environment/testdata`: disposable SQL/JSON fixtures, not product schemas.
- `tests/environment/fixturedb`: versioned sqlc output for the smoke fixture only.
- `scripts/dev.ps1`: shared PowerShell development/validation entry point.
- `artifacts/`: ignored local build output.

Use English identifiers, standard Go formatting, LF endings, tests alongside
implementation, and conventional commits (`chore`, `feat`, `fix`, etc.). Do not
commit secrets or generated build output. No commit hooks are silently installed.
Existing local `inspiration/`, `docs/huma/`, `.agents/`, `.opencode/` and
`skills-lock.json` are preserved but excluded from the backend repository; they
are reference/tooling material, not application dependencies.

Before updating versions, review compatibility, licensing and vulnerabilities;
regenerate, run all gates, and update the environment/design together. Do not
upgrade arbitrary transitive dependencies or install the rest of the planned
stack before it is used. Future Go skills should be routed through
`golang-how-to` when available, while project decisions take precedence.

## Design and progress

- [Product design](PRODUCT_DESIGN.md)
- [Technical design](TECHNICAL_DESIGN.md)
- [Development blocks](DEVELOPMENT_PLAN.md)
- [Project instructions](AGENTS.md)
