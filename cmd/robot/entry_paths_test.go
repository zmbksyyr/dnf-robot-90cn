package main

import (
	"os"
	"path/filepath"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestApplyBackendSelectionSettingsValidatesInput(t *testing.T) {
	cfg := &config.SysConfig{}
	cases := []struct {
		name      string
		selection shared.BackendSelection
		wantErr   bool
	}{
		{name: "missing settings", selection: shared.BackendSelection{}, wantErr: true},
		{name: "incomplete", selection: shared.BackendSelection{Settings: map[string]string{"server_directory": "C:\\srv"}}, wantErr: true},
		{name: "bad port", selection: shared.BackendSelection{Settings: map[string]string{
			"server_directory": "C:\\srv", "server_host": "127.0.0.1", "game_port": "70000"}}, wantErr: true},
		{name: "valid", selection: shared.BackendSelection{Settings: map[string]string{
			"server_directory": "C:\\srv", "server_host": "10.0.0.2", "game_port": "10011"}}, wantErr: false},
	}
	for _, tc := range cases {
		cfg.ServerDirectory, cfg.RobotConnectIP, cfg.RobotGamePort = "", "", 0
		err := applyBackendSelectionSettings(cfg, tc.selection)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected an error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if cfg.ServerDirectory != "C:\\srv" || cfg.RobotConnectIP != "10.0.0.2" || cfg.RobotGamePort != 10011 {
			t.Fatalf("%s: config = %+v", tc.name, cfg)
		}
	}
}

func TestComposeBackendTransportsRequiresCompleteAddress(t *testing.T) {
	if _, err := composeBackendTransports(shared.BackendInfo{ID: "test"}, nil); err == nil {
		t.Fatal("missing config was accepted")
	}
	cfg := &config.SysConfig{RobotConnectIP: "127.0.0.1"}
	if _, err := composeBackendTransports(shared.BackendInfo{ID: "test"}, cfg); err == nil {
		t.Fatal("missing game port was accepted")
	}
	cfg.RobotGamePort = 10011
	bundle, err := composeBackendTransports(shared.BackendInfo{ID: "test"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.actions == nil || bundle.sessions == nil || bundle.close == nil {
		t.Fatalf("bundle = %+v", bundle)
	}
	if err := bundle.close(); err != nil {
		t.Fatalf("close bundle: %v", err)
	}
}

func TestEnsureAdapterOpenFileLimitValidatesCapacity(t *testing.T) {
	if err := ensureAdapterOpenFileLimit(robotconfig.RuntimeConfig{}); err == nil {
		t.Fatal("zero online capacity was accepted")
	}
	if err := ensureAdapterOpenFileLimit(robotconfig.RuntimeConfig{MaxOnlineRobots: 10}); err != nil {
		t.Fatalf("valid capacity rejected: %v", err)
	}
}

func TestS4A21PVFPathResolvesDirectoryAndFile(t *testing.T) {
	dir := t.TempDir()
	pvf := filepath.Join(dir, "Script.pvf")
	if err := os.WriteFile(pvf, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := s4a21PVFPath(dir); err != nil || got != pvf {
		t.Fatalf("directory resolution = %q, %v", got, err)
	}
	if got, err := s4a21PVFPath(pvf); err != nil || got != pvf {
		t.Fatalf("file resolution = %q, %v", got, err)
	}
	if _, err := s4a21PVFPath(""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
	if _, err := s4a21PVFPath(t.TempDir()); err == nil {
		t.Fatal("directory without Script.pvf was accepted")
	}
}

func TestS4A21DatabasePathPrefersExplicitPath(t *testing.T) {
	dbDir := t.TempDir()
	explicit := filepath.Join(dbDir, "custom.db")
	if err := os.WriteFile(explicit, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := s4a21DatabasePath("", explicit); err != nil || got != explicit {
		t.Fatalf("explicit database = %q, %v", got, err)
	}

	serverDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(serverDir, "Data"), 0755); err != nil {
		t.Fatal(err)
	}
	derived := filepath.Join(serverDir, "Data", "inventory.db")
	if err := os.WriteFile(derived, []byte("db"), 0644); err != nil {
		t.Fatal(err)
	}
	if got, err := s4a21DatabasePath(serverDir, ""); err != nil || got != derived {
		t.Fatalf("derived database = %q, %v", got, err)
	}
	if _, err := s4a21DatabasePath("", ""); err == nil {
		t.Fatal("empty server directory was accepted")
	}
}
