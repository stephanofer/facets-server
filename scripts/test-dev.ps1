#Requires -Version 7.4
# Exercise the real task bodies with simulated tools; never run Go or Docker.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$tokens = $null
$parseErrors = $null
$ast = [Management.Automation.Language.Parser]::ParseFile(
    (Join-Path $PSScriptRoot 'dev.ps1'), [ref] $tokens, [ref] $parseErrors)
if ($parseErrors.Count) { throw "dev.ps1 syntax errors: $parseErrors" }
$switch = $ast.Find({ param($node) $node -is [Management.Automation.Language.SwitchStatementAst] }, $true)
function Get-TaskBody([string] $Name) {
    $clause = @($switch.Clauses | Where-Object { $_.Item1.Value -eq $Name })
    if ($clause.Count -ne 1) { throw "Missing or duplicate task: $Name" }
    $text = $clause[0].Item2.Extent.Text
    return [scriptblock]::Create($text.Substring(1, $text.Length - 2))
}
$validate = Get-TaskBody 'validate'
$verify = Get-TaskBody 'verify'
$taskParameter = $ast.ParamBlock.Parameters | Where-Object { $_.Name.VariablePath.UserPath -eq 'Task' }
$taskValues = $taskParameter.Attributes | Where-Object { $_.TypeName.Name -eq 'ValidateSet' }
if ('verify' -notin $taskValues.PositionalArguments.Value) { throw 'verify must be an accepted task.' }

function Record([string] $Step) {
    $script:steps.Add($Step)
    if ($Step -eq $script:failAt) { throw "Simulated failure: $Step" }
}
function Invoke-Checked([string] $File, [string[]] $Arguments) {
    if ($File -eq 'pwsh') { Record 'script tests'; return }
    Record "$File $($Arguments -join ' ')"
}
function Invoke-Doctor { Record 'doctor' }
function Invoke-Format { param([switch] $Check); if (-not $Check) { throw 'Validation must not format files.' }; Record 'format check' }
function Assert-Generation { Record 'sqlc drift check' }
function Write-OpenAPI {
    Record 'openapi'
    if ($script:staleSchema) { [IO.File]::WriteAllText((Join-Path $root 'docs/openapi.json'), 'changed') }
}
function Invoke-Integration { Record 'integration' }
function Invoke-Race { Record 'race' }
function Invoke-Build { Record 'build' }
function Assert-APIClient { Record 'api-client drift check' }
function Get-ValidationFingerprint {
    $script:fingerprints++
    if ($script:changedInputs -and $script:fingerprints -gt 1) { return 'changed' }
    return 'stable'
}

