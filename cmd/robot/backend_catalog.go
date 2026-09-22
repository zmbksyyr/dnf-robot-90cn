package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func loadBackendTownMapCatalog(ctx context.Context, info shared.BackendInfo, cfg *config.SysConfig) ([]shared.MapCatalogItem, error) {
	if cfg == nil {
		return nil, fmt.Errorf("backend catalog requires config")
	}
	switch info.ID {
	case shared.BackendNative:
		return nil, nil
	case shared.BackendS4A21:
		path, err := s4a21PVFPath(cfg.DFGameR)
		if err != nil {
			return nil, err
		}
		return (s4a21backend.TownMapCatalogProvider{PVFPath: path}).TownMapCatalog(ctx)
	default:
		return nil, fmt.Errorf("backend %s has no town map catalog provider", info.ID)
	}
}

func s4a21PVFPath(dfGameR string) (string, error) {
	value := strings.TrimSpace(dfGameR)
	if value == "" {
		return "", fmt.Errorf("S4A21 PVF path cannot be resolved from empty DfGameR")
	}
	if strings.EqualFold(filepath.Ext(value), ".pvf") {
		if _, err := os.Stat(value); err != nil {
			return "", fmt.Errorf("S4A21 PVF %q: %w", value, err)
		}
		return value, nil
	}
	candidate := filepath.Join(filepath.Dir(value), "Script.pvf")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("S4A21 PVF %q: %w", candidate, err)
	}
	return candidate, nil
}
