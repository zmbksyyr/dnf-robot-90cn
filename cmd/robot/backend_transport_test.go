package main

import (
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestComposeRejectsNonS4A21Backend(t *testing.T) {
	if _, err := composeBackendTransports(shared.BackendInfo{ID: "other"}, &config.SysConfig{}); err == nil {
		t.Fatal("non-S4A21 backend unexpectedly accepted")
	}
}

func TestComposeS4A21TransportsRequiresGameAddress(t *testing.T) {
	if _, err := composeBackendTransports(shared.BackendInfo{ID: shared.BackendS4A21}, &config.SysConfig{}); err == nil {
		t.Fatal("incomplete S4A21 address unexpectedly accepted")
	}
}
