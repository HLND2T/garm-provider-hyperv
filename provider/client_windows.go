// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

//go:build windows

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/cloudbase/garm-provider-hyperv/config"
)

type windowsClient struct {
	powerShellPath string
	sshPath        string
}

var setExecutionPolicyCommand = regexp.MustCompile(`(?mi)^[^\r\n]*\bSet-ExecutionPolicy\b[^\r\n]*(?:\r?\n|$)`)
var bootstrapCredentialValue = regexp.MustCompile(`(?i)(BEARER_TOKEN=|Authorization:\s*Bearer\s+)([A-Za-z0-9._~-]+)`)

func newWindowsClient(cfg *config.Config) (Client, error) {
	return &windowsClient{
		powerShellPath: cfg.PowerShell(),
		sshPath:        cfg.SSH(),
	}, nil
}

func (c *windowsClient) ListVMs(ctx context.Context, names []string) ([]vmInfo, error) {
	var response struct {
		Items []vmInfo `json:"items"`
	}
	err := c.invokeJSON(ctx, `
$neighbors = @(
    Get-NetNeighbor -AddressFamily IPv4 -ErrorAction SilentlyContinue
)

function Normalize-MacAddress {
    param([string]$Address)
    if ([string]::IsNullOrWhiteSpace($Address)) { return '' }
    return ($Address -replace '[^0-9A-Fa-f]', '').ToUpperInvariant()
}

$items = @()
foreach ($name in @($payload.names)) {
    $vm = Get-VM -Name $name -ErrorAction Stop
    $adapters = @(
        Get-VMNetworkAdapter -VMName $name -ErrorAction SilentlyContinue
    )
    $ips = @(
        $adapters |
        ForEach-Object { $_.IPAddresses } |
        Where-Object { $_ }
    )
    foreach ($adapter in $adapters) {
        $mac = Normalize-MacAddress ([string]$adapter.MacAddress)
        if ($mac -eq '') { continue }
        $ips += @(
            $neighbors |
            Where-Object { (Normalize-MacAddress ([string]$_.LinkLayerAddress)) -eq $mac } |
            ForEach-Object { $_.IPAddress } |
            Where-Object { $_ }
        )
    }
    $items += [PSCustomObject]@{
        id = [string]$vm.Id
        name = [string]$vm.Name
        state = [string]$vm.State
        notes = [string]$vm.Notes
        ip_addresses = @($ips | Sort-Object -Unique)
    }
}
[PSCustomObject]@{ items = @($items) } | ConvertTo-Json -Compress -Depth 5
`, map[string]any{"names": names}, &response)
	if err != nil {
		return nil, err
	}
	return response.Items, nil
}

func (c *windowsClient) PrepareForAllocation(ctx context.Context, name, checkpoint string) error {
	return c.invokeJSON(ctx, `
function Wait-ForVMState {
    param([string]$VMName, [string]$ExpectedState)
    $deadline = (Get-Date).AddMinutes(3)
    do {
        $state = [string](Get-VM -Name $VMName -ErrorAction Stop).State
        if ($state -eq $ExpectedState) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "VM '$VMName' did not reach state '$ExpectedState'; current state: '$state'"
}

$vm = Get-VM -Name $payload.name -ErrorAction Stop
if ([string]$vm.State -ne 'Off') {
    Stop-VM -Name $payload.name -TurnOff -Force -ErrorAction Stop | Out-Null
    Wait-ForVMState -VMName $payload.name -ExpectedState 'Off'
}
Restore-VMCheckpoint -VMName $payload.name -Name $payload.checkpoint -Confirm:$false -ErrorAction Stop | Out-Null
Start-VM -Name $payload.name -ErrorAction Stop | Out-Null
Wait-ForVMState -VMName $payload.name -ExpectedState 'Running'
[PSCustomObject]@{} | ConvertTo-Json -Compress
`, map[string]string{"name": name, "checkpoint": checkpoint}, nil)
}

