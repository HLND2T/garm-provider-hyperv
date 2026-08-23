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

	result, err := environment.Run(ctx, prov)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to run command: %s", err)
		os.Exit(commonExecution.ResolveErrorToExitCode(err))
	}
	if result != "" {
		fmt.Fprint(os.Stdout, result)
	}
}
