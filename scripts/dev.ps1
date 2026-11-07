#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('setup', 'doctor', 'generate', 'openapi', 'api-client-check', 'api-client-smoke', 'format', 'check', 'build', 'run', 'worker', 'migrate', 'db-config', 'test', 'integration', 'race', 'lint', 'verify', 'validate', 'db-up', 'db-down')]
    [string] $Task = 'validate',
    [string[]] $Packages = @('./...')
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$environment = Get-Content (Join-Path $root 'environment.json') -Raw | ConvertFrom-Json
$previousToolchain = $env:GOTOOLCHAIN
$previousRuntime = @{}
$taskTimer = [Diagnostics.Stopwatch]::StartNew()
$validationLock = $null
Push-Location $root

function Invoke-Checked {
    param([string] $File, [string[]] $Arguments)
    Write-Host "Running: $File $($Arguments -join ' ')"
    $timer = [Diagnostics.Stopwatch]::StartNew()
    try {
        & $File @Arguments
        if ($LASTEXITCODE -ne 0) {
            throw "$File $($Arguments -join ' ') failed (exit $LASTEXITCODE)."
        }
    }
    finally {
        Write-Host ('Elapsed: {0:N2}s — {1}' -f $timer.Elapsed.TotalSeconds, $File)
    }
}

function Get-OwnedGoFiles {
    foreach ($folder in @('cmd', 'internal', 'migrations', 'tests')) {
        Get-ChildItem $folder -Filter '*.go' -File -Recurse | ForEach-Object { $_.FullName }
    }
}

function Invoke-Format {
    param([switch] $Check)
    $goRoot = & go env GOROOT
    if ($LASTEXITCODE -ne 0) { throw 'Unable to resolve pinned Go formatter.' }
    $formatter = Join-Path $goRoot 'bin/gofmt'
    if ($IsWindows) { $formatter += '.exe' }
    $files = @(Get-OwnedGoFiles)
    if ($Check) {
        $timer = [Diagnostics.Stopwatch]::StartNew()
        $unformatted = @(& $formatter -l @files)
        if ($LASTEXITCODE -ne 0) { throw 'Go format check failed.' }
        Write-Host ('Elapsed: {0:N2}s — gofmt check' -f $timer.Elapsed.TotalSeconds)
        if ($unformatted.Count -ne 0) {
            throw "Unformatted Go files. Run format first: $($unformatted -join ', ')"
        }
    }
    else { Invoke-Checked $formatter (@('-w') + $files) }
}

function Get-ValidationFingerprint {
    $files = @(Get-OwnedGoFiles)
    foreach ($folder in @('cmd', 'internal', 'migrations', 'tests', 'scripts', '.github')) {
        $files += @(Get-ChildItem $folder -File -Recurse | ForEach-Object { $_.FullName })
    }
    $files += @(Get-ChildItem $root -File | Where-Object {
        $_.Name -in @('go.mod', 'go.sum', 'environment.json', 'sqlc.yaml', '.golangci.yml', 'compose.yaml')
    } | ForEach-Object { $_.FullName })
    $files += (Join-Path $root 'docs/openapi.json')
    # Include collection inputs, but never local dotenv credentials or CLI caches.
    $files += @(Get-ChildItem (Join-Path $root 'api-client') -File -Recurse |
        Where-Object { $_.Extension -in @('.yml', '.yaml', '.bru') -and $_.FullName -notmatch '[\\/](node_modules|\.git)[\\/]' } |
        ForEach-Object { $_.FullName })
    $hashes = foreach ($file in $files | Sort-Object -Unique) {
        '{0}:{1}' -f $file, (Get-FileHash $file -Algorithm SHA256).Hash
    }
    return $hashes -join "`n"
}

function Get-GeneratedSnapshot {
    $snapshot = @{}
    foreach ($folder in @('tests/environment/fixturedb', 'internal/execution/executiondb', 'internal/access/accessdb', 'internal/finance/financedb')) {
      Get-ChildItem $folder -Filter '*.go' -File | ForEach-Object {
        $snapshot[$_.FullName] = (Get-FileHash $_.FullName -Algorithm SHA256).Hash
      }
    }
    return $snapshot
}

