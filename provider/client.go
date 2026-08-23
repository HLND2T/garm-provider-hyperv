// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package provider

import (
	"context"

	"github.com/cloudbase/garm-provider-hyperv/config"
)

// Client isolates Hyper-V operations from provider lifecycle logic.
type Client interface {
	ListVMs(context.Context, []string) ([]vmInfo, error)
	PrepareForAllocation(context.Context, string, string) error
	Suspend(context.Context, string) error
	ResumeOrStart(context.Context, string) error
	SetNotes(context.Context, string, string) error
	BootstrapWindowsGuest(context.Context, string, string, string, string) error
	BootstrapLinuxGuest(context.Context, string, int, string, string, string, string) error
}

type vmInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	State       string   `json:"state"`
	Notes       string   `json:"notes"`
	IPAddresses []string `json:"ip_addresses"`
}

func NewClient(cfg *config.Config) (Client, error) {
	return newWindowsClient(cfg)
}
