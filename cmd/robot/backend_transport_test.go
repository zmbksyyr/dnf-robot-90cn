package main

import (
	"path/filepath"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestComposeNativeTransportsLeavesAdaptersUnset(t *testing.T) {
	bundle, err := composeBackendTransports(shared.BackendInfo{ID: shared.BackendNative}, &config.SysConfig{})
	if err != nil || bundle.actions != nil || bundle.sessions != nil {
		t.Fatalf("bundle=%+v err=%v", bundle, err)
	}
	if err := bundle.close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenBackendRobotStateNativeUsesLegacyRepository(t *testing.T) {
	state, err := openBackendRobotState(shared.BackendInfo{ID: shared.BackendNative}, layout.New(filepath.Join(t.TempDir(), "config")))
	if err != nil || state != nil {
		t.Fatalf("state=%T err=%v", state, err)
	}
}

func TestComposeS4A21TransportsRequiresGameAddress(t *testing.T) {
	if _, err := composeBackendTransports(shared.BackendInfo{ID: shared.BackendS4A21}, &config.SysConfig{}); err == nil {
		t.Fatal("incomplete S4A21 address unexpectedly accepted")
	}
}
