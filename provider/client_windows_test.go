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

func TestPrepareLinuxGuestBootstrapScriptCreatesGARMRunnerAccount(t *testing.T) {
	script := prepareLinuxGuestBootstrapScript("printf ready")
	for _, want := range []string{
		"id -u runner",
		"useradd --create-home --shell /bin/bash --user-group runner",
		"install -d -m 0755 -o runner -g runner /home/runner",
		"printf ready",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("prepared script does not contain %q: %q", want, script)
		}
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
