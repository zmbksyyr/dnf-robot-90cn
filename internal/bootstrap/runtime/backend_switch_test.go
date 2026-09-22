package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	old := shared.BackendSelection{BackendID: shared.BackendNative, ConfigGeneration: 1}
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