function Assert-Generation {
    $before = Get-GeneratedSnapshot
    Invoke-Checked go @('tool', 'sqlc', 'generate')
    $after = Get-GeneratedSnapshot
    if ($before.Count -ne $after.Count) { throw 'Generated files are stale. Run generate and review the diff.' }
    foreach ($name in $before.Keys) {
        if ($before[$name] -ne $after[$name]) {
            throw 'Generated files are stale. Run generate and review the diff.'
        }
    }
}

function Import-LocalConfiguration {
    if (-not (Test-Path '.env')) { throw 'Run setup first.' }
    foreach ($line in Get-Content '.env') {
        if ($line -match '^([A-Z][A-Z0-9_]*)=(.*)$') {
            $key, $value = $Matches[1], $Matches[2]
            # Explicit process settings, such as a worker's diagnostic port,
            # take precedence over local defaults.
            if (-not [string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($key, 'Process')) -and -not $previousRuntime.ContainsKey($key)) { continue }
            if (-not $previousRuntime.ContainsKey($key)) { $previousRuntime[$key] = [Environment]::GetEnvironmentVariable($key, 'Process') }
            [Environment]::SetEnvironmentVariable($key, $value, 'Process')
        }
    }
    if (-not $env:FACETS_APP_PASSWORD) { throw 'Run db-config to initialize application credentials.' }
    if (-not $previousRuntime.ContainsKey('FACETS_DATABASE_URL')) { $previousRuntime['FACETS_DATABASE_URL'] = $env:FACETS_DATABASE_URL }
    $password = [uri]::EscapeDataString($env:FACETS_APP_PASSWORD)
    $database = [uri]::EscapeDataString($env:POSTGRES_DB)
    $env:FACETS_DATABASE_URL = "postgres://facets_app:$password@127.0.0.1:$($env:POSTGRES_PORT)/${database}?sslmode=disable"
}

function Invoke-LocalRuntime {
    param([ValidateSet('serve', 'worker')] [string] $Mode)
    $explicitDiagnostics = -not [string]::IsNullOrEmpty($env:FACETS_DIAGNOSTICS_ADDRESS)
    Import-LocalConfiguration
    # The template's shared default is for HTTP. Give the local worker its own
    # port, preserving both session overrides and custom addresses in .env.
    if ($Mode -eq 'worker' -and -not $explicitDiagnostics -and
        $env:FACETS_DIAGNOSTICS_ADDRESS -in @('', $null, '127.0.0.1:9090')) {
        if (-not $previousRuntime.ContainsKey('FACETS_DIAGNOSTICS_ADDRESS')) {
            $previousRuntime['FACETS_DIAGNOSTICS_ADDRESS'] = $env:FACETS_DIAGNOSTICS_ADDRESS
        }
        $env:FACETS_DIAGNOSTICS_ADDRESS = '127.0.0.1:9091'
    }
    Invoke-Checked go @('run', './cmd/server', '-mode', $Mode)
}

function Invoke-Migrate {
    Import-LocalConfiguration
    $previousRuntime['FACETS_MIGRATION_URL'] = $env:FACETS_MIGRATION_URL
    $password = [uri]::EscapeDataString($env:FACETS_MIGRATOR_PASSWORD)
    $database = [uri]::EscapeDataString($env:POSTGRES_DB)
    $env:FACETS_MIGRATION_URL = "postgres://facets_migrator:$password@127.0.0.1:$($env:POSTGRES_PORT)/${database}?sslmode=disable"
    try { Invoke-Checked go @('run', './cmd/server', '-mode', 'migrate') }
    finally {
        if ($null -eq $previousRuntime['FACETS_MIGRATION_URL']) {
            Remove-Item Env:FACETS_MIGRATION_URL -ErrorAction SilentlyContinue
        }
        else { $env:FACETS_MIGRATION_URL = $previousRuntime['FACETS_MIGRATION_URL'] }
    }
}

