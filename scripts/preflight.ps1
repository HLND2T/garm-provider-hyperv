[CmdletBinding()]
param(
    [string]$CheckpointName = "garm-clean"
)

$ErrorActionPreference = "Stop"
$expectedVMs = @(
    @{ Name = "Ubuntu22Runner"; OSType = "linux" },
    @{ Name = "Ubuntu24Runner"; OSType = "linux" },
    @{ Name = "Windows11Runner"; OSType = "windows" }
)

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
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $issues.Add("Go is not on PATH")
}
if (-not (Test-Path -LiteralPath "C:\garm\providers\garm-provider-hyperv.exe")) {
    $issues.Add("provider executable is missing")
}

[PSCustomObject]@{
    Host = $env:COMPUTERNAME
    Elevated = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
    ProviderExecutable = "C:\garm\providers\garm-provider-hyperv.exe"
    Guests = @($results)
    Ready = $issues.Count -eq 0
    Issues = @($issues)
} | ConvertTo-Json -Depth 6

if ($issues.Count -gt 0) {
    exit 1
}
