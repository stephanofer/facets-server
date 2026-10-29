#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('setup', 'doctor', 'generate', 'format', 'build', 'run', 'test', 'integration', 'race', 'lint', 'validate', 'db-up', 'db-down')]
    [string] $Task = 'validate'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$environment = Get-Content (Join-Path $root 'environment.json') -Raw | ConvertFrom-Json
$previousToolchain = $env:GOTOOLCHAIN
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
    Get-ChildItem 'tests/environment/fixturedb' -Filter '*.go' -File | ForEach-Object {
        $snapshot[$_.Name] = (Get-FileHash $_.FullName -Algorithm SHA256).Hash
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
    Invoke-Checked $binary @()
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
        'generate' { Invoke-Checked go @('tool', 'sqlc', 'generate') }
        'format' { Invoke-Checked go @('tool', 'golangci-lint', 'fmt') }
        'build' { Invoke-Build }
        'run' { Invoke-Checked go @('run', './cmd/server') }
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
            Invoke-Checked go @('tool', 'goose', '-dir', 'tests/environment/testdata/migrations', 'validate')
            Invoke-Checked go @('vet', '-tags=integration', './...')
            Invoke-Checked go @('tool', 'golangci-lint', 'run')
            Invoke-Checked go @('test', '-count=1', '-timeout=2m', './...')
            Invoke-Integration
            Invoke-Race
            Invoke-Checked go @('tool', 'govulncheck', '-tags=integration', '-test', './...')
            Invoke-Build
            Write-Host 'All block 1 validation gates passed.'
        }
    }
}
finally {
    $env:GOTOOLCHAIN = $previousToolchain
    Pop-Location
}