function Initialize-DatabaseRoles {
    foreach ($key in @('FACETS_APP_PASSWORD', 'FACETS_MIGRATOR_PASSWORD', 'FACETS_ACCESS_KEY')) {
        $text = Get-Content '.env' -Raw
        if ($text -notmatch "(?m)^$key=[^\r\n]+\r?$") {
            $password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
            if ($text -match "(?m)^$key=.*$") { $text = [regex]::Replace($text, "(?m)^$key=.*$", "$key=$password") }
            else { $text += "`n$key=$password`n" }
            Set-Content '.env' $text -NoNewline -Encoding utf8
        }
    }
    Import-LocalConfiguration
    if ($env:POSTGRES_DB -notmatch '^[a-zA-Z_][a-zA-Z0-9_]*$' -or $env:POSTGRES_USER -notmatch '^[a-zA-Z_][a-zA-Z0-9_]*$') { throw 'Local bootstrap names must be SQL identifiers.' }
    $appPassword = $env:FACETS_APP_PASSWORD.Replace("'", "''")
    $migrationPassword = $env:FACETS_MIGRATOR_PASSWORD.Replace("'", "''")
    # SQL and passwords travel through stdin, never command arguments or task logs.
    $sql = @"
SELECT 'CREATE ROLE facets_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='facets_migrator')\gexec
SELECT 'CREATE ROLE facets_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='facets_app')\gexec
ALTER ROLE facets_migrator PASSWORD '$migrationPassword';
ALTER ROLE facets_app PASSWORD '$appPassword';
GRANT CONNECT ON DATABASE "$($env:POSTGRES_DB)" TO facets_migrator,facets_app;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE,CREATE ON SCHEMA public TO facets_migrator;
GRANT USAGE ON SCHEMA public TO facets_app;
"@
    $sql | & docker compose --env-file .env exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U $env:POSTGRES_USER -d $env:POSTGRES_DB
    if ($LASTEXITCODE -ne 0) { throw 'Database role provisioning failed.' }
    Invoke-Migrate
    $grants = @'
GRANT SELECT,INSERT,UPDATE ON action_receipts,jobs TO facets_app;
GRANT SELECT,INSERT,UPDATE ON principals,recovery_codes,sessions,identity_tokens,access_limits,spaces,memberships,space_access,space_capabilities,invitations,access_deliveries TO facets_app;
GRANT SELECT,INSERT,UPDATE ON financial_accounts TO facets_app;
GRANT SELECT,INSERT,DELETE ON account_grants TO facets_app;
GRANT SELECT,INSERT,UPDATE ON financial_catalog TO facets_app;
GRANT SELECT,INSERT ON catalog_merges,exchange_references TO facets_app;
GRANT SELECT,INSERT ON account_configuration_history,account_grant_history,catalog_configuration_history TO facets_app;
GRANT SELECT,INSERT ON financial_operations,operation_revisions,position_effects,economic_components,operation_tags TO facets_app;
GRANT SELECT,INSERT ON operation_refunds TO facets_app;
GRANT SELECT,INSERT ON account_reconciliations,reconciliation_reviews,balance_consistency_checks TO facets_app;
GRANT UPDATE(current_revision) ON financial_operations TO facets_app;
GRANT SELECT,INSERT ON technical_audit TO facets_app;
REVOKE UPDATE,DELETE,TRUNCATE ON technical_audit FROM facets_app;
GRANT USAGE ON SEQUENCE technical_audit_id_seq TO facets_app;
GRANT SELECT ON goose_db_version TO facets_app;
'@
    $grants | & docker compose --env-file .env exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U $env:POSTGRES_USER -d $env:POSTGRES_DB
    if ($LASTEXITCODE -ne 0) { throw 'Database runtime grants failed.' }
}

function Write-OpenAPI {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $schema = & go run ./cmd/server -mode openapi
    if ($LASTEXITCODE -ne 0) { throw 'OpenAPI generation failed.' }
    [IO.File]::WriteAllText((Join-Path $root 'docs/openapi.json'), ($schema -join "`n") + "`n")
    Write-Host ('Elapsed: {0:N2}s — OpenAPI generation' -f $timer.Elapsed.TotalSeconds)
}

function Assert-Docker {
    $info = & docker info --format '{{.OSType}}'
    if ($LASTEXITCODE -ne 0 -or $info -ne 'linux') {
        throw 'Start Docker with Linux containers before continuing.'
    }
}

function Assert-APIClient {
    # Always execute: Go's package filter/cache must not bypass collection edits.
    Invoke-Checked go @('test', '-count=1', '-timeout=2m', '-run', '^TestCollectionContract$', './tests/apiclient')
}

