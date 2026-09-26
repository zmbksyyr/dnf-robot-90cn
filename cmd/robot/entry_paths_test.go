package main

import (
	"context"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	s4a21backend "robot/internal/composition/backend/s4a21"
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
		{name: "defaults fill in", selection: shared.BackendSelection{Settings: map[string]string{"server_directory": "C:\\srv"}}, wantErr: false},
		{name: "bad port", selection: shared.BackendSelection{Settings: map[string]string{
			"server_directory": "C:\\srv", "server_host": "127.0.0.1", "game_port": "70000"}}, wantErr: true},
		{name: "valid", selection: shared.BackendSelection{Settings: map[string]string{
			"server_directory": "C:\\srv", "server_host": "10.0.0.2", "game_port": "10011"}}, wantErr: false},
	}
	for _, tc := range cases {
		cfg.ServerDirectory, cfg.RobotConnectIP, cfg.RobotGamePort = "", "", 0
		err := applyBackendSelectionSettings(cfg, testBackendInfo(), tc.selection)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("%s: expected an error", tc.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if cfg.ServerDirectory != "C:\\srv" || cfg.RobotConnectIP == "" || cfg.RobotGamePort != 10011 {
			t.Fatalf("%s: config = %+v", tc.name, cfg)
		}
	}
}

func TestComposeRuntimeRejectsIncompleteOptions(t *testing.T) {
	for _, opts := range []s4a21backend.RuntimeComposeOptions{
		{},
		{AccountPrefix: "robot"},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1"},
		{AccountPrefix: "robot", ConnectIP: "127.0.0.1", GamePort: 10011},
	} {
		if _, err := s4a21backend.ComposeRuntime(context.Background(), opts); err == nil {
			t.Fatalf("incomplete compose options accepted: %+v", opts)
		}
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