func (c *windowsClient) Suspend(ctx context.Context, name string) error {
	return c.invokeJSON(ctx, `
function Wait-ForSuspended {
    param([string]$VMName)
    $deadline = (Get-Date).AddMinutes(3)
    do {
        $state = [string](Get-VM -Name $VMName -ErrorAction Stop).State
        if ($state -in @('Saved', 'Paused', 'Off', 'FastSaved')) { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "VM '$VMName' did not reach a suspended state; current state: '$state'"
}

$vm = Get-VM -Name $payload.name -ErrorAction Stop
if ([string]$vm.State -eq 'Running') {
    Suspend-VM -Name $payload.name -ErrorAction Stop | Out-Null
    Wait-ForSuspended -VMName $payload.name
}
[PSCustomObject]@{} | ConvertTo-Json -Compress
`, map[string]string{"name": name}, nil)
}

func (c *windowsClient) ResumeOrStart(ctx context.Context, name string) error {
	return c.invokeJSON(ctx, `
function Wait-ForRunning {
    param([string]$VMName)
    $deadline = (Get-Date).AddMinutes(3)
    do {
        $state = [string](Get-VM -Name $VMName -ErrorAction Stop).State
        if ($state -eq 'Running') { return }
        Start-Sleep -Milliseconds 500
    } while ((Get-Date) -lt $deadline)
    throw "VM '$VMName' did not reach Running; current state: '$state'"
}

$vm = Get-VM -Name $payload.name -ErrorAction Stop
switch ([string]$vm.State) {
    'Running' { break }
    'Saved' { Resume-VM -Name $payload.name -ErrorAction Stop | Out-Null }
    'Paused' { Resume-VM -Name $payload.name -ErrorAction Stop | Out-Null }
    'Off' { Start-VM -Name $payload.name -ErrorAction Stop | Out-Null }
    default { throw "cannot start VM '$($payload.name)' from state '$($vm.State)'" }
}
Wait-ForRunning -VMName $payload.name
[PSCustomObject]@{} | ConvertTo-Json -Compress
`, map[string]string{"name": name}, nil)
}

func (c *windowsClient) SetNotes(ctx context.Context, name, notes string) error {
	return c.invokeJSON(ctx, `
Set-VM -Name $payload.name -Notes $payload.notes -ErrorAction Stop | Out-Null
[PSCustomObject]@{} | ConvertTo-Json -Compress
`, map[string]string{"name": name, "notes": notes}, nil)
}

func (c *windowsClient) BootstrapWindowsGuest(ctx context.Context, name, username, password, script string) error {
	return c.invokeJSON(ctx, `
$securePassword = ConvertTo-SecureString $payload.password -AsPlainText -Force
$credential = [System.Management.Automation.PSCredential]::new($payload.username, $securePassword)
Invoke-Command -VMName $payload.name -Credential $credential -ErrorAction Stop -ScriptBlock {
    param([string]$bootstrapScript)
    $ErrorActionPreference = 'Stop'
    # The runner bootstrap creates services. Running it directly in a
    # PowerShell Direct session can tear down that remoting session before
    # Invoke-Command returns, even though the guest is healthy. Stage it and
    # launch a separate process instead; GARM receives the final result via
    # the existing callback in the bootstrap script.
    $bootstrapDir = Join-Path $env:ProgramData 'garm-provider-hyperv'
    New-Item -ItemType Directory -Path $bootstrapDir -Force | Out-Null
    $bootstrapID = [guid]::NewGuid().ToString('N')
    $bootstrapPath = Join-Path $bootstrapDir ("bootstrap-$bootstrapID.ps1")
    $launcherPath = Join-Path $bootstrapDir ("launcher-$bootstrapID.ps1")
    # Preserve only process output for failed asynchronous bootstraps. The
    # payload and launcher themselves are removed by the launcher below.
    $stdoutPath = Join-Path $bootstrapDir ("bootstrap-$bootstrapID.stdout.log")
    $stderrPath = Join-Path $bootstrapDir ("bootstrap-$bootstrapID.stderr.log")
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    [System.IO.File]::WriteAllText($bootstrapPath, $bootstrapScript, $utf8NoBom)

    $escapedBootstrapPath = $bootstrapPath.Replace("'", "''")
    $launcher = @'
try {
    & '__GARM_BOOTSTRAP_PATH__'
} finally {
    Remove-Item -LiteralPath '__GARM_BOOTSTRAP_PATH__' -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue
}
'@
    $launcher = $launcher.Replace('__GARM_BOOTSTRAP_PATH__', $escapedBootstrapPath)
    [System.IO.File]::WriteAllText($launcherPath, $launcher, $utf8NoBom)
    Start-Process -FilePath 'powershell.exe' -ArgumentList @(
        '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $launcherPath
    ) -WindowStyle Hidden -RedirectStandardOutput $stdoutPath -RedirectStandardError $stderrPath -ErrorAction Stop | Out-Null
} -ArgumentList $payload.script | Out-Null
[PSCustomObject]@{} | ConvertTo-Json -Compress
`, map[string]string{
		"name":     name,
		"username": username,
		"password": password,
		"script":   sanitizeWindowsBootstrapScript(script),
	}, nil)
}

