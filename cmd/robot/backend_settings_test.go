package main

import (
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func testBackendInfo() shared.BackendInfo {
	return shared.BackendInfo{
		ID: "test",
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", RuntimeSource: "server_directory", Required: true},
			{Key: "server_host", Label: "Host", RuntimeSource: "game_host", Required: true, Default: "127.0.0.1"},
			{Key: "game_port", Label: "Port", RuntimeSource: "game_port", Required: true, Default: "10011"},
			{Key: "database_path", Label: "Database"},
		},
	}
}

func TestApplyBackendSelectionSettings(t *testing.T) {
	cfg := &config.SysConfig{ServerDirectory: "old", RobotConnectIP: "old", RobotGamePort: 10010}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "192.0.2.21",
		"game_port":        "10011",
		"database_path":    `D:\game\DfoServer\Data\inventory.db`,
	}}
	if err := applyBackendSelectionSettings(cfg, testBackendInfo(), selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "192.0.2.21" || cfg.RobotConnectIPSetting != "192.0.2.21" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyBackendSelectionSettingsDerivesOptionalDatabase(t *testing.T) {
	cfg := &config.SysConfig{}
	selection := shared.BackendSelection{BackendID: shared.BackendID("test"), Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "127.0.0.1",
		"game_port":        "10011",
	}}
	if err := applyBackendSelectionSettings(cfg, testBackendInfo(), selection); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerDirectory != `D:\game\DfoServer` || cfg.RobotConnectIP != "127.0.0.1" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyBackendSelectionSettingsRequiresInitialization(t *testing.T) {
	if err := applyBackendSelectionSettings(&config.SysConfig{}, testBackendInfo(), shared.BackendSelection{}); err == nil {
		t.Fatal("missing backend settings unexpectedly accepted")
	}
	if err := applyBackendSelectionSettings(&config.SysConfig{}, shared.BackendInfo{ID: "empty"}, shared.BackendSelection{
		Settings: map[string]string{"server_directory": "x"},
	}); err == nil {
		t.Fatal("backend without declared settings unexpectedly accepted")
	}
}
