// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

//go:build !windows

package provider

import (
	"fmt"

	"github.com/cloudbase/garm-provider-hyperv/config"
)

func newWindowsClient(*config.Config) (Client, error) {
	return nil, fmt.Errorf("garm-provider-hyperv must run on a Windows Hyper-V host")
}
