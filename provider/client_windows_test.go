// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package provider

import (
	"strings"
	"testing"
)

func TestSanitizeWindowsBootstrapScriptRemovesPolicyMutation(t *testing.T) {
	input := "Set-ExecutionPolicy RemoteSigned -Force -ErrorAction SilentlyContinue\r\nWrite-Output ready\r\n"
	got := sanitizeWindowsBootstrapScript(input)
	if strings.Contains(got, "Set-ExecutionPolicy") {
		t.Fatalf("sanitized script still contains Set-ExecutionPolicy: %q", got)
	}
	if !strings.Contains(got, "Write-Output ready") {
		t.Fatalf("sanitized script lost subsequent commands: %q", got)
	}
}

func TestTransientSSHBootstrapFailure(t *testing.T) {
	tests := []struct {
		message string
		want    bool
	}{
		{message: "banner exchange: Connection to UNKNOWN port -1: Connection refused", want: true},
		{message: "ssh: connect to host 172.22.93.94 port 22: Connection timed out", want: true},
		{message: "Permission denied (publickey,password).", want: false},
		{message: "Host key verification failed.", want: false},
	}

	for _, test := range tests {
		t.Run(test.message, func(t *testing.T) {
			if got := isTransientSSHBootstrapFailure(test.message); got != test.want {
				t.Fatalf("isTransientSSHBootstrapFailure(%q) = %t, want %t", test.message, got, test.want)
			}
		})
	}
}

func TestLinuxSSHArgsHostKeyAlias(t *testing.T) {
	tests := []struct {
		name      string
		alias     string
		wantAlias bool
	}{
		{name: "alias set", alias: "Ubuntu24Runner", wantAlias: true},
		{name: "alias empty keeps legacy IP lookup", alias: "", wantAlias: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := strings.Join(linuxSSHArgs("172.19.1.25", 22, "runner", `C:\keys\id`, `C:\keys\known_hosts`, test.alias), " ")
			for _, want := range []string{
				"StrictHostKeyChecking=yes",
				`UserKnownHostsFile=C:\keys\known_hosts`,
				"runner@172.19.1.25",
				"sudo -n /bin/bash -s",
			} {
				if !strings.Contains(args, want) {
					t.Fatalf("ssh args %q do not contain %q", args, want)
				}
			}
			hasAlias := strings.Contains(args, "HostKeyAlias=")
			if hasAlias != test.wantAlias {
				t.Fatalf("ssh args %q HostKeyAlias present = %t, want %t", args, hasAlias, test.wantAlias)
			}
			if test.wantAlias && !strings.Contains(args, "HostKeyAlias="+test.alias) {
				t.Fatalf("ssh args %q do not contain HostKeyAlias=%s", args, test.alias)
			}
		})
	}
}

func TestRedactBootstrapOutput(t *testing.T) {
	input := "BEARER_TOKEN=secret.value-1\nAuthorization: Bearer another.secret-2\nready"
	got := redactBootstrapOutput(input)
	if strings.Contains(got, "secret.value-1") || strings.Contains(got, "another.secret-2") {
		t.Fatalf("redacted output exposes a credential: %q", got)
	}
	if !strings.Contains(got, "BEARER_TOKEN=[redacted]") || !strings.Contains(got, "Authorization: Bearer [redacted]") {
		t.Fatalf("redacted output is missing expected markers: %q", got)
	}
}
