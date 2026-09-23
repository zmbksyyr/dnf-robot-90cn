package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	runtimeinit "robot/internal/bootstrap/runtime"
	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

type recordingLoadoutApplier struct {
	calls []string
}

func (a *recordingLoadoutApplier) ApplyCharacterLoadout(_ context.Context, account string, info robotcap.Info) error {
	a.calls = append(a.calls, account+"/"+info.Name)
	return nil
}

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
	if got := runSimulatorBackend(cfg, paths, shared.BackendInfo{ID: shared.BackendS4A21}, selection); got == 0 {
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

func TestReconcileSimulatorLoadoutsUsesPersistedIdentity(t *testing.T) {
	state, err := robotstate.OpenFileStore(filepath.Join(t.TempDir(), "robot_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := state.RegisterRobots(ctx, []robotcap.Info{{UID: 7, Name: "bot"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterIdentity(ctx, robotstate.Identity{Backend: shared.BackendS4A21, Account: "robot7", CharacterName: "bot"}); err != nil {
		t.Fatal(err)
	}
	applier := &recordingLoadoutApplier{}
	if err := reconcileSimulatorLoadouts(ctx, state, applier); err != nil {
		t.Fatal(err)
	}
	if len(applier.calls) != 1 || applier.calls[0] != "robot7/bot" {
		t.Fatalf("loadout calls=%v", applier.calls)
	}
}
