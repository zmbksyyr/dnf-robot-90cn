package main

import (
	"os"
	"path/filepath"
	"testing"

	runtimeinit "robot/internal/bootstrap/runtime"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestSimulatorStartupDoesNotMarkRuntimeBeforePVFInit(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	cfg := &config.SysConfig{
		ConfigDir:      root,
		DFGameR:        filepath.Join(root, "missing", "DfoServer.exe"),
		RobotConnectIP: "127.0.0.1",
		RobotGamePort:  1,
	}
	selection := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 3}
	if got := runS4A21Backend(cfg, paths, shared.BackendInfo{ID: shared.BackendS4A21}, selection); got == 0 {
		t.Fatal("simulator startup unexpectedly succeeded with missing PVF")
	}
	if _, err := os.Stat(paths.BackendRuntime()); !os.IsNotExist(err) {
		t.Fatalf("runtime marker was written before PVF initialization: %v", err)
	}
	if _, err := os.Stat(paths.RobotConfig()); err != nil {
		t.Fatalf("config-only initialization did not release robot config: %v", err)
	}
	if _, err := runtimeinit.PrepareBackendRuntime(paths, selection); err != nil {
		t.Fatalf("failed startup left runtime layout unusable: %v", err)
	}
}
