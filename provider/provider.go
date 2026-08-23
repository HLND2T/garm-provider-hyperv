// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package provider

import (
	"context"
	stdErrors "errors"
	"fmt"
	"net"
	"strings"
	"time"

	cloudconfig "github.com/cloudbase/garm-provider-common/cloudconfig"
	runnerErrors "github.com/cloudbase/garm-provider-common/errors"
	execution "github.com/cloudbase/garm-provider-common/execution/v0.1.0"
	commonParams "github.com/cloudbase/garm-provider-common/params"
	"github.com/cloudbase/garm-provider-common/util"

	"github.com/cloudbase/garm-provider-hyperv/config"
)

var _ execution.ExternalProvider = (*HyperV)(nil)

// Version is set at build time with -ldflags "-X .../provider.Version=vX.Y.Z".
var Version = "v0.0.0-unknown"

type toolFetchFunc func(commonParams.OSType, commonParams.OSArch, []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error)
type cloudConfigFunc func(commonParams.BootstrapInstance, commonParams.RunnerApplicationDownload, string) (string, error)
type runnerScriptFunc func(commonParams.BootstrapInstance, commonParams.RunnerApplicationDownload, string, config.VM) ([]byte, error)

// HyperV manages a deliberately fixed pool of pre-created Linux and Windows guests.
// The immutable baseline checkpoint makes suspended guests safe to reuse.
type HyperV struct {
	cfg              *config.Config
	controllerID     string
	client           Client
	toolFetch        toolFetchFunc
	windowsBootstrap cloudConfigFunc
	linuxBootstrap   runnerScriptFunc
}

func NewHyperVProvider(configFile, controllerID string) (execution.ExternalProvider, error) {
	if controllerID == "" {
		return nil, fmt.Errorf("controller ID is required")
	}
	cfg, err := config.NewConfig(configFile)
	if err != nil {
		return nil, err
	}
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return newHyperVProvider(cfg, controllerID, client), nil
}

func newHyperVProvider(cfg *config.Config, controllerID string, client Client) *HyperV {
	return &HyperV{
		cfg:              cfg,
		controllerID:     controllerID,
		client:           client,
		toolFetch:        util.GetTools,
		windowsBootstrap: cloudconfig.GetCloudConfig,
		linuxBootstrap:   renderLinuxRunnerScript,
	}
}

