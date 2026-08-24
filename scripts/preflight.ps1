[CmdletBinding()]
param(
    [string]$CheckpointName = "garm-clean",
    # Provider executable to verify, taken from $env:GARM_PROVIDER_EXECUTABLE when unset.
    # Falls back to <GARM_ROOT>\providers\garm-provider-hyperv.exe when $env:GARM_ROOT is
    # set, otherwise ..\bin\garm-provider-hyperv.exe next to this script.
    [string]$ProviderExecutable = $env:GARM_PROVIDER_EXECUTABLE,
    # Require a Go toolchain on PATH. Only relevant when building from source;
    # a downloaded pre-built release does not need Go.
    [switch]$RequireGo,
    # Expected guests to verify: a path to a JSON file (array of
    # {"Name": "...", "OSType": "linux|windows"} objects) or inline JSON.
    # Defaults to the three reference guests; any number of guests is
    # supported, and names must match actual Hyper-V VMs.
    [string]$ExpectedGuests = ""
)

$ErrorActionPreference = "Stop"
if (-not $ProviderExecutable) {
    if ($env:GARM_ROOT) {
        $ProviderExecutable = Join-Path $env:GARM_ROOT "providers\garm-provider-hyperv.exe"
    } else {
        $ProviderExecutable = Join-Path $PSScriptRoot "..\bin\garm-provider-hyperv.exe"
    }
}
$defaultExpectedVMs = @(
    @{ Name = "Ubuntu22Runner"; OSType = "linux" },
    @{ Name = "Ubuntu24Runner"; OSType = "linux" },
    @{ Name = "Windows11Runner"; OSType = "windows" }
)

$expectedVMs = $defaultExpectedVMs
if ($ExpectedGuests) {
    $source = if (Test-Path -LiteralPath $ExpectedGuests) {
        Get-Content -LiteralPath $ExpectedGuests -Raw
    } else {
        $ExpectedGuests
    }
    # Windows PowerShell's ConvertFrom-Json emits a JSON array as a single
    # pipeline item, so a bare @(...) would nest it. ForEach-Object forces
    # each guest out as its own item; a one-element array or a single object
    # still yields exactly one guest.
    $expectedVMs = @(($source | ConvertFrom-Json) | ForEach-Object { $_ })
}

$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [Security.Principal.WindowsPrincipal]::new($identity)
$issues = [System.Collections.Generic.List[string]]::new()
$results = foreach ($expected in $expectedVMs) {
    $vm = Get-VM -Name $expected.Name -ErrorAction Stop
    $checkpoints = @(Get-VMCheckpoint -VMName $expected.Name -ErrorAction Stop)
    $integrationServices = @(Get-VMIntegrationService -VMName $expected.Name -ErrorAction Stop)
    $keyValueExchangeEnabled = $false
    foreach ($service in $integrationServices) {
        if ([bool]$service.Enabled -and $service.Id -match "2A34B1C2-FD73-4043-8A5B-DD2159BC743F") {
            $keyValueExchangeEnabled = $true
            break
        }
    }
    $baselineCheckpoints = @(
        $checkpoints | Where-Object { $_.Name -eq $CheckpointName }
    )
    $checkpointExists = $baselineCheckpoints.Count -eq 1

    if (-not $keyValueExchangeEnabled) {
        $issues.Add("$($expected.Name): Hyper-V key-value exchange must be enabled")
    }
    if (-not $checkpointExists) {
        $issues.Add("$($expected.Name): missing baseline checkpoint '$CheckpointName'")
    }
    if ([string]$vm.State -notin @("Off", "Saved", "Paused")) {
        $issues.Add("$($expected.Name): expected an idle state before GARM onboarding, got '$($vm.State)'")
    }

    [PSCustomObject]@{
        Name = $expected.Name
        OSType = $expected.OSType
        State = [string]$vm.State
        ProcessorCount = $vm.ProcessorCount
        MemoryStartupBytes = $vm.MemoryStartup
        Switches = @(
            Get-VMNetworkAdapter -VMName $expected.Name -ErrorAction Stop |
            Select-Object -ExpandProperty SwitchName
        )
        KeyValueExchangeEnabled = $keyValueExchangeEnabled
        BaselineCheckpoint = $CheckpointName
        BaselineCheckpointExists = $checkpointExists
    }
}

if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    $issues.Add("the current process is not elevated")
}
if ($RequireGo -and -not (Get-Command go -ErrorAction SilentlyContinue)) {
    $issues.Add("Go is not on PATH")
}
if (-not (Test-Path -LiteralPath $ProviderExecutable)) {
    $issues.Add("provider executable is missing: $ProviderExecutable")
}

[PSCustomObject]@{
    Host = $env:COMPUTERNAME
    Elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    ProviderExecutable = $ProviderExecutable
    Guests = @($results)
    Ready = $issues.Count -eq 0
    Issues = @($issues)
} | ConvertTo-Json -Depth 6

if ($issues.Count -gt 0) {
    exit 1
}
