# GARM Hyper-V fixed-guest provider

This is a Windows-hosted [GARM external provider](https://github.com/cloudbase/garm/blob/main/doc/external_provider.md) for exactly three pre-created Hyper-V guests: Ubuntu22Runner, Ubuntu24Runner, and Windows11Runner.

It intentionally does **not** create or delete virtual machines or VHDX files.
On idle reclamation GARM invokes DeleteInstance; this provider calls
Suspend-VM, clears its allocation marker in the VM Notes field, and leaves
the guest and its disks intact. The next allocation powers the VM off,
restores its named clean checkpoint, starts it, and runs GARM's standard
bootstrap script: PowerShell Direct for Windows and authenticated SSH for Linux.

## Preconditions

- GARM and this executable run on the physical Windows Hyper-V host.
- The account running GARM is elevated or is an effective member of
  Hyper-V Administrators; it must be able to run Get-VM, Set-VM,
  Restore-VMCheckpoint, Start-VM, Suspend-VM, and PowerShell Direct.
- Windows11Runner is an x64 Windows guest with a local administrator account;
  PowerShell Direct must be available.
- Ubuntu22Runner and Ubuntu24Runner are x64 Linux guests. Each needs OpenSSH
  server, a non-interactive sudo-capable runner user, and a dedicated key
  accepted by that user. The SSH host key must be pinned in a known_hosts file.
- If Hyper-V does not expose a Linux guest address through key-value exchange
  (as with this host's Default Switch), the provider resolves the guest IPv4
  address from the host's IPv4 neighbor table by matching the virtual NIC MAC.
- Each guest has a named, powered-off baseline checkpoint (for example
  garm-clean). The checkpoint must contain no registered runner or runner
  service. It is restored before every allocation.
- Guests can reach the GARM callback/metadata URL and GitHub or GHES over the
  network. The GARM service must be reachable from the guest, not just from
  the Hyper-V host.

Run the following readiness check on the elevated Hyper-V host before a
deployment or whenever host networking changes:

```powershell
Get-VM | Sort-Object Name |
  Select-Object Name,State,ProcessorCount,MemoryStartup,MemoryAssigned,Status

Get-VMNetworkAdapter -VMName * |
  Select-Object VMName,Name,SwitchName,Status,IPAddresses,DefaultIPGateway

Get-VMIntegrationService -VMName * |
  Select-Object VMName,Name,Enabled,PrimaryStatusDescription

Get-VMCheckpoint -VMName * |
  Select-Object VMName,Name,CheckpointType,CreationTime
```

## Installation

All commands below run in an elevated PowerShell session on the physical
Hyper-V host. `<GARM_ROOT>` denotes your GARM installation root — this guide
uses `C:\garm`, but any path works. Set it once per session; `build.ps1`,
`preflight.ps1`, and the verification steps below all honour it:

```powershell
$env:GARM_ROOT = "C:\garm"
```

### 1. Expected directory structure

The provider executable, its configuration, and its secrets live together
under `<GARM_ROOT>`. Create the tree with:

```powershell
New-Item -ItemType Directory -Force -Path "$env:GARM_ROOT\config"    | Out-Null
New-Item -ItemType Directory -Force -Path "$env:GARM_ROOT\providers" | Out-Null
New-Item -ItemType Directory -Force -Path "$env:GARM_ROOT\secrets"   | Out-Null
```

```text
<GARM_ROOT>\                               e.g. C:\garm
├─ config\
│  └─ garm-provider-hyperv.toml            provider configuration (from examples/)
├─ providers\
│  └─ garm-provider-hyperv.exe             built provider executable
└─ secrets\                                readable only by the GARM service account
   ├─ ubuntu22-runner-ed25519              Ubuntu 22.04 private key
   ├─ ubuntu22-known_hosts                 Ubuntu 22.04 pinned SSH host key
   ├─ ubuntu24-runner-ed25519              Ubuntu 24.04 private key
   ├─ ubuntu24-known_hosts                 Ubuntu 24.04 pinned SSH host key
   └─ windows11-runner-password.txt        Windows 11 administrator password
```

The repository itself does not need to live under `<GARM_ROOT>`; only the built
executable and its configuration do. Never commit any file from `secrets\`.

### 2. Get the provider executable

Choose one of the two options below. Both produce
`<GARM_ROOT>\providers\garm-provider-hyperv.exe`.

#### Option A — build from source

Install Go 1.26.2 or newer on the Windows host and clone this repository
anywhere on disk, then build:

```powershell
Set-Location <repo>\garm-provider-hyperv
.\build.ps1 -GarmRoot $env:GARM_ROOT -Version v0.1.0
```

`build.ps1` uses `-GarmRoot` (or `$env:GARM_ROOT`) to write the executable to
`<GARM_ROOT>\providers\garm-provider-hyperv.exe`. Without `-GarmRoot` the
default output is `bin\garm-provider-hyperv.exe` inside the repo;
`-OutputPath` overrides both.

#### Option B — download a pre-built release

No Go toolchain or source checkout is required. Download the executable from a
[GitHub release](https://github.com/HLND2T/garm-provider-hyperv/releases)
directly to `<GARM_ROOT>\providers\`:

```powershell
$version = "v0.1.0"   # pin a version, or use "latest"
Invoke-WebRequest -UseBasicParsing `
  -Uri "https://github.com/HLND2T/garm-provider-hyperv/releases/download/$version/garm-provider-hyperv.exe" `
  -OutFile "$env:GARM_ROOT\providers\garm-provider-hyperv.exe"
```

Each release also ships `garm-provider-hyperv-windows-amd64.zip`, which bundles
the executable together with the example configs and `preflight.ps1`. Download
and extract it when you are not cloning the repository, since steps 3 and 6
otherwise reference files inside the repo checkout:

```powershell
$version = "v0.1.0"
$zip = "$env:TEMP\garm-provider-hyperv-windows-amd64.zip"
Invoke-WebRequest -UseBasicParsing `
  -Uri "https://github.com/HLND2T/garm-provider-hyperv/releases/download/$version/garm-provider-hyperv-windows-amd64.zip" `
  -OutFile $zip
Expand-Archive -Path $zip -DestinationPath "$env:TEMP\garm-provider-hyperv" -Force
Copy-Item "$env:TEMP\garm-provider-hyperv\garm-provider-hyperv.exe" "$env:GARM_ROOT\providers\"
```

The extracted config and preflight files are then under
`$env:TEMP\garm-provider-hyperv\`.

### 3. Configure the provider

Copy the example configuration into the tree and edit it:

```powershell
Copy-Item .\examples\garm-provider-hyperv.toml `
  "$env:GARM_ROOT\config\garm-provider-hyperv.toml"
notepad "$env:GARM_ROOT\config\garm-provider-hyperv.toml"
```

In the copy, replace every `<GARM_ROOT>` placeholder with your actual root
(for example `C:\garm` — the template keeps `<GARM_ROOT>\secrets\...` intact
for every guest). The three VM entries are pre-filled with the confirmed guest
names; substitute:

- `ssh_username` / `windows_guest_username` with the real guest accounts;
- `checkpoint` if your baseline checkpoints are named differently;
- `os_version` / `os_arch` if a guest differs from the defaults;
- the credential paths only if you store secrets elsewhere.

`bootstrap_timeout_seconds` must not exceed the `runner_bootstrap_timeout` you
configure in GARM (step 7).

The provider passes the bootstrap script, GARM callback token, and guest
credential to the host-side process over standard input. They are never
encoded into a PowerShell or SSH command line.

### 4. Install guest credentials

For each Ubuntu guest, generate a dedicated SSH key pair and add the public
half to the runner user's `authorized_keys`. Copy the private key to
`secrets\ubuntu22-runner-ed25519` (and `ubuntu24-runner-ed25519`).

Each Ubuntu guest's `known_hosts` file pins its SSH host key. Capture it while
the guest is temporarily running — before you take the clean checkpoint:

```powershell
ssh-keyscan -t ed25519 <ubuntu22-ip> | Out-File -Encoding ascii `
  "$env:GARM_ROOT\secrets\ubuntu22-known_hosts"
```

Write the Windows11Runner administrator password (single line, no trailing
newline) to `secrets\windows11-runner-password.txt`.

Restrict the whole `secrets\` directory so only the account that runs GARM can
read it:

```powershell
icacls "$env:GARM_ROOT\secrets" /inheritance:r /grant:r "$env:USERNAME:(OI)(CI)R"
```

### 5. Register the provider with GARM

Add the `[provider.external]` block from
[examples/garm-provider.fragment.toml](examples/garm-provider.fragment.toml)
to the GARM `config.toml`, replacing `<GARM_ROOT>` with your root:

```toml
[[provider]]
name = "hyperv_fixed"
provider_type = "external"
description = "Three checkpointed Windows Hyper-V guests; idle guests are suspended"

[provider.external]
provider_executable = "<GARM_ROOT>\\providers\\garm-provider-hyperv.exe"
config_file = "<GARM_ROOT>\\config\\garm-provider-hyperv.toml"
exec_timeout_seconds = 900
```

The `interface_version` is intentionally omitted: GARM then selects v0.1.0,
avoiding the current v0.1.1 ListInstances adapter regression. Do not set it to
v0.1.1.

### 6. Run the readiness preflight

From the repository root,
[scripts/preflight.ps1](scripts/preflight.ps1) verifies that the session is
elevated, the provider executable exists, and each of the three guests is idle,
has key-value exchange enabled, and has its baseline checkpoint:

```powershell
.\scripts\preflight.ps1
```

It prints a JSON report with `Ready: true`/`false` and the list of issues, and
exits non-zero when anything is wrong. Pass `-CheckpointName` if your baseline
checkpoints are not named `garm-clean`, `-ProviderExecutable` (or
`$env:GARM_PROVIDER_EXECUTABLE`) to check a different executable, and
`-RequireGo` to also enforce that Go is on PATH (only relevant when building
from source).

### 7. Create runner pools

Create a Linux/amd64 pool for each Ubuntu guest and a Windows/amd64 pool for
Windows11Runner, all using `provider_name = "hyperv_fixed"`. Set each
`runner_bootstrap_timeout` to at least 900 seconds — greater than or equal to
`bootstrap_timeout_seconds` and `exec_timeout_seconds`.

### 8. Verify the installation

Smoke-test the provider binary directly; it must return the provider version
without touching a guest:

```powershell
$env:GARM_COMMAND = "GetVersion"
$env:GARM_CONTROLLER_ID = "verification-controller"
$env:GARM_PROVIDER_CONFIG_FILE = "$env:GARM_ROOT\config\garm-provider-hyperv.toml"
& "$env:GARM_ROOT\providers\garm-provider-hyperv.exe"

Get-VM -Name '<one configured VM>' | Select-Object Name,State,Notes
```

Then create one test pool runner and confirm the provider returns the
configured VM name as ProviderID, the guest reports runner bootstrap status to
GARM, and idle cleanup leaves the VM in Saved or Paused state after
Suspend-VM. Do not test RemoveAllInstances until the three entries and
checkpoints are confirmed: it suspends every guest currently allocated to this
controller.

## Lifecycle

```text
CreateInstance
  -> select one unallocated guest
  -> Stop-VM / Restore-VMCheckpoint / Start-VM
  -> write GARM allocation metadata into VM Notes
  -> Windows: PowerShell Direct runs GARM's bootstrap script
  -> Linux: SSH runs GARM's bootstrap script as a sudo-capable runner user

DeleteInstance (including GARM idle reclamation)
  -> Suspend-VM
  -> clear only the GARM allocation metadata

next CreateInstance
  -> restore clean checkpoint before the guest is reused
```

Stop also calls Suspend-VM, but standard GARM idle reclaim currently uses
DeleteInstance, so both paths intentionally suspend instead of delete.

Each VM's existing human-readable Notes text is preserved. The provider adds a
short JSON allocation marker while GARM owns the guest, then removes only that
marker after a successful Suspend-VM.