function Invoke-APIClientSmoke {
    Assert-APIClient
    if (-not (Get-Command bru -ErrorAction SilentlyContinue)) {
        throw 'Bruno CLI is optional and is not installed. Install @usebruno/cli@4.3.0 explicitly; see api-client/README.md.'
    }
    if (-not (Test-Path (Join-Path $root 'api-client/.env'))) {
        throw 'Configure api-client/.env and verify the local customer first; see api-client/README.md.'
    }
    Push-Location (Join-Path $root 'api-client')
    try {
        # No parallelism, reports, auto-installation, database provisioning or secrets in arguments.
        Invoke-Checked bru @('run', '--env', 'local', '--tags', 'smoke', '--bail', '--sandbox', 'safe')
    }
    finally { Pop-Location }
}

function Invoke-Doctor {
    Invoke-Checked go @('version')
    Assert-Docker
    Invoke-Checked docker @('version', '--format', 'Engine: {{.Server.Version}}')
    Invoke-Checked docker @('compose', 'version')
    Write-Host "Reference baseline: Go $($environment.go), Docker $($environment.dockerEngine), Compose $($environment.dockerCompose)."
}

function Initialize-Reaper {
    Assert-Docker
    # Testcontainers 0.44.0 fixes the Ryuk tag internally and has no image override.
    # Pull the locked digest and map that exact image to its expected local tag.
    Invoke-Checked docker @('pull', $environment.ryukImage)
    Invoke-Checked docker @('tag', $environment.ryukImage, ($environment.ryukImage -split '@')[0])
}

function Invoke-Integration {
    Initialize-Reaper
    Invoke-Checked go @('test', '-tags=integration', '-count=1', '-timeout=5m', './...')
}

function Invoke-Race {
    Assert-Docker
    # The official Debian Go image contains GCC. No Windows C compiler is required.
    Invoke-Checked docker @(
        'run', '--rm', '--init',
        '--mount', "type=bind,source=$root,target=/workspace,readonly",
        '--mount', 'type=volume,source=facets-go-modcache,target=/go/pkg/mod',
        '--mount', 'type=volume,source=facets-go-buildcache,target=/root/.cache/go-build',
        '--workdir', '/workspace', '--env', 'GOTOOLCHAIN=local', '--env', 'CGO_ENABLED=1',
        $environment.goImage,
        'go', 'test', '-mod=readonly', '-race', '-count=1', '-timeout=5m', './...'
    )
}

function Invoke-Build {
    New-Item artifacts -ItemType Directory -Force | Out-Null
    $binary = 'artifacts/facets-server'
    if ($IsWindows) { $binary += '.exe' }
    Invoke-Checked go @('build', '-mod=readonly', '-trimpath', '-o', $binary, './cmd/server')
    Invoke-Checked go @('version', '-m', $binary)
    Invoke-Checked $binary @('-mode', 'version')
}

