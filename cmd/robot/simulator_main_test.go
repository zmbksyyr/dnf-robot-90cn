package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	runtimeinit "robot/internal/bootstrap/runtime"
	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	s4a21backend "robot/internal/composition/backend/s4a21"
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

type recordingBatchLoadoutApplier struct {
	requests []s4a21backend.CharacterLoadoutRequest
}

func (a *recordingBatchLoadoutApplier) ApplyCharacterLoadout(context.Context, string, robotcap.Info) error {
	return nil
}

func (a *recordingBatchLoadoutApplier) ReconcileCharacterLoadouts(_ context.Context, requests []s4a21backend.CharacterLoadoutRequest) ([]robotcap.Info, error) {
	a.requests = append(a.requests, requests...)
	updates := make([]robotcap.Info, 0, len(requests))
	for _, request := range requests {
		request.Robot.Level = 70
		updates = append(updates, request.Robot)
	}
	return updates, nil
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

func TestReconcileSimulatorLoadoutsUsesBatchAdapter(t *testing.T) {
	state, err := robotstate.OpenFileStore(filepath.Join(t.TempDir(), "robot_state.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := state.RegisterRobots(ctx, []robotcap.Info{{UID: 7, Name: "bot", Level: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterIdentity(ctx, robotstate.Identity{Backend: shared.BackendS4A21, Account: "robot7", CharacterName: "bot"}); err != nil {
		t.Fatal(err)
	}
	applier := &recordingBatchLoadoutApplier{}
	if err := reconcileSimulatorLoadouts(ctx, state, applier); err != nil {
		t.Fatal(err)
	}
	if len(applier.requests) != 1 || applier.requests[0].Account != "robot7" || applier.requests[0].Robot.Name != "bot" {
		t.Fatalf("batch requests=%+v", applier.requests)
	}
	robots, err := state.SelectRobots(ctx, robotcap.CommandRequest{Count: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(robots) != 1 || robots[0].Level != 70 {
		t.Fatalf("updated robots=%+v", robots)
	}
}
