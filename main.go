// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	execution "github.com/cloudbase/garm-provider-common/execution"
	commonExecution "github.com/cloudbase/garm-provider-common/execution/common"

	"github.com/cloudbase/garm-provider-hyperv/provider"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	environment, err := execution.GetEnvironment()
	if err != nil {
		log.Fatal(err)
	}

	prov, err := provider.NewHyperVProvider(environment.ProviderConfigFile, environment.ControllerID)
	if err != nil {
		log.Fatal(err)
	}

	result, err := runProviderCommand(ctx, environment, prov)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to run command: %s", err)
		os.Exit(commonExecution.ResolveErrorToExitCode(err))
	}
	if result != "" {
		fmt.Fprint(os.Stdout, result)
	}
}

// runProviderCommand is a compatibility bridge for the v0.1.0 provider-common
// release currently pinned by this provider. Once that dependency includes
// NamedInstanceDeleter dispatch, this explicit delete branch can be removed.
func runProviderCommand(ctx context.Context, environment execution.Environment, prov interface{}) (string, error) {
	instanceID := environment.EnvironmentV010.InstanceID
	instanceName := os.Getenv("GARM_INSTANCE_NAME")
	command := environment.EnvironmentV010.Command
	if environment.EnvironmentV011.Command == commonExecution.DeleteInstanceCommand {
		instanceID = environment.EnvironmentV011.InstanceID
		command = environment.EnvironmentV011.Command
	}
	if command != commonExecution.DeleteInstanceCommand {
		return environment.Run(ctx, prov)
	}

	namedProvider, ok := prov.(provider.NamedInstanceDeleter)
	if !ok {
		return "", fmt.Errorf("provider does not support generation-aware instance deletion")
	}
	if err := namedProvider.DeleteInstanceWithName(
		ctx,
		instanceID,
		instanceName,
	); err != nil {
		return "", fmt.Errorf("failed to delete instance from provider: %w", err)
	}
	return "", nil
}
