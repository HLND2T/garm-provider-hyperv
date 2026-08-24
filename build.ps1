[CmdletBinding()]
param(
    # Root of the GARM installation, taken from $env:GARM_ROOT when unset.
    # When provided, the default -OutputPath is <GarmRoot>\providers\garm-provider-hyperv.exe.
    [string]$GarmRoot = $env:GARM_ROOT,
    # Where to write the executable. Defaults to <GarmRoot>\providers\garm-provider-hyperv.exe
    # when -GarmRoot is set, otherwise bin\garm-provider-hyperv.exe inside this repo.
    [string]$OutputPath = $(if ($GarmRoot) { Join-Path $GarmRoot "providers\garm-provider-hyperv.exe" } else { Join-Path $PSScriptRoot "bin\garm-provider-hyperv.exe" }),
    [string]$Version = "v0.1.0"
)

$ErrorActionPreference = "Stop"
$projectRoot = $PSScriptRoot
$outputDirectory = Split-Path -Parent $OutputPath
New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null

Push-Location $projectRoot
try {
    go mod download
    go build -trimpath -ldflags "-s -w -X github.com/cloudbase/garm-provider-hyperv/provider.Version=$Version" -o $OutputPath .
}
finally {
    Pop-Location
}