func (p *HyperV) CreateInstance(ctx context.Context, bootstrap commonParams.BootstrapInstance) (commonParams.ProviderInstance, error) {
	if bootstrap.Name == "" {
		return commonParams.ProviderInstance{}, runnerErrors.NewBadRequestError("missing runner name")
	}
	if bootstrap.PoolID == "" {
		return commonParams.ProviderInstance{}, runnerErrors.NewBadRequestError("missing pool ID")
	}
	if bootstrap.OSType != commonParams.Windows && bootstrap.OSType != commonParams.Linux {
		return commonParams.ProviderInstance{}, runnerErrors.NewBadRequestError("Hyper-V fixed guests support Linux and Windows runners only")
	}
	if bootstrap.OSArch != "" && bootstrap.OSArch != commonParams.Amd64 {
		return commonParams.ProviderInstance{}, runnerErrors.NewBadRequestError("Hyper-V fixed guests support amd64 runners only")
	}

	var result commonParams.ProviderInstance
	err := p.withLock(ctx, func() error {
		vms, err := p.listVMs(ctx)
		if err != nil {
			return err
		}
		vm, vmCfg, found, err := p.findFreeVM(vms, bootstrap.OSType)
		if err != nil {
			return err
		}
		if !found {
			return runnerErrors.NewNoCapacityError("all configured Hyper-V guests are allocated")
		}

		// Restoring the baseline precedes metadata assignment: VM checkpoints include
		// settings such as Notes and would otherwise erase the allocation marker.
		if err := p.client.PrepareForAllocation(ctx, vm.Name, vmCfg.Checkpoint); err != nil {
			return fmt.Errorf("prepare guest %q from checkpoint %q: %w", vm.Name, vmCfg.Checkpoint, err)
		}
		prepared, err := p.vmByName(ctx, vm.Name)
		if err != nil {
			return err
		}

		allocatedNotes, err := notesWithAllocation(prepared.Notes, &allocation{
			ControllerID: p.controllerID,
			PoolID:       bootstrap.PoolID,
			RunnerName:   bootstrap.Name,
		})
		if err != nil {
			return err
		}
		if err := p.client.SetNotes(ctx, vm.Name, allocatedNotes); err != nil {
			return fmt.Errorf("mark guest %q allocated: %w", vm.Name, err)
		}

		tool, err := p.toolFetch(bootstrap.OSType, bootstrap.OSArch, bootstrap.Tools)
		if err != nil {
			return fmt.Errorf("select runner tool: %w", err)
		}
		bootstrapCtx, cancel := context.WithTimeout(ctx, p.cfg.BootstrapTimeout())
		defer cancel()
		switch bootstrap.OSType {
		case commonParams.Windows:
			script, err := p.windowsBootstrap(bootstrap, tool, bootstrap.Name)
			if err != nil {
				return fmt.Errorf("render Windows runner bootstrap script: %w", err)
			}
			password, err := p.cfg.WindowsPassword()
			if err != nil {
				return err
			}
			if err := p.client.BootstrapWindowsGuest(bootstrapCtx, vm.Name, p.cfg.WindowsGuestUsername, password, script); err != nil {
				return fmt.Errorf("bootstrap Windows guest %q: %w", vm.Name, err)
			}
		case commonParams.Linux:
			script, err := p.linuxBootstrap(bootstrap, tool, bootstrap.Name, vmCfg)
			if err != nil {
				return fmt.Errorf("render Linux runner bootstrap script: %w", err)
			}
			address, err := firstGuestIPv4(prepared.IPAddresses)
			if err != nil {
				return fmt.Errorf("resolve Linux guest %q address: %w", vm.Name, err)
			}
			if err := p.client.BootstrapLinuxGuest(bootstrapCtx, address, vmCfg.SSHPortOrDefault(), vmCfg.SSHUsername, vmCfg.SSHPrivateKeyFile, vmCfg.SSHKnownHostsFile, string(script)); err != nil {
				return fmt.Errorf("bootstrap Linux guest %q: %w", vm.Name, err)
			}
		}

		refreshed, err := p.vmByName(ctx, vm.Name)
		if err != nil {
			return err
		}
		result = p.instanceFromVM(refreshed, vmCfg, allocation{
			ControllerID: p.controllerID,
			PoolID:       bootstrap.PoolID,
			RunnerName:   bootstrap.Name,
		})
		return nil
	})
	if err != nil {
		return commonParams.ProviderInstance{}, err
	}
	return result, nil
}

// renderLinuxRunnerScript keeps GARM's standard install template while binding
// it to the configured guest account. The shared helper defaults to a user
// named "runner", which does not exist on this fixed Hyper-V inventory.
func renderLinuxRunnerScript(bootstrap commonParams.BootstrapInstance, tool commonParams.RunnerApplicationDownload, runnerName string, vm config.VM) ([]byte, error) {
	if strings.TrimSpace(vm.SSHUsername) == "" {
		return nil, fmt.Errorf("linux guest %q has no SSH username", vm.Name)
	}
	if tool.GetFilename() == "" {
		return nil, fmt.Errorf("missing tools filename")
	}
	if tool.GetDownloadURL() == "" {
		return nil, fmt.Errorf("missing tools download URL")
	}

	extraSpecs, err := cloudconfig.GetSpecs(bootstrap)
	if err != nil {
		return nil, fmt.Errorf("get Linux runner install specs: %w", err)
	}

	installParams := cloudconfig.InstallRunnerParams{
		FileName:          tool.GetFilename(),
		DownloadURL:       tool.GetDownloadURL(),
		TempDownloadToken: tool.GetTempDownloadToken(),
		MetadataURL:       bootstrap.MetadataURL,
		RunnerUsername:    vm.SSHUsername,
		RunnerGroup:       vm.SSHUsername,
		RepoURL:           bootstrap.RepoURL,
		RunnerName:        runnerName,
		RunnerLabels:      strings.Join(bootstrap.Labels, ","),
		CallbackURL:       bootstrap.CallbackURL,
		CallbackToken:     bootstrap.InstanceToken,
		GitHubRunnerGroup: bootstrap.GitHubRunnerGroup,
		ExtraContext:      extraSpecs.ExtraContext,
		EnableBootDebug:   bootstrap.UserDataOptions.EnableBootDebug,
		UseJITConfig:      bootstrap.JitConfigEnabled,
	}
	if len(bootstrap.CACertBundle) > 0 {
		installParams.CABundle = string(bootstrap.CACertBundle)
	}

	script, err := cloudconfig.InstallRunnerScript(installParams, bootstrap.OSType, string(extraSpecs.RunnerInstallTemplate))
	if err != nil {
		return nil, fmt.Errorf("render Linux runner install script: %w", err)
	}
	// The upstream default template has one legacy literal in its optional
	// SELinux branch. Keep that path tied to the configured guest account too.
	guestHome := "/home/" + vm.SSHUsername + "/"
	return []byte(strings.ReplaceAll(string(script), "/home/runner/", guestHome)), nil
}

