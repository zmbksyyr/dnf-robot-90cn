package main

import (
	"fmt"
	"strconv"
	"strings"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

// applyBackendSelectionSettings maps the adapter-declared settings onto the
// process configuration using each field's runtime source. Adapter key names
// stay in the descriptor; the composition root only knows the runtime sources.
func applyBackendSelectionSettings(cfg *config.SysConfig, info shared.BackendInfo, selection shared.BackendSelection) error {
	if cfg == nil {
		return fmt.Errorf("backend settings require config")
	}
	if len(selection.Settings) == 0 {
		return fmt.Errorf("backend settings are not configured")
	}
	if len(info.Settings) == 0 {
		return fmt.Errorf("backend %s declares no runtime settings", info.ID)
	}
	applied := make(map[string]bool, len(info.Settings))
	for _, field := range info.Settings {
		source := strings.TrimSpace(field.RuntimeSource)
		if source == "" {
			continue
		}
		value := strings.TrimSpace(selection.Settings[field.Key])
		if value == "" {
			value = strings.TrimSpace(field.Default)
		}
		if value == "" {
			if field.Required {
				return fmt.Errorf("%s is required", field.Label)
			}
			continue
		}
		switch source {
		case "server_directory":
			cfg.ServerDirectory = value
		case "game_host":
			cfg.RobotConnectIPSetting = value
			cfg.RobotConnectIP = value
		case "game_port":
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("%s must be between 1 and 65535", field.Label)
			}
			cfg.RobotGamePort = port
		default:
			continue
		}
		applied[source] = true
	}
	// The adapter descriptor declares which runtime sources it needs; only
	// fields marked required must have been applied.
	for _, field := range info.Settings {
		source := strings.TrimSpace(field.RuntimeSource)
		if source == "" || !field.Required {
			continue
		}
		if !applied[source] {
			return fmt.Errorf("backend %s runtime settings are incomplete", info.ID)
		}
	}
	return nil
}
