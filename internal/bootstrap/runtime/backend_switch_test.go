package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestPrepareBackendRuntimeBacksUpRobotFilesAndPreservesSystemConfig(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MainConfig(), []byte("system-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RobotConfig(), []byte("old-robot-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Templates, "old.json"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	old := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 1}
	oldData, _ := json.Marshal(old)
	if err := os.WriteFile(paths.BackendRuntime(), oldData, 0600); err != nil {
		t.Fatal(err)
	}

	want := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 2}
	changed, err := PrepareBackendRuntime(paths, want)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got, err := os.ReadFile(paths.MainConfig()); err != nil || string(got) != "system-config" {
		t.Fatalf("system config got=%q err=%v", got, err)
	}
	if _, err := os.Stat(paths.RobotConfig()); !os.IsNotExist(err) {
		t.Fatalf("old robot config still exists: %v", err)
	}
	selectionData, err := os.ReadFile(paths.BackendSelection())
	if err != nil {
		t.Fatalf("selection was not recreated: %v", err)
	}
	selection, err := shared.DecodeBackendSelection(selectionData)
	if err != nil || selection.BackendID != want.BackendID || selection.ConfigGeneration != want.ConfigGeneration {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
	entries, err := os.ReadDir(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	backupFound := false
	for _, entry := range entries {
		if entry.IsDir() && len(entry.Name()) > len(backendBackupPrefix) && entry.Name()[:len(backendBackupPrefix)] == backendBackupPrefix {
			backupFound = true
			if _, err := os.Stat(filepath.Join(filepath.Dir(root), entry.Name(), "conf", "robot_config.ini")); err != nil {
				t.Fatalf("backup robot config missing: %v", err)
			}
		}
	}
	if !backupFound {
		t.Fatal("backend backup was not created")
	}
	if err := MarkBackendRuntimeApplied(paths, want); err != nil {
		t.Fatal(err)
	}
	got, err := readBackendRuntime(paths.BackendRuntime())
	if err != nil || got.BackendID != want.BackendID || got.ConfigGeneration != want.ConfigGeneration {
		t.Fatalf("runtime marker=%+v err=%v", got, err)
	}
}

func TestPrepareBackendRuntimeDoesNotResetWithoutAppliedMarker(t *testing.T) {
	paths := layout.New(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RobotConfig(), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := PrepareBackendRuntime(paths, shared.BackendSelection{BackendID: shared.BackendS4A21})
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(paths.RobotConfig()); err != nil {
		t.Fatalf("robot config was unexpectedly reset: %v", err)
	}
}

func TestPrepareBackendRuntimeDoesNotCarrySimulatorStateIntoNative(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MainConfig(), []byte("system-config"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.State, "simulator.cache"), []byte(`{"backend":"sim_a21"}`), 0600); err != nil {
		t.Fatal(err)
	}
	previous := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 4}
	previousData, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BackendRuntime(), previousData, 0600); err != nil {
		t.Fatal(err)
	}

	target := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 5}
	changed, err := PrepareBackendRuntime(paths, target)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(paths.State, "simulator.cache")); !os.IsNotExist(err) {
		t.Fatalf("simulator robot state survived native switch: %v", err)
	}
	if got, err := os.ReadFile(paths.MainConfig()); err != nil || string(got) != "system-config" {
		t.Fatalf("system config got=%q err=%v", got, err)
	}
}

func TestPrepareBackendRuntimeRetriesUntilAppliedMarkerIsWritten(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.RobotConfig(), []byte("old-config"), 0600); err != nil {
		t.Fatal(err)
	}
	previous := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 1}
	previousData, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.BackendRuntime(), previousData, 0600); err != nil {
		t.Fatal(err)
	}
	target := shared.BackendSelection{BackendID: shared.BackendS4A21, ConfigGeneration: 2}

	changed, err := PrepareBackendRuntime(paths, target)
	if err != nil || !changed {
		t.Fatalf("first preparation changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(paths.RobotConfig()); !os.IsNotExist(err) {
		t.Fatalf("old robot config unexpectedly survived first preparation: %v", err)
	}
	changed, err = PrepareBackendRuntime(paths, target)
	if err != nil || !changed {
		t.Fatalf("second preparation changed=%v err=%v", changed, err)
	}
	entries, err := os.ReadDir(filepath.Dir(root))
	if err != nil {
		t.Fatal(err)
	}
	backups := 0
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), backendBackupPrefix) {
			backups++
		}
	}
	if backups < 2 {
		t.Fatalf("retry did not create a fresh diagnostic backup, got %d", backups)
	}
	if err := MarkBackendRuntimeApplied(paths, target); err != nil {
		t.Fatal(err)
	}
	changed, err = PrepareBackendRuntime(paths, target)
	if err != nil || changed {
		t.Fatalf("applied marker did not stop repeated preparation, changed=%v err=%v", changed, err)
	}
}
