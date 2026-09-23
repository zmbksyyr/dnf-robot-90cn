package main

import (
	"fmt"
	"strconv"
	"strings"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func applyBackendSelectionSettings(cfg *config.SysConfig, selection shared.BackendSelection) error {
	if cfg == nil || selection.BackendID != shared.BackendS4A21 || len(selection.Settings) == 0 {
		return nil
	}
	serverDir := strings.TrimSpace(selection.Settings["server_directory"])
	host := strings.TrimSpace(selection.Settings["server_host"])
	portText := strings.TrimSpace(selection.Settings["game_port"])
	databasePath := strings.TrimSpace(selection.Settings["database_path"])
	if serverDir == "" || host == "" || portText == "" || databasePath == "" {
		return fmt.Errorf("S4A21 backend settings are incomplete")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("S4A21 game port must be between 1 and 65535")
	}
	cfg.DFGameR = serverDir
	cfg.RobotConnectIPSetting = host
	cfg.RobotConnectIP = host
	cfg.RobotGamePort = port
	return nil
}