func (p *HyperV) DeleteInstance(ctx context.Context, instance string) error {
	return p.withLock(ctx, func() error {
		vm, _, metadata, err := p.findAllocatedVM(ctx, instance)
		if err != nil {
			if isNotFound(err) {
				return nil
			}
			return err
		}
		if err := p.client.Suspend(ctx, vm.Name); err != nil {
			return fmt.Errorf("suspend guest %q: %w", vm.Name, err)
		}
		notes, err := notesWithAllocation(vm.Notes, nil)
		if err != nil {
			return err
		}
		if err := p.client.SetNotes(ctx, vm.Name, notes); err != nil {
			return fmt.Errorf("clear allocation marker for guest %q (%s): %w", vm.Name, metadata.RunnerName, err)
		}
		return nil
	})
}

func (p *HyperV) GetInstance(ctx context.Context, instance string) (commonParams.ProviderInstance, error) {
	vm, vmCfg, metadata, err := p.findAllocatedVM(ctx, instance)
	if err != nil {
		return commonParams.ProviderInstance{}, err
	}
	return p.instanceFromVM(vm, vmCfg, metadata), nil
}

func (p *HyperV) ListInstances(ctx context.Context, poolID string) ([]commonParams.ProviderInstance, error) {
	vms, err := p.listVMs(ctx)
	if err != nil {
		return nil, err
	}
	instances := make([]commonParams.ProviderInstance, 0, len(vms))
	for _, vm := range vms {
		metadata, allocated, err := allocationFromNotes(vm.Notes)
		if err != nil {
			return nil, fmt.Errorf("read metadata on guest %q: %w", vm.Name, err)
		}
		if !allocated || metadata.ControllerID != p.controllerID || (poolID != "" && metadata.PoolID != poolID) {
			continue
		}
		vmCfg, ok := p.configVM(vm.Name)
		if !ok {
			return nil, fmt.Errorf("guest %q is not configured", vm.Name)
		}
		instances = append(instances, p.instanceFromVM(vm, vmCfg, metadata))
	}
	return instances, nil
}

func (p *HyperV) RemoveAllInstances(ctx context.Context) error {
	return p.withLock(ctx, func() error {
		instances, err := p.ListInstances(ctx, "")
		if err != nil {
			return err
		}
		for _, instance := range instances {
			vm, _, _, err := p.findAllocatedVM(ctx, instance.ProviderID)
			if err != nil {
				return err
			}
			if err := p.client.Suspend(ctx, vm.Name); err != nil {
				return fmt.Errorf("suspend guest %q: %w", vm.Name, err)
			}
			notes, err := notesWithAllocation(vm.Notes, nil)
			if err != nil {
				return err
			}
			if err := p.client.SetNotes(ctx, vm.Name, notes); err != nil {
				return fmt.Errorf("clear allocation marker for guest %q: %w", vm.Name, err)
			}
		}
		return nil
	})
}

// Stop deliberately uses Suspend-VM. GARM currently reclaims idle instances
// through DeleteInstance, which also calls Suspend-VM and clears ownership.
func (p *HyperV) Stop(ctx context.Context, instance string, _ bool) error {
	return p.withLock(ctx, func() error {
		vm, _, _, err := p.findAllocatedVM(ctx, instance)
		if err != nil {
			return err
		}
		if err := p.client.Suspend(ctx, vm.Name); err != nil {
			return fmt.Errorf("suspend guest %q: %w", vm.Name, err)
		}
		return nil
	})
}

func (p *HyperV) Start(ctx context.Context, instance string) error {
	return p.withLock(ctx, func() error {
		vm, _, _, err := p.findAllocatedVM(ctx, instance)
		if err != nil {
			return err
		}
		if err := p.client.ResumeOrStart(ctx, vm.Name); err != nil {
			return fmt.Errorf("start guest %q: %w", vm.Name, err)
		}
		return nil
	})
}

func (p *HyperV) GetVersion(context.Context) string {
	return Version
}

func (p *HyperV) withLock(ctx context.Context, work func() error) error {
	return withProviderLock(ctx, p.controllerID, time.Duration(p.cfg.LockTimeout())*time.Second, work)
}

