package main

import (
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestApplyS4A21BackendSelectionSettings(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: "old", RobotConnectIP: "old", RobotGamePort: 10010}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "192.0.2.21",
		"game_port":        "10011",
		"database_path":    `D:\game\DfoServer\Data\inventory.db`,
	}}
	if err := applyBackendSelectionSettings(cfg, selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "192.0.2.21" || cfg.RobotConnectIPSetting != "192.0.2.21" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyS4A21BackendSelectionSettingsDerivesOptionalDatabase(t *testing.T) {
	cfg := &config.SysConfig{}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "127.0.0.1",
		"game_port":        "10011",
	}}
	if err := applyBackendSelectionSettings(cfg, selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "127.0.0.1" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyBackendSelectionSettingsRequiresInitialization(t *testing.T) {
	if err := applyBackendSelectionSettings(&config.SysConfig{}, shared.BackendSelection{}); err == nil {
		t.Fatal("missing backend settings unexpectedly accepted")
	}
}
