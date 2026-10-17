// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package provider

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	commonParams "github.com/cloudbase/garm-provider-common/params"

	"github.com/cloudbase/garm-provider-hyperv/config"
)

func TestCreateThenDeleteSuspendsAndReleasesGuest(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Off", Notes: "reserved for CI"},
		{Name: "runner-02", State: "Off"},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)

	instance, err := provider.CreateInstance(context.Background(), commonParams.BootstrapInstance{
		Name:   "garm-runner-123",
		PoolID: "pool-a",
		OSType: commonParams.Windows,
		OSArch: commonParams.Amd64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ProviderID != "runner-01" || instance.Name != "garm-runner-123" {
		t.Fatalf("unexpected instance: %#v", instance)
	}
	if client.prepared != "runner-01:garm-clean" || client.bootstrapped != "runner-01" {
		t.Fatalf("unexpected lifecycle calls: prepared=%q bootstrapped=%q", client.prepared, client.bootstrapped)
	}
	metadata, allocated, err := allocationFromNotes(client.vms[0].Notes)
	if err != nil || !allocated || metadata.RunnerName != "garm-runner-123" {
		t.Fatalf("allocation metadata = %#v, allocated=%t, err=%v", metadata, allocated, err)
	}

	if err := provider.DeleteInstanceWithName(context.Background(), instance.ProviderID, instance.Name); err != nil {
		t.Fatal(err)
	}
	if client.suspended != "runner-01" {
		t.Fatalf("Suspend() VM = %q, want runner-01", client.suspended)
	}
	if client.vms[0].Notes != "reserved for CI" {
		t.Fatalf("Notes after delete = %q, want original operator note", client.vms[0].Notes)
	}
}

func TestStaleNamedDeleteDoesNotSuspendReallocatedGuest(t *testing.T) {
	notes := mustNotes(t, "garm-runner-new")
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Running", Notes: notes},
		{Name: "runner-02", State: "Off"},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)

	err := provider.DeleteInstanceWithName(context.Background(), "runner-01", "garm-runner-old")
	if err != nil {
		t.Fatal(err)
	}
	if client.suspended != "" {
		t.Fatalf("Suspend() VM = %q, want no suspension", client.suspended)
	}
	if client.vms[0].Notes != notes {
		t.Fatalf("Notes after stale delete = %q, want %q", client.vms[0].Notes, notes)
	}
}

func TestDeleteRetryDoesNotSuspendGuestReallocatedToNewRunner(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Off"},
		{Name: "runner-02", State: "Off"},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)
	ctx := context.Background()

	first, err := provider.CreateInstance(ctx, commonParams.BootstrapInstance{
		Name:   "garm-runner-old",
		PoolID: "pool-a",
		OSType: commonParams.Windows,
		OSArch: commonParams.Amd64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := provider.DeleteInstanceWithName(ctx, first.ProviderID, first.Name); err != nil {
		t.Fatal(err)
	}

	second, err := provider.CreateInstance(ctx, commonParams.BootstrapInstance{
		Name:   "garm-runner-new",
		PoolID: "pool-a",
		OSType: commonParams.Windows,
		OSArch: commonParams.Amd64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ProviderID != first.ProviderID {
		t.Fatalf("reallocated ProviderID = %q, want %q", second.ProviderID, first.ProviderID)
	}
	client.suspended = ""

	if err := provider.DeleteInstanceWithName(ctx, first.ProviderID, first.Name); err != nil {
		t.Fatal(err)
	}
	if client.suspended != "" {
		t.Fatalf("stale retry suspended %q, want no suspension", client.suspended)
	}
	metadata, allocated, err := allocationFromNotes(client.vms[0].Notes)
	if err != nil {
		t.Fatal(err)
	}
	if !allocated || metadata.RunnerName != second.Name {
		t.Fatalf("allocation after stale retry = %#v, allocated=%t; want %q", metadata, allocated, second.Name)
	}
}

func TestNamedDeleteRequiresRunnerName(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Running", Notes: mustNotes(t, "garm-runner-new")},
	}}
	provider := testProvider(t, client)

	err := provider.DeleteInstanceWithName(context.Background(), "runner-01", "")
	if err == nil || !strings.Contains(err.Error(), "runner name") {
		t.Fatalf("DeleteInstanceWithName() error = %v, want missing runner name", err)
	}
	if client.suspended != "" {
		t.Fatalf("Suspend() VM = %q, want no suspension", client.suspended)
	}
}

func TestListInstancesFiltersPoolAndController(t *testing.T) {
	firstNotes, err := notesWithAllocation("", &allocation{ControllerID: "controller-a", PoolID: "pool-a", RunnerName: "one"})
	if err != nil {
		t.Fatal(err)
	}
	secondNotes, err := notesWithAllocation("", &allocation{ControllerID: "controller-b", PoolID: "pool-a", RunnerName: "two"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Running", Notes: firstNotes, IPAddresses: []string{"192.168.10.10"}},
		{Name: "runner-02", State: "Running", Notes: secondNotes},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)

	instances, err := provider.ListInstances(context.Background(), "pool-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Name != "one" || instances[0].Status != commonParams.InstanceRunning {
		t.Fatalf("ListInstances() = %#v, want controller-a's running instance", instances)
	}
	if len(instances[0].Addresses) != 1 || instances[0].Addresses[0].Type != commonParams.PrivateAddress {
		t.Fatalf("instance addresses = %#v, want private IPv4 address", instances[0].Addresses)
	}
}

func TestCreateFailsWhenAllGuestsAllocated(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", Notes: mustNotes(t, "one")},
		{Name: "runner-02", Notes: mustNotes(t, "two")},
		{Name: "runner-03", Notes: mustNotes(t, "three")},
	}}
	provider := testProvider(t, client)

	_, err := provider.CreateInstance(context.Background(), commonParams.BootstrapInstance{
		Name:   "four",
		PoolID: "pool-a",
		OSType: commonParams.Windows,
	})
	if err == nil {
		t.Fatal("CreateInstance() error = nil, want capacity error")
	}
}

