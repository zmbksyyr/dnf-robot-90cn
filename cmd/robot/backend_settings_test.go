package main

import (
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestApplyS4A21BackendSelectionSettings(t *testing.T) {
	cfg := &config.SysConfig{DFGameR: "old", RobotConnectIP: "old", RobotGamePort: 10010}
	selection := shared.BackendSelection{BackendID: shared.BackendS4A21, Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "192.0.2.21",
		"game_port":        "10011",
		"database_path":    `D:\game\DfoServer\Data\inventory.db`,
	}}
	if err := applyBackendSelectionSettings(cfg, selection); err != nil {
		t.Fatal(err)
	}
	if cfg.DFGameR != `D:\game\DfoServer` || cfg.RobotConnectIP != "192.0.2.21" || cfg.RobotConnectIPSetting != "192.0.2.21" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestApplyS4A21BackendSelectionSettingsDerivesOptionalDatabase(t *testing.T) {
	cfg := &config.SysConfig{}
	selection := shared.BackendSelection{BackendID: shared.BackendS4A21, Settings: map[string]string{
		"server_directory": `D:\game\DfoServer`,
		"server_host":      "127.0.0.1",
		"game_port":        "10011",
	}}
	if err := applyBackendSelectionSettings(cfg, selection); err != nil {
		t.Fatal(err)
	}
	if cfg.DFGameR != `D:\game\DfoServer` || cfg.RobotConnectIP != "127.0.0.1" || cfg.RobotGamePort != 10011 {
		t.Fatalf("config=%+v", cfg)
	}
}

func TestNativeBackendDoesNotApplySimulatorSettings(t *testing.T) {
	cfg := &config.SysConfig{DFGameR: "native", RobotConnectIP: "native", RobotGamePort: 10010}
	selection := shared.BackendSelection{BackendID: shared.BackendNative, Settings: map[string]string{"game_port": "10011"}}
	if err := applyBackendSelectionSettings(cfg, selection); err != nil {
		t.Fatal(err)
	}
	if cfg.DFGameR != "native" || cfg.RobotConnectIP != "native" || cfg.RobotGamePort != 10010 {
		t.Fatalf("native config was modified: %+v", cfg)
	}
}
