[CmdletBinding()]
param(
    [string]$OutputPath = "C:\garm\providers\garm-provider-hyperv.exe",
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