function Run-Scenario([string] $Name, [string] $Failure = '', [switch] $Stale, [switch] $Changed, [switch] $Locked) {
    $script:steps = [Collections.Generic.List[string]]::new()
    $script:failAt = $Failure
    $script:staleSchema = $Stale.IsPresent
    $script:changedInputs = $Changed.IsPresent
    $script:fingerprints = 0
    $validationLock = $null
    $heldLock = $null
    [IO.File]::WriteAllText((Join-Path $root 'docs/openapi.json'), 'original')
    if ($Locked) {
        $heldLock = [IO.File]::Open((Join-Path $root 'artifacts/validation.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
    }
    $failureMessage = ''
    try { . $validate }
    catch { $failureMessage = $_.Exception.Message }
    finally {
        if ($null -ne $validationLock) { $validationLock.Dispose() }
        if ($null -ne $heldLock) { $heldLock.Dispose() }
    }
    $expected = if ($Locked) { 'Another validation is active' }
        elseif ($Stale) { 'OpenAPI contract is stale' }
        elseif ($Changed) { 'Validation inputs changed' }
        elseif ($Failure) { "Simulated failure: $Failure" }
        else { '' }
    if (($expected -and -not $failureMessage.StartsWith($expected)) -or (-not $expected -and $failureMessage)) {
        throw "${Name}: expected '$expected', got '$failureMessage'"
    }
    if ($Locked -and $script:steps.Count) { throw 'Locked validation executed checks.' }
    if ($Stale -and 'integration' -in $script:steps) { throw 'Stale OpenAPI did not stop before integration.' }
    if ($Failure -and $script:steps[-1] -ne $Failure) { throw "Checks continued after $Failure" }
    # Disposal must allow the next validation to acquire the lock after failure.
    $released = [IO.File]::Open((Join-Path $root 'artifacts/validation.lock'), 'OpenOrCreate', 'ReadWrite', 'None')
    $released.Dispose()
    Write-Host "PASS: $Name"
}

$tempBase = if ($IsWindows) { Join-Path $env:LOCALAPPDATA 'Temp/opencode' } else { [IO.Path]::GetTempPath() }
$root = Join-Path $tempBase ('facets-dev-test-' + [guid]::NewGuid().ToString('N'))
New-Item (Join-Path $root 'docs'), (Join-Path $root 'artifacts') -ItemType Directory -Force | Out-Null
Push-Location $root
try {
    Run-Scenario 'successful validation'
    $expectedSteps = @(
        'doctor', 'format check', 'script tests',
        'go test -count=1 -timeout=2m ./...', 'go mod tidy -diff',
        'go tool sqlc compile', 'sqlc drift check', 'openapi', 'api-client drift check',
        'go tool goose -dir tests/environment/testdata/migrations validate',
        'go tool goose -dir migrations/sql validate', 'integration',
        'go vet -tags=integration ./...', 'go tool golangci-lint run', 'race',
        'go tool govulncheck -tags=integration -test ./...', 'build')
    if (($script:steps -join "`n") -cne ($expectedSteps -join "`n")) { throw 'Unexpected validation checks or order.' }
    foreach ($step in $expectedSteps) { Run-Scenario "fail fast: $step" -Failure $step }
    Run-Scenario 'OpenAPI drift fails before integration' -Stale
    Run-Scenario 'changed inputs reject closure' -Changed
    Run-Scenario 'concurrent validation is rejected' -Locked
    $script:steps = [Collections.Generic.List[string]]::new()
    $script:failAt = ''
    . $verify
    if ($script:steps.Count -ne 1 -or $script:steps[0] -ne 'go mod verify') { throw 'verify must only check module integrity.' }
    $script:failAt = 'go mod verify'
    $failed = $false
    try { . $verify } catch { $failed = $_.Exception.Message -eq 'Simulated failure: go mod verify' }
    if (-not $failed) { throw 'verify swallowed a module integrity failure.' }
    Write-Host 'PASS: verify is separate and propagates failures'

    # A filtered fast check must still execute the collection guard.
    $script:steps = [Collections.Generic.List[string]]::new()
    $script:failAt = ''
    $Packages = @('./internal/money')
    . (Get-TaskBody 'check')
    if (($script:steps -join '|') -ne 'format check|api-client drift check|go test -timeout=2m ./internal/money') {
        throw 'Filtered check bypasses the collection guard.'
    }
    foreach ($name in @('Assert-APIClient', 'Invoke-APIClientSmoke')) {
        $definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
        . ([scriptblock]::Create($definition.Extent.Text))
    }
    $script:steps = [Collections.Generic.List[string]]::new()
    . (Get-TaskBody 'api-client-check')
    if (($script:steps -join '|') -ne 'go test -count=1 -timeout=2m -run ^TestCollectionContract$ ./tests/apiclient') {
        throw 'Collection guard must execute uncached without Bruno.'
    }
    New-Item (Join-Path $root 'api-client') -ItemType Directory -Force | Out-Null
    # Simulate an installed executable without invoking Node or Bruno.
    function bru { }
    $beforeLocation = (Get-Location).Path
    $failed = $false
    try { Invoke-APIClientSmoke } catch { $failed = $_.Exception.Message.StartsWith('Configure api-client/.env') }
    if (-not $failed) { throw 'Smoke must explain missing client configuration.' }
    Set-Content (Join-Path $root 'api-client/.env') 'CUSTOMER_EMAIL=synthetic@example.test'
    $script:smokeFailure = $false
    function Invoke-Checked([string] $File, [string[]] $Arguments) {
        if ($File -eq 'go') {
            if (($Arguments -join ' ') -ne 'test -count=1 -timeout=2m -run ^TestCollectionContract$ ./tests/apiclient') { throw 'Unexpected collection guard.' }
            return
        }
        if ($File -ne 'bru' -or ($Arguments -join ' ') -ne 'run --env local --tags smoke --bail --sandbox safe') { throw 'Unexpected smoke command.' }
        if ((Get-Location).Path -ne (Join-Path $root 'api-client')) { throw 'Smoke must run from the collection directory.' }
        if ($script:smokeFailure) { throw 'Simulated Bruno failure' }
    }
    . (Get-TaskBody 'api-client-smoke')
    if ((Get-Location).Path -ne $beforeLocation) { throw 'Smoke changed the caller directory.' }
    $script:smokeFailure = $true
    $failed = $false
    try { Invoke-APIClientSmoke } catch { $failed = $_.Exception.Message -eq 'Simulated Bruno failure' }
    if (-not $failed -or (Get-Location).Path -ne $beforeLocation) { throw 'Smoke swallowed a failure or leaked its directory.' }
    Remove-Item Function:bru
    Write-Host 'PASS: filtered check, uncached collection guard, smoke arguments, configuration, failures and directory restoration'

    # Exercise the actual local configuration and runtime functions, not copies.
    foreach ($name in @('Import-LocalConfiguration', 'Invoke-LocalRuntime')) {
        $definition = $ast.Find({ param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq $name }, $true)
        . ([scriptblock]::Create($definition.Extent.Text))
    }
    $saved = @{}
    foreach ($key in @('FACETS_DIAGNOSTICS_ADDRESS', 'FACETS_APP_PASSWORD', 'POSTGRES_DB', 'POSTGRES_PORT', 'FACETS_DATABASE_URL')) {
        $saved[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
        [Environment]::SetEnvironmentVariable($key, $null, 'Process')
    }
    function Invoke-Checked([string] $File, [string[]] $Arguments) {
        if ($File -ne 'go' -or ($Arguments -join ' ') -ne "run ./cmd/server -mode $script:expectedMode") { throw 'Unexpected local runtime command.' }
        if ($env:FACETS_DIAGNOSTICS_ADDRESS -ne $script:expectedDiagnostics) { throw "Unexpected diagnostics: $env:FACETS_DIAGNOSTICS_ADDRESS" }
    }
    try {
        foreach ($case in @(
            @{ Mode = 'serve'; Local = '127.0.0.1:9090'; Session = ''; Want = '127.0.0.1:9090' },
            @{ Mode = 'worker'; Local = '127.0.0.1:9090'; Session = ''; Want = '127.0.0.1:9091' },
            @{ Mode = 'worker'; Local = ''; Session = ''; Want = '127.0.0.1:9091' },
            @{ Mode = 'worker'; Local = '127.0.0.1:9191'; Session = ''; Want = '127.0.0.1:9191' },
            @{ Mode = 'worker'; Local = '127.0.0.1:9090'; Session = '127.0.0.1:9090'; Want = '127.0.0.1:9090' },
            @{ Mode = 'worker'; Local = '127.0.0.1:9090'; Session = '127.0.0.1:9292'; Want = '127.0.0.1:9292' },
            @{ Mode = 'serve'; Local = '127.0.0.1:9090'; Session = '127.0.0.1:9292'; Want = '127.0.0.1:9292' }
        )) {
            Set-Content '.env' "FACETS_APP_PASSWORD=synthetic`nPOSTGRES_DB=synthetic`nPOSTGRES_PORT=5432`nFACETS_DIAGNOSTICS_ADDRESS=$($case.Local)"
            $env:FACETS_DIAGNOSTICS_ADDRESS = $case.Session
            $before = $env:FACETS_DIAGNOSTICS_ADDRESS
            $previousRuntime = @{}
            $script:expectedMode = $case.Mode
            $script:expectedDiagnostics = $case.Want
            Invoke-LocalRuntime $case.Mode
            foreach ($key in $previousRuntime.Keys) {
                [Environment]::SetEnvironmentVariable($key, $previousRuntime[$key], 'Process')
            }
            if ($env:FACETS_DIAGNOSTICS_ADDRESS -ne $before) { throw 'Local runtime did not preserve the original diagnostics environment.' }
        }
        Write-Host 'PASS: local HTTP/worker defaults, explicit overrides and environment restoration'
    }
    finally {
        foreach ($key in $saved.Keys) { [Environment]::SetEnvironmentVariable($key, $saved[$key], 'Process') }
    }
}
finally {
    Pop-Location
    Remove-Item $root -Recurse -Force
}
