// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestConfigValidateRequiresExactlyThreeGuests(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "guest-password.txt")
	if err := os.WriteFile(passwordFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		VMs: []VM{
			{Name: "runner-01", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
			{Name: "runner-02", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "exactly 3") {
		t.Fatalf("Validate() error = %v, want exact guest-count error", err)
	}
}

func TestConfigPasswordPreservesSpaces(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "guest-password.txt")
	if err := os.WriteFile(passwordFile, []byte(" secret value \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{VMs: []VM{{Name: "runner-01", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile}}}

	_, password, err := cfg.WindowsCredentials(cfg.VMs[0])
	if err != nil {
		t.Fatal(err)
	}
	if password != " secret value " {
		t.Fatalf("WindowsPassword() = %q, want surrounding spaces retained", password)
	}
}

func TestConfigValidateRequiresWindowsCredentialsOnVM(t *testing.T) {
	passwordFile := filepath.Join(t.TempDir(), "guest-password.txt")
	if err := os.WriteFile(passwordFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{VMs: []VM{
		{Name: "runner-01", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
		{Name: "runner-02", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator"},
		{Name: "runner-03", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
	}}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "runner-02") || !strings.Contains(err.Error(), "windows_guest_password_file") {
		t.Fatalf("Validate() error = %v, want runner-02 password-file error", err)
	}
}

func TestExampleFilesUseValidTOML(t *testing.T) {
	var providerConfig Config
	if _, err := toml.DecodeFile(filepath.Join("..", "examples", "garm-provider-hyperv.toml"), &providerConfig); err != nil {
		t.Fatalf("parse provider example: %v", err)
	}
	if len(providerConfig.VMs) != expectedGuestCount {
		t.Fatalf("provider example guest count = %d, want %d", len(providerConfig.VMs), expectedGuestCount)
	}

	var garmFragment map[string]any
	if _, err := toml.DecodeFile(filepath.Join("..", "examples", "garm-provider.fragment.toml"), &garmFragment); err != nil {
		t.Fatalf("parse GARM provider fragment: %v", err)
	}
	if len(garmFragment) == 0 {
		t.Fatal("GARM provider fragment is empty")
	}
}