func TestCreateSelectsLinuxGuestAndUsesSSHBootstrap(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Off"},
		{Name: "runner-02", State: "Off", IPAddresses: []string{"172.20.1.25"}},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)
	provider.cfg.VMs[1] = config.VM{
		Name:              "runner-02",
		Checkpoint:        "garm-clean",
		OSType:            "linux",
		SSHUsername:       "runner",
		SSHPrivateKeyFile: "C:\\keys\\runner",
		SSHKnownHostsFile: "C:\\keys\\known_hosts",
		SSHHostKeyAlias:   "ubuntu-fixed-alias",
	}

	instance, err := provider.CreateInstance(context.Background(), commonParams.BootstrapInstance{
		Name:   "linux-runner",
		PoolID: "pool-linux",
		OSType: commonParams.Linux,
		OSArch: commonParams.Amd64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ProviderID != "runner-02" || instance.OSType != commonParams.Linux {
		t.Fatalf("unexpected Linux instance: %#v", instance)
	}
	if client.bootstrapped != "172.20.1.25" {
		t.Fatalf("Linux bootstrap target = %q, want guest IPv4", client.bootstrapped)
	}
	if client.bootstrapAlias != "ubuntu-fixed-alias" {
		t.Fatalf("Linux host key alias = %q, want ubuntu-fixed-alias", client.bootstrapAlias)
	}
}

func TestCreateSkipsDisabledGuest(t *testing.T) {
	client := &fakeClient{vms: []vmInfo{
		{Name: "runner-01", State: "Off", IPAddresses: []string{"172.20.1.24"}},
		{Name: "runner-02", State: "Off", IPAddresses: []string{"172.20.1.25"}},
		{Name: "runner-03", State: "Off"},
	}}
	provider := testProvider(t, client)
	provider.cfg.VMs[0] = config.VM{
		Name:              "runner-01",
		Checkpoint:        "garm-clean",
		Disabled:          true,
		OSType:            "linux",
		SSHUsername:       "runner",
		SSHPrivateKeyFile: "C:\\\\keys\\\\runner",
		SSHKnownHostsFile: "C:\\\\keys\\\\known_hosts",
	}
	provider.cfg.VMs[1] = config.VM{
		Name:              "runner-02",
		Checkpoint:        "garm-clean",
		OSType:            "linux",
		SSHUsername:       "runner",
		SSHPrivateKeyFile: "C:\\\\keys\\\\runner",
		SSHKnownHostsFile: "C:\\\\keys\\\\known_hosts",
	}

	instance, err := provider.CreateInstance(context.Background(), commonParams.BootstrapInstance{
		Name:   "linux-runner",
		PoolID: "pool-linux",
		OSType: commonParams.Linux,
		OSArch: commonParams.Amd64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if instance.ProviderID != "runner-02" {
		t.Fatalf("CreateInstance() selected %q, want enabled runner-02", instance.ProviderID)
	}
	if client.prepared != "runner-02:garm-clean" || client.bootstrapped != "172.20.1.25" {
		t.Fatalf("unexpected lifecycle calls: prepared=%q bootstrapped=%q", client.prepared, client.bootstrapped)
	}
}

func TestRenderLinuxRunnerScriptUsesConfiguredGuestUser(t *testing.T) {
	filename := "actions-runner.tar.gz"
	downloadURL := "https://example.invalid/actions-runner.tar.gz"
	script, err := renderLinuxRunnerScript(commonParams.BootstrapInstance{
		OSType: commonParams.Linux,
		Labels: []string{"self-hosted", "linux"},
	}, commonParams.RunnerApplicationDownload{
		Filename:    &filename,
		DownloadURL: &downloadURL,
	}, "garm-runner", config.VM{Name: "ubuntu-24", SSHUsername: "ubuntu24runner"})
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(script)
	for _, want := range []string{
		"/home/ubuntu24runner/actions-runner",
		"chown ubuntu24runner:ubuntu24runner",
		"./svc.sh install ubuntu24runner",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered script does not contain %q", want)
		}
	}
	if strings.Contains(rendered, "/home/runner/") {
		t.Fatalf("rendered script still contains the default runner home: %q", rendered)
	}
	if strings.Contains(rendered, "\r") {
		t.Fatalf("rendered Linux script must use LF line endings, got a CR")
	}
}

func testProvider(t *testing.T, client *fakeClient) *HyperV {
	t.Helper()
	passwordFile := filepath.Join(t.TempDir(), "guest-password.txt")
	if err := os.WriteFile(passwordFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := newHyperVProvider(&config.Config{
		BootstrapTimeoutSeconds: 10,
		VMs: []config.VM{
			{Name: "runner-01", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
			{Name: "runner-02", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
			{Name: "runner-03", Checkpoint: "garm-clean", OSType: "windows", WindowsGuestUsername: "Administrator", WindowsGuestPasswordFile: passwordFile},
		},
	}, "controller-a", client)
	provider.toolFetch = func(commonParams.OSType, commonParams.OSArch, []commonParams.RunnerApplicationDownload) (commonParams.RunnerApplicationDownload, error) {
		return commonParams.RunnerApplicationDownload{}, nil
	}
	provider.windowsBootstrap = func(commonParams.BootstrapInstance, commonParams.RunnerApplicationDownload, string) (string, error) {
		return "Write-Output bootstrap", nil
	}
	provider.linuxBootstrap = func(commonParams.BootstrapInstance, commonParams.RunnerApplicationDownload, string, config.VM) ([]byte, error) {
		return []byte("#!/bin/bash"), nil
	}
	return provider
}

func mustNotes(t *testing.T, runnerName string) string {
	t.Helper()
	notes, err := notesWithAllocation("", &allocation{ControllerID: "controller-a", PoolID: "pool-a", RunnerName: runnerName})
	if err != nil {
		t.Fatal(err)
	}
	return notes
}

type fakeClient struct {
	vms            []vmInfo
	prepared       string
	suspended      string
	bootstrapped   string
	bootstrapAlias string
}

func (f *fakeClient) ListVMs(_ context.Context, _ []string) ([]vmInfo, error) {
	return append([]vmInfo(nil), f.vms...), nil
}

func (f *fakeClient) PrepareForAllocation(_ context.Context, name, checkpoint string) error {
	f.prepared = name + ":" + checkpoint
	f.setState(name, "Running")
	return nil
}

func (f *fakeClient) Suspend(_ context.Context, name string) error {
	f.suspended = name
	f.setState(name, "Saved")
	return nil
}

func (f *fakeClient) ResumeOrStart(_ context.Context, name string) error {
	f.setState(name, "Running")
	return nil
}

func (f *fakeClient) SetNotes(_ context.Context, name, notes string) error {
	for index := range f.vms {
		if f.vms[index].Name == name {
			f.vms[index].Notes = notes
			return nil
		}
	}
	return os.ErrNotExist
}

func (f *fakeClient) BootstrapWindowsGuest(_ context.Context, name, _, _, _ string) error {
	f.bootstrapped = name
	return nil
}

func (f *fakeClient) BootstrapLinuxGuest(_ context.Context, address string, _ int, _, _, _, hostKeyAlias, _ string) error {
	f.bootstrapped = address
	f.bootstrapAlias = hostKeyAlias
	return nil
}

func (f *fakeClient) setState(name, state string) {
	for index := range f.vms {
		if f.vms[index].Name == name {
			f.vms[index].State = state
			return
		}
	}
}
