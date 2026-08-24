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

## Configure

1. Copy [examples/garm-provider-hyperv.toml](examples/garm-provider-hyperv.toml)
   to C:\garm\config\garm-provider-hyperv.toml; the three confirmed VM names
   are already filled in. Substitute credentials and checkpoint names.
2. Write the Windows11Runner administrator password to the file referenced by
   that guest's `windows_guest_password_file` setting and restrict its NTFS ACL
   to the GARM service account. Set `windows_guest_username` beside it in the
   same `[[vm]]` entry. Add each Ubuntu private key and matching pinned
   known_hosts file at the paths in the template. These files must not be
   checked in.
3. Copy [examples/garm-provider.fragment.toml](examples/garm-provider.fragment.toml)
   into the GARM config.toml and adjust paths only if GARM lives elsewhere.
4. Create Linux/amd64 pools for the two Ubuntu Guests and a Windows/amd64 pool
   for Windows11Runner, all using provider_name hyperv_fixed. Set each
   runner_bootstrap_timeout to at least 900 seconds. Keep it greater than or
   equal to bootstrap_timeout_seconds and exec_timeout_seconds.

Each VM's existing human-readable Notes text is preserved. The provider adds a
short JSON allocation marker while GARM owns the guest, then removes only that
marker after a successful Suspend-VM.

The provider passes the bootstrap script, GARM callback token, and guest
credential to the host-side process over standard input. They are not encoded
into a PowerShell or SSH command line.

## Build

Install Go 1.26.2 or newer on the Windows host, then run in an elevated
PowerShell:

```powershell
Set-Location C:\garm\garm-provider-hyperv
.\build.ps1 -Version v0.1.0
```

The executable is written to C:\garm\providers\garm-provider-hyperv.exe.
The project uses GARM external-provider interface v0.1.0; do not set
interface_version to v0.1.1 in the GARM provider configuration.

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

## Targeted verification

After building and filling the real configuration, run the following as the
same account that runs GARM:

```powershell
$env:GARM_COMMAND = "GetVersion"
$env:GARM_CONTROLLER_ID = "verification-controller"
$env:GARM_PROVIDER_CONFIG_FILE = "C:\garm\config\garm-provider-hyperv.toml"
& C:\garm\providers\garm-provider-hyperv.exe

Get-VM -Name '<one configured VM>' | Select-Object Name,State,Notes
```

Then create one test pool runner. Verify the provider returns the configured
VM name as ProviderID, the guest reports runner bootstrap status to GARM, and
idle cleanup leaves the VM in Saved or Paused state after Suspend-VM. Do not
test RemoveAllInstances until the three entries and checkpoints are confirmed:
it suspends every guest currently allocated to this controller.
