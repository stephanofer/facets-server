#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('setup', 'doctor', 'generate', 'openapi', 'format', 'build', 'run', 'worker', 'migrate', 'db-config', 'test', 'integration', 'race', 'lint', 'validate', 'db-up', 'db-down')]
    [string] $Task = 'validate'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$environment = Get-Content (Join-Path $root 'environment.json') -Raw | ConvertFrom-Json
$previousToolchain = $env:GOTOOLCHAIN
$previousRuntime = @{}
Push-Location $root

function Invoke-Checked {
    param([string] $File, [string[]] $Arguments)
    Write-Host "Running: $File $($Arguments -join ' ')"
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$File failed (exit $LASTEXITCODE)."
    }
}

function Get-GeneratedSnapshot {
    $snapshot = @{}
    foreach ($folder in @('tests/environment/fixturedb', 'internal/execution/executiondb')) {
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
            if ($null -ne [Environment]::GetEnvironmentVariable($key, 'Process') -and -not $previousRuntime.ContainsKey($key)) { continue }
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

function Invoke-Migrate {
    Import-LocalConfiguration
    $previousRuntime['FACETS_MIGRATION_URL'] = $env:FACETS_MIGRATION_URL
    $password = [uri]::EscapeDataString($env:FACETS_MIGRATOR_PASSWORD)
    $database = [uri]::EscapeDataString($env:POSTGRES_DB)
    $env:FACETS_MIGRATION_URL = "postgres://facets_migrator:$password@127.0.0.1:$($env:POSTGRES_PORT)/${database}?sslmode=disable"
    try { Invoke-Checked go @('run', './cmd/server', '-mode', 'migrate') }
    finally { $env:FACETS_MIGRATION_URL = $previousRuntime['FACETS_MIGRATION_URL'] }
}

function Initialize-DatabaseRoles {
    foreach ($key in @('FACETS_APP_PASSWORD', 'FACETS_MIGRATOR_PASSWORD')) {
        $text = Get-Content '.env' -Raw
        if ($text -notmatch "(?m)^$key=.+$") {
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
GRANT SELECT,INSERT ON technical_audit TO facets_app;
REVOKE UPDATE,DELETE,TRUNCATE ON technical_audit FROM facets_app;
GRANT USAGE ON SEQUENCE technical_audit_id_seq TO facets_app;
GRANT SELECT ON goose_db_version TO facets_app;
'@
    $grants | & docker compose --env-file .env exec -T postgres psql -X -q -v ON_ERROR_STOP=1 -U $env:POSTGRES_USER -d $env:POSTGRES_DB
    if ($LASTEXITCODE -ne 0) { throw 'Database runtime grants failed.' }
}

function Write-OpenAPI {
    $schema = & go run ./cmd/server -mode openapi
    if ($LASTEXITCODE -ne 0) { throw 'OpenAPI generation failed.' }
    [IO.File]::WriteAllText((Join-Path $root 'docs/openapi.json'), ($schema -join "`n") + "`n")
}

function Assert-Docker {
    $info = & docker info --format '{{.OSType}}'
    if ($LASTEXITCODE -ne 0 -or $info -ne 'linux') {
        throw 'Start Docker with Linux containers before continuing.'
    }
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
        'format' { Invoke-Checked go @('tool', 'golangci-lint', 'fmt') }
        'build' { Invoke-Build }
        'run' { Import-LocalConfiguration; Invoke-Checked go @('run', './cmd/server') }
        'worker' { Import-LocalConfiguration; Invoke-Checked go @('run', './cmd/server', '-mode', 'worker') }
        'migrate' { Invoke-Migrate }
        'db-config' { Initialize-DatabaseRoles }
        'test' { Invoke-Checked go @('test', '-count=1', '-timeout=2m', './...') }
        'integration' { Invoke-Integration }
        'race' { Invoke-Race }
        'lint' { Invoke-Checked go @('tool', 'golangci-lint', 'run') }
        'db-up' {
            Assert-Docker
            Invoke-Checked docker @('compose', '--env-file', '.env', 'up', '-d', '--wait', '--wait-timeout', '90', 'postgres')
        }
        'db-down' {
            # Deliberately preserve the named data volume.
            Invoke-Checked docker @('compose', '--env-file', '.env', 'down')
        }
        'validate' {
            Invoke-Doctor
            Invoke-Checked go @('tool', 'golangci-lint', 'fmt', '--diff')
            Invoke-Checked go @('mod', 'tidy', '-diff')
            Invoke-Checked go @('mod', 'verify')
            Invoke-Checked go @('tool', 'sqlc', 'compile')
            Assert-Generation
            $schemaBefore = (Get-FileHash 'docs/openapi.json' -Algorithm SHA256).Hash
            Write-OpenAPI
            if ($schemaBefore -ne (Get-FileHash 'docs/openapi.json' -Algorithm SHA256).Hash) { throw 'OpenAPI contract is stale. Review generated changes.' }
            Invoke-Checked go @('tool', 'goose', '-dir', 'tests/environment/testdata/migrations', 'validate')
            Invoke-Checked go @('tool', 'goose', '-dir', 'migrations/sql', 'validate')
            Invoke-Checked go @('vet', '-tags=integration', './...')
            Invoke-Checked go @('tool', 'golangci-lint', 'run')
            Invoke-Checked go @('test', '-count=1', '-timeout=2m', './...')
            Invoke-Integration
            Invoke-Race
            Invoke-Checked go @('tool', 'govulncheck', '-tags=integration', '-test', './...')
            Invoke-Build
            Write-Host 'All backend validation gates passed.'
        }
    }
}
finally {
    $env:GOTOOLCHAIN = $previousToolchain
    foreach ($key in $previousRuntime.Keys) { [Environment]::SetEnvironmentVariable($key, $previousRuntime[$key], 'Process') }
    Pop-Location
}
