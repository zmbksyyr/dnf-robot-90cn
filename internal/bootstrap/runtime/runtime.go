package runtime

import (
	"embed"
	"fmt"
	"os"

	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

//go:embed defaults/*
var defaultFiles embed.FS

// InitConfigOnly prepares the public runtime files and the selected adapter's
// defaults. PVF parsing, transport setup, and persistence belong to the
// adapter lifecycle after this step.
func InitConfigOnly(cfg *config.SysConfig) error {
	return InitConfigForBackend(cfg, shared.BackendInfo{})
}

// InitConfigForBackend releases only files declared by the selected adapter.
// A fresh runtime directory is expected when the adapter changes.
func InitConfigForBackend(cfg *config.SysConfig, backend shared.BackendInfo) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if cfg.ConfigDir == "" {
		return fmt.Errorf("empty runtime config directory")
	}
	paths := layout.New(cfg.ConfigDir)
	if err := paths.Ensure(); err != nil {
		return err
	}
	if err := releaseBackendDefaults(paths, backend); err != nil {
		return err
	}
	_ = os.Chmod(paths.MainConfig(), 0600)
	return nil
}

func releaseBackendDefaults(paths layout.Paths, backend shared.BackendInfo) error {
	for _, name := range []string{"robot_config.ini", "robot_name_templates.json", "robot_shout_templates.json"} {
		data, err := defaultFiles.ReadFile("defaults/" + name)
		if err != nil {
			return err
		}
		dst, err := defaultReleasePath(paths, name)
		if err != nil {
			return err
		}
		if _, err := atomicfile.WriteFileIfMissing(dst, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func defaultReleasePath(paths layout.Paths, name string) (string, error) {
	switch name {
	case "robot_config.ini":
		return paths.RobotConfig(), nil
	case "robot_name_templates.json":
		return paths.NameTemplates(), nil
	case "robot_shout_templates.json":
		return paths.ShoutTemplates(), nil
	default:
		return "", fmt.Errorf("runtime default %q has no adapter destination", name)
	}
}
