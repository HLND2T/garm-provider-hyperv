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

func TestConfigValidateRequiresAtLeastOneGuest(t *testing.T) {
	cfg := Config{}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("Validate() error = %v, want guest-count error", err)
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

func TestConfigValidateSSHHostKeyAlias(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "id_rsa")
	knownHostsFile := filepath.Join(t.TempDir(), "known_hosts")
	for _, path := range []string{keyFile, knownHostsFile} {
		if err := os.WriteFile(path, []byte("placeholder\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name    string
		alias   string
		wantErr bool
	}{
		{name: "empty preserves IP lookup", alias: ""},
		{name: "stable name", alias: "Ubuntu24Runner"},
		{name: "surrounding whitespace is trimmed", alias: " Ubuntu24Runner "},
		{name: "interior whitespace", alias: "Ubuntu 24Runner", wantErr: true},
		{name: "comma", alias: "Ubuntu24Runner,other", wantErr: true},
		{name: "colon", alias: "Ubuntu24Runner:22", wantErr: true},
		{name: "wildcard", alias: "Ubuntu24*", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Config{VMs: []VM{{
				Name:              "runner-01",
				Checkpoint:        "garm-clean",
				OSType:            "linux",
				SSHUsername:       "runner",
				SSHPrivateKeyFile: keyFile,
				SSHKnownHostsFile: knownHostsFile,
				SSHHostKeyAlias:   test.alias,
			}}}

			err := cfg.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantErr && !strings.Contains(err.Error(), "ssh_host_key_alias") {
				t.Fatalf("Validate() error = %v, want ssh_host_key_alias error", err)
			}
		})
	}
}

func TestExampleFilesUseValidTOML(t *testing.T) {
	var providerConfig Config
	if _, err := toml.DecodeFile(filepath.Join("..", "examples", "garm-provider-hyperv.toml"), &providerConfig); err != nil {
		t.Fatalf("parse provider example: %v", err)
	}
	if len(providerConfig.VMs) == 0 {
		t.Fatalf("provider example guest count = %d, want at least one", len(providerConfig.VMs))
	}

	var garmFragment map[string]any
	if _, err := toml.DecodeFile(filepath.Join("..", "examples", "garm-provider.fragment.toml"), &garmFragment); err != nil {
		t.Fatalf("parse GARM provider fragment: %v", err)
	}
	if len(garmFragment) == 0 {
		t.Fatal("GARM provider fragment is empty")
	}
}
