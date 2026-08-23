// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

//go:build !windows

package provider

import (
	"context"
	"time"
)

func withProviderLock(_ context.Context, _ string, _ time.Duration, work func() error) error {
	return work()
}