func (p *HyperV) listVMs(ctx context.Context) ([]vmInfo, error) {
	names := make([]string, 0, len(p.cfg.VMs))
	for _, vm := range p.cfg.VMs {
		names = append(names, vm.Name)
	}
	vms, err := p.client.ListVMs(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("list configured Hyper-V guests: %w", err)
	}
	return vms, nil
}

func (p *HyperV) vmByName(ctx context.Context, name string) (vmInfo, error) {
	vms, err := p.listVMs(ctx)
	if err != nil {
		return vmInfo{}, err
	}
	for _, vm := range vms {
		if vm.Name == name {
			return vm, nil
		}
	}
	return vmInfo{}, fmt.Errorf("%w: guest %q", runnerErrors.ErrNotFound, name)
}

func (p *HyperV) findFreeVM(vms []vmInfo, osType commonParams.OSType) (vmInfo, config.VM, bool, error) {
	for _, vm := range vms {
		_, allocated, err := allocationFromNotes(vm.Notes)
		if err != nil {
			return vmInfo{}, config.VM{}, false, fmt.Errorf("read metadata on guest %q: %w", vm.Name, err)
		}
		if allocated {
			continue
		}
		vmCfg, ok := p.configVM(vm.Name)
		if !ok {
			return vmInfo{}, config.VM{}, false, fmt.Errorf("guest %q is not configured", vm.Name)
		}
		if vmCfg.Disabled {
			continue
		}
		if vmCfg.OSType != string(osType) {
			continue
		}
		return vm, vmCfg, true, nil
	}
	return vmInfo{}, config.VM{}, false, nil
}

func (p *HyperV) findAllocatedVM(ctx context.Context, instance string) (vmInfo, config.VM, allocation, error) {
	vms, err := p.listVMs(ctx)
	if err != nil {
		return vmInfo{}, config.VM{}, allocation{}, err
	}
	for _, vm := range vms {
		metadata, allocated, err := allocationFromNotes(vm.Notes)
		if err != nil {
			return vmInfo{}, config.VM{}, allocation{}, fmt.Errorf("read metadata on guest %q: %w", vm.Name, err)
		}
		if !allocated || metadata.ControllerID != p.controllerID {
			continue
		}
		if vm.Name != instance && metadata.RunnerName != instance {
			continue
		}
		vmCfg, ok := p.configVM(vm.Name)
		if !ok {
			return vmInfo{}, config.VM{}, allocation{}, fmt.Errorf("guest %q is not configured", vm.Name)
		}
		return vm, vmCfg, metadata, nil
	}
	return vmInfo{}, config.VM{}, allocation{}, fmt.Errorf("%w: instance %q", runnerErrors.ErrNotFound, instance)
}

func (p *HyperV) configVM(name string) (config.VM, bool) {
	for _, vm := range p.cfg.VMs {
		if vm.Name == name {
			return vm, true
		}
	}
	return config.VM{}, false
}

func (p *HyperV) instanceFromVM(vm vmInfo, vmCfg config.VM, metadata allocation) commonParams.ProviderInstance {
	addresses := make([]commonParams.Address, 0, len(vm.IPAddresses))
	for _, value := range vm.IPAddresses {
		ip := net.ParseIP(value)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		addressType := commonParams.PublicAddress
		if ip.IsPrivate() {
			addressType = commonParams.PrivateAddress
		}
		addresses = append(addresses, commonParams.Address{Address: value, Type: addressType})
	}
	osName := vmCfg.OSName
	if osName == "" {
		osName = vmCfg.OSType
	}
	return commonParams.ProviderInstance{
		ProviderID: vm.Name,
		Name:       metadata.RunnerName,
		OSType:     commonParams.OSType(vmCfg.OSType),
		OSName:     osName,
		OSVersion:  vmCfg.OSVersion,
		OSArch:     commonParams.Amd64,
		Addresses:  addresses,
		Status:     hyperVStatus(vm.State),
	}
}

func firstGuestIPv4(addresses []string) (string, error) {
	for _, value := range addresses {
		ip := net.ParseIP(value)
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
			return value, nil
		}
	}
	return "", fmt.Errorf("no routable IPv4 address was reported by Hyper-V key-value exchange")
}

func hyperVStatus(state string) commonParams.InstanceStatus {
	switch strings.ToLower(state) {
	case "running":
		return commonParams.InstanceRunning
	case "off", "saved", "paused", "fastsaved":
		return commonParams.InstanceStopped
	case "critical":
		return commonParams.InstanceError
	default:
		return commonParams.InstanceStatusUnknown
	}
}

func isNotFound(err error) bool {
	return stdErrors.Is(err, runnerErrors.ErrNotFound)
}
