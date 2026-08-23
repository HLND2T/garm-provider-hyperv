// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

//go:build windows

package provider

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWithProviderLockAcceptsAnExistingNamedMutex(t *testing.T) {
	controllerID := fmt.Sprintf("lock-test-%d", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(controllerID))
	name := fmt.Sprintf("Local\\garm-provider-hyperv-%x", hash[:8])
	handle, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr(name))
	if err != nil {
		t.Fatalf("create test mutex: %v", err)
	}
	defer windows.CloseHandle(handle)

	called := false
	if err := withProviderLock(context.Background(), controllerID, time.Second, func() error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("withProviderLock() error = %v", err)
	}
	if !called {
		t.Fatal("withProviderLock() did not run work")
	}
}
