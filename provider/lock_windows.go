// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

//go:build windows

package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

func withProviderLock(ctx context.Context, controllerID string, timeout time.Duration, work func() error) error {
	hash := sha256.Sum256([]byte(controllerID))
	name := fmt.Sprintf("Local\\garm-provider-hyperv-%x", hash[:8])
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(name))
	// CreateMutex returns a usable handle together with ERROR_ALREADY_EXISTS
	// when another provider process already created the named mutex. That is
	// the normal contended-lock path; WaitForSingleObject below must handle it.
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return fmt.Errorf("create provider mutex: %w", err)
	}
	if handle == 0 {
		return fmt.Errorf("create provider mutex: received an invalid handle")
	}
	defer windows.CloseHandle(handle)

	deadline := time.Now().Add(timeout)
	for {
		status, err := windows.WaitForSingleObject(handle, 500)
		if err != nil {
			return fmt.Errorf("wait for provider mutex: %w", err)
		}
		switch status {
		case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
			defer windows.ReleaseMutex(handle)
			return work()
		case uint32(windows.WAIT_TIMEOUT):
			if err := ctx.Err(); err != nil {
				return err
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for another Hyper-V provider operation")
			}
		default:
			return fmt.Errorf("unexpected provider mutex result: %d", status)
		}
	}
}
