// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package main

import (
	"context"
	"testing"

	execution "github.com/cloudbase/garm-provider-common/execution"
	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"
	executionv010 "github.com/cloudbase/garm-provider-common/execution/v0.1.0"
)

type recordingNamedDeleter struct {
	instanceID   string
	instanceName string
}

func (p *recordingNamedDeleter) DeleteInstanceWithName(_ context.Context, instanceID, instanceName string) error {
	p.instanceID = instanceID
	p.instanceName = instanceName
	return nil
}

func TestRunProviderCommandBridgesNamedDelete(t *testing.T) {
	t.Setenv("GARM_INSTANCE_NAME", "runner-a")
	provider := &recordingNamedDeleter{}
	environment := execution.Environment{
		EnvironmentV010: executionv010.EnvironmentV010{
			Command:    commonExecution.DeleteInstanceCommand,
			InstanceID: "vm-01",
		},
	}

	result, err := runProviderCommand(context.Background(), environment, provider)
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Fatalf("result = %q, want empty", result)
	}
	if provider.instanceID != "vm-01" || provider.instanceName != "runner-a" {
		t.Fatalf("delete identity = %q/%q, want vm-01/runner-a", provider.instanceID, provider.instanceName)
	}
}