// The provider always invokes the staged script with -ExecutionPolicy Bypass.
// Do not let an optional template policy mutation fail bootstrapping when a
// stricter machine policy overrides it.
func sanitizeWindowsBootstrapScript(script string) string {
	return setExecutionPolicyCommand.ReplaceAllString(script, "# execution policy is supplied by the provider\n")
}

func (c *windowsClient) BootstrapLinuxGuest(ctx context.Context, address string, port int, username, privateKeyFile, knownHostsFile, script string) error {
	for {
		command := exec.CommandContext(ctx, c.sshPath,
			"-p", strconv.Itoa(port),
			"-i", privateKeyFile,
			"-o", "BatchMode=yes",
			"-o", "ConnectTimeout=30",
			"-o", "IdentitiesOnly=yes",
			"-o", "StrictHostKeyChecking=yes",
			"-o", "UserKnownHostsFile="+knownHostsFile,
			username+"@"+address,
			"sudo -n /bin/bash -s")
		command.Stdin = strings.NewReader(script)
		rawOutput, err := command.CombinedOutput()
		if err == nil {
			return nil
		}

		message := redactBootstrapOutput(strings.TrimSpace(string(rawOutput)))
		if len(message) > 4096 {
			message = message[:4096] + "…"
		}
		failure := fmt.Errorf("run Linux guest bootstrap over SSH: %w: %s", err, message)
		if ctx.Err() != nil || !isTransientSSHBootstrapFailure(message) {
			return failure
		}

		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for Linux guest SSH readiness: %w; last error: %v", ctx.Err(), failure)
		case <-timer.C:
		}
	}
}

func redactBootstrapOutput(output string) string {
	return bootstrapCredentialValue.ReplaceAllString(output, "$1[redacted]")
}

func isTransientSSHBootstrapFailure(message string) bool {
	message = strings.ToLower(message)
	for _, transient := range []string{
		"connection refused",
		"connection timed out",
		"no route to host",
		"network is unreachable",
		"connection reset by",
		"connection closed by",
	} {
		if strings.Contains(message, transient) {
			return true
		}
	}
	return false
}

func (c *windowsClient) invokeJSON(ctx context.Context, body string, input any, output any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshal PowerShell input: %w", err)
	}
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$payloadJson = [Console]::In.ReadToEnd()
$payload = $payloadJson | ConvertFrom-Json
%s
`, body)

	command := exec.CommandContext(ctx, c.powerShellPath,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePowerShell(script))
	command.Stdin = bytes.NewReader(data)
	rawOutput, err := command.Output()
	if err != nil {
		message := ""
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			message = strings.TrimSpace(string(exitError.Stderr))
		}
		if message == "" {
			message = strings.TrimSpace(string(rawOutput))
		}
		if len(message) > 4096 {
			message = message[:4096] + "…"
		}
		return fmt.Errorf("run Hyper-V PowerShell command: %w: %s", err, message)
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(rawOutput, output); err != nil {
		return fmt.Errorf("decode Hyper-V PowerShell response %q: %w", strings.TrimSpace(string(rawOutput)), err)
	}
	return nil
}

func encodePowerShell(script string) string {
	codeUnits := utf16.Encode([]rune(script))
	encoded := make([]byte, len(codeUnits)*2)
	for index, value := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[index*2:], value)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}
