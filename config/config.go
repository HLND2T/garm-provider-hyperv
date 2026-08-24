// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const expectedGuestCount = 3

// Config describes a fixed-capacity set of Hyper-V guests. A guest is never
// deleted by this provider. It is suspended on idle reclamation and restored
// from its named baseline checkpoint before its next allocation.
type Config struct {
	PowerShellPath          string `toml:"powershell_path"`
	SSHPath                 string `toml:"ssh_path"`
	LockTimeoutSeconds      int    `toml:"lock_timeout_seconds"`
	BootstrapTimeoutSeconds int    `toml:"bootstrap_timeout_seconds"`
	VMs                     []VM   `toml:"vm"`
}

// VM is one pre-provisioned guest available to GARM.
type VM struct {
	Name       string `toml:"name"`
	Checkpoint string `toml:"checkpoint"`
	// SkipCheckpointRestore keeps the guest's current disk state across
	// allocations instead of restoring the baseline checkpoint. Use it to
	// preserve data (for example build artifacts) on the guest between runs.
	SkipCheckpointRestore bool `toml:"skip_checkpoint_restore"`
	// Disabled keeps a guest in the fixed inventory without making it eligible
	// for new allocations. It is intended for temporary guest maintenance.
	Disabled                 bool   `toml:"disabled"`
	OSType                   string `toml:"os_type"`
	OSName                   string `toml:"os_name"`
	OSVersion                string `toml:"os_version"`
	OSArch                   string `toml:"os_arch"`
	WindowsGuestUsername     string `toml:"windows_guest_username"`
	WindowsGuestPasswordFile string `toml:"windows_guest_password_file"`
	SSHUsername              string `toml:"ssh_username"`
	SSHPrivateKeyFile        string `toml:"ssh_private_key_file"`
	SSHKnownHostsFile        string `toml:"ssh_known_hosts_file"`
	SSHPort                  int    `toml:"ssh_port"`
}

func NewConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("decode provider config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate provider config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if len(c.VMs) != expectedGuestCount {
		return fmt.Errorf("this fixed-capacity provider requires exactly %d vm entries, got %d", expectedGuestCount, len(c.VMs))
	}

	names := make(map[string]struct{}, len(c.VMs))
	for _, vm := range c.VMs {
		if strings.TrimSpace(vm.Name) == "" {
			return fmt.Errorf("vm name is required")
		}
		if _, ok := names[vm.Name]; ok {
			return fmt.Errorf("duplicate vm name %q", vm.Name)
		}
		names[vm.Name] = struct{}{}
		if strings.TrimSpace(vm.Checkpoint) == "" {
			return fmt.Errorf("vm %q requires a baseline checkpoint for safe reuse", vm.Name)
		}
		switch vm.OSType {
		case "windows":
			if vm.WindowsGuestUsername == "" {
				return fmt.Errorf("windows vm %q requires windows_guest_username", vm.Name)
			}
			if err := validateFile("windows_guest_password_file", vm.WindowsGuestPasswordFile); err != nil {
				return fmt.Errorf("windows vm %q: %w", vm.Name, err)
			}
		case "linux":
			if vm.SSHUsername == "" {
				return fmt.Errorf("linux vm %q requires ssh_username", vm.Name)
			}
			for field, path := range map[string]string{
				"ssh_private_key_file": vm.SSHPrivateKeyFile,
				"ssh_known_hosts_file": vm.SSHKnownHostsFile,
			} {
				if err := validateFile(field, path); err != nil {
					return fmt.Errorf("linux vm %q: %w", vm.Name, err)
				}
			}
		default:
			return fmt.Errorf("vm %q has unsupported os_type %q; supported values are linux and windows", vm.Name, vm.OSType)
		}
		if vm.OSArch != "" && vm.OSArch != "amd64" {
			return fmt.Errorf("vm %q has unsupported os_arch %q; only amd64 guests are supported", vm.Name, vm.OSArch)
		}
	}
	return nil
}

func (c *Config) WindowsCredentials(vm VM) (string, string, error) {
	if vm.WindowsGuestUsername == "" {
		return "", "", fmt.Errorf("windows vm %q requires windows_guest_username", vm.Name)
	}
	password, err := readWindowsPassword(vm.WindowsGuestPasswordFile)
	if err != nil {
		return "", "", err
	}
	return vm.WindowsGuestUsername, password, nil
}

func readWindowsPassword(passwordFile string) (string, error) {
	data, err := os.ReadFile(passwordFile)
	if err != nil {
		return "", fmt.Errorf("read windows_guest_password_file: %w", err)
	}
	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", fmt.Errorf("windows_guest_password_file is empty")
	}
	return password, nil
}

func validateFile(field, path string) error {
	if path == "" {
		return fmt.Errorf("%s is required", field)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("access %s: %w", field, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s must be a file", field)
	}
	return nil
}

func (c *Config) PowerShell() string {
	if c.PowerShellPath != "" {
		return c.PowerShellPath
	}
	return "powershell.exe"
}

func (c *Config) SSH() string {
	if c.SSHPath != "" {
		return c.SSHPath
	}
	return "ssh.exe"
}

func (v VM) SSHPortOrDefault() int {
	if v.SSHPort > 0 {
		return v.SSHPort
	}
	return 22
}

func (c *Config) LockTimeout() int {
	if c.LockTimeoutSeconds > 0 {
		return c.LockTimeoutSeconds
	}
	return 60
}

func (c *Config) BootstrapTimeout() time.Duration {
	if c.BootstrapTimeoutSeconds > 0 {
		return time.Duration(c.BootstrapTimeoutSeconds) * time.Second
	}
	return 15 * time.Minute
}
