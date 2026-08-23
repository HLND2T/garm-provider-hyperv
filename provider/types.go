// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright 2026

package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

const metadataMarker = "[garm-provider-hyperv]"

type allocation struct {
	ControllerID string `json:"controller_id"`
	PoolID       string `json:"pool_id"`
	RunnerName   string `json:"runner_name"`
}

func allocationFromNotes(notes string) (allocation, bool, error) {
	index := strings.Index(notes, metadataMarker)
	if index == -1 {
		return allocation{}, false, nil
	}
	payload := strings.TrimSpace(notes[index+len(metadataMarker):])
	if payload == "" {
		return allocation{}, true, fmt.Errorf("empty GARM allocation metadata")
	}
	var value allocation
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return allocation{}, true, fmt.Errorf("decode GARM allocation metadata: %w", err)
	}
	if value.ControllerID == "" || value.PoolID == "" || value.RunnerName == "" {
		return allocation{}, true, fmt.Errorf("incomplete GARM allocation metadata")
	}
	return value, true, nil
}

func notesWithAllocation(notes string, value *allocation) (string, error) {
	index := strings.Index(notes, metadataMarker)
	prefix := strings.TrimSpace(notes)
	if index >= 0 {
		prefix = strings.TrimSpace(notes[:index])
	}
	if value == nil {
		return prefix, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode GARM allocation metadata: %w", err)
	}
	if prefix == "" {
		return fmt.Sprintf("%s\n%s", metadataMarker, encoded), nil
	}
	return fmt.Sprintf("%s\n%s\n%s", prefix, metadataMarker, encoded), nil
}