try {
    $env:GOTOOLCHAIN = "go$($environment.go)"
    switch ($Task) {
        'setup' {
            Invoke-Doctor
            if (-not (Test-Path '.env') -or (Get-Item '.env').Length -eq 0) {
                $password = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
                $example = Get-Content '.env.example' -Raw
                $example.Replace('POSTGRES_PASSWORD=', "POSTGRES_PASSWORD=$password") |
                    Set-Content '.env' -NoNewline -Encoding utf8
                Write-Host 'Created ignored .env with a random local-only password.'
            }
            Invoke-Checked docker @('compose', '--env-file', '.env', 'config', '--quiet')
            Invoke-Checked go @('mod', 'download')
            Invoke-Checked go @('tool', 'sqlc', 'version')
            Invoke-Checked go @('tool', 'goose', '-version')
            Invoke-Checked go @('tool', 'golangci-lint', 'version')
            Invoke-Checked go @('tool', 'govulncheck', '-version')
            Invoke-Checked docker @('pull', $environment.postgresImage)
            Invoke-Checked docker @('pull', $environment.goImage)
            Initialize-Reaper
        }
        'doctor' { Invoke-Doctor }
        'generate' { Invoke-Checked go @('tool', 'sqlc', 'generate'); Write-OpenAPI }
        'openapi' { Write-OpenAPI }
        'api-client-check' { Assert-APIClient }
        'api-client-smoke' { Invoke-APIClientSmoke }
        'format' { Invoke-Format }
        'check' {
            Invoke-Format -Check
            Assert-APIClient
            # Local iteration only: retain Go's test cache. Not a closure gate.
            Invoke-Checked go (@('test', '-timeout=2m') + $Packages)
        }
        'build' { Invoke-Build }
        'run' { Invoke-LocalRuntime 'serve' }
        'worker' { Invoke-LocalRuntime 'worker' }
        'migrate' { Invoke-Migrate }
        'db-config' { Initialize-DatabaseRoles }
        'test' { Invoke-Checked go (@('test', '-count=1', '-timeout=2m') + $Packages) }
        'integration' { Invoke-Integration }
        'race' { Invoke-Race }
        'lint' { Invoke-Checked go @('tool', 'golangci-lint', 'run') }
        # Cache integrity is intentionally separate from local code validation.
        'verify' { Invoke-Checked go @('mod', 'verify') }
        'db-up' {
            Assert-Docker
            Invoke-Checked docker @('compose', '--env-file', '.env', 'up', '-d', '--wait', '--wait-timeout', '90', 'postgres')
        }
        'db-down' {
            # Deliberately preserve the named data volume.
            Invoke-Checked docker @('compose', '--env-file', '.env', 'down')
        }
        'validate' {
            New-Item artifacts -ItemType Directory -Force | Out-Null
            try {
                $validationLock = [IO.File]::Open((Join-Path $root 'artifacts/validation.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
            }
            catch { throw 'Another validation is active in this checkout. Wait for it; do not launch a duplicate.' }
            $sourceBefore = Get-ValidationFingerprint
            Invoke-Doctor
            Invoke-Format -Check
            Invoke-Checked pwsh @('-NoProfile', '-File', (Join-Path $root 'scripts/test-dev.ps1'))
            # Fail on inexpensive tests and stale generated files before lint,
            # race detection, vulnerability scanning and the final build.
            Invoke-Checked go @('test', '-count=1', '-timeout=2m', './...')
            Invoke-Checked go @('mod', 'tidy', '-diff')
            Invoke-Checked go @('tool', 'sqlc', 'compile')
            Assert-Generation
            $schemaBefore = (Get-FileHash 'docs/openapi.json' -Algorithm SHA256).Hash
            Write-OpenAPI
            if ($schemaBefore -ne (Get-FileHash 'docs/openapi.json' -Algorithm SHA256).Hash) { throw 'OpenAPI contract is stale. Review generated changes.' }
            Assert-APIClient
            Invoke-Checked go @('tool', 'goose', '-dir', 'tests/environment/testdata/migrations', 'validate')
            Invoke-Checked go @('tool', 'goose', '-dir', 'migrations/sql', 'validate')
            # Exercise real database behavior before the slower static analysis.
            # Keep heavy checks serial to avoid saturating the development host.
            Invoke-Integration
            Invoke-Checked go @('vet', '-tags=integration', './...')
            Invoke-Checked go @('tool', 'golangci-lint', 'run')
            Invoke-Race
            Invoke-Checked go @('tool', 'govulncheck', '-tags=integration', '-test', './...')
            Invoke-Build
            if ($sourceBefore -ne (Get-ValidationFingerprint)) {
                throw 'Validation inputs changed while checks were running. This is not a valid closure run; stabilize files before retrying.'
            }
            Write-Host 'Backend code validation passed. Module cache integrity is separate: run ./scripts/dev.ps1 verify (also required in CI).'
        }
    }
}
finally {
    if ($null -ne $validationLock) { $validationLock.Dispose() }
    if ($null -eq $previousToolchain) { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue }
    else { $env:GOTOOLCHAIN = $previousToolchain }
    foreach ($key in $previousRuntime.Keys) {
        if ($null -eq $previousRuntime[$key]) { Remove-Item "Env:$key" -ErrorAction SilentlyContinue }
        else { [Environment]::SetEnvironmentVariable($key, $previousRuntime[$key], 'Process') }
    }
    Pop-Location
    Write-Host ('Task {0} elapsed: {1:N2}s' -f $Task, $taskTimer.Elapsed.TotalSeconds)
}
