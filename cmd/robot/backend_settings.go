package main

import (
	"fmt"
	"strconv"
	"strings"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func applyBackendSelectionSettings(cfg *config.SysConfig, selection shared.BackendSelection) error {
	if cfg == nil {
		return fmt.Errorf("backend settings require config")
	}
	if len(selection.Settings) == 0 {
		return fmt.Errorf("backend settings are not configured")
	}
	serverDir := strings.TrimSpace(selection.Settings["server_directory"])
	host := strings.TrimSpace(selection.Settings["server_host"])
	portText := strings.TrimSpace(selection.Settings["game_port"])
	if serverDir == "" || host == "" || portText == "" {
		return fmt.Errorf("S4A21 backend settings are incomplete")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("S4A21 game port must be between 1 and 65535")
	}
	cfg.ServerDirectory = serverDir
	cfg.RobotConnectIPSetting = host
	cfg.RobotConnectIP = host
	cfg.RobotGamePort = port
	return nil
}
