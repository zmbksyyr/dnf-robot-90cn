package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	capabilitypvf "robot/internal/capability/pvf"
	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func loadBackendTownMapCatalog(ctx context.Context, _ shared.BackendInfo, cfg *config.SysConfig) ([]shared.MapCatalogItem, error) {
	if cfg == nil {
		return nil, fmt.Errorf("backend catalog requires config")
	}
	path, err := s4a21PVFPath(cfg.ServerDirectory)
	if err != nil {
		return nil, err
	}
	return (s4a21backend.TownMapCatalogProvider{PVFPath: path}).TownMapCatalog(ctx)
}

func exportBackendItemCatalogs(_ shared.BackendInfo, cfg *config.SysConfig, paths layout.Paths) error {
	pvfPath, err := s4a21PVFPath(cfg.ServerDirectory)
	if err != nil {
		return err
	}
	equipment, stackable, err := s4a21backend.ReadItemCatalogs(pvfPath)
	if err != nil {
		return err
	}
	return exportItemCatalogs(paths, equipment, stackable)
}

func exportItemCatalogs(paths layout.Paths, equipment, stackable []shared.EquipmentCatalogItem) error {
	if err := capabilitypvf.WriteJSON(paths.PVFEquipment(), equipment); err != nil {
		return fmt.Errorf("write S4A21 equipment catalog: %w", err)
	}
	if err := capabilitypvf.WriteJSON(paths.PVFStackable(), stackable); err != nil {
		return fmt.Errorf("write S4A21 stackable catalog: %w", err)
	}
	return nil
}

func s4a21PVFPath(serverDirectory string) (string, error) {
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("S4A21 PVF path cannot be resolved from empty ServerDirectory")
	}
	if strings.EqualFold(filepath.Ext(value), ".pvf") {
		if _, err := os.Stat(value); err != nil {
			return "", fmt.Errorf("S4A21 PVF %q: %w", value, err)
		}
		return value, nil
	}
	// Some adapter bundles configure ServerDirectory as the server executable,
	// while Linux-oriented bundles may configure it as the server directory.
	// Resolve both forms without making backend selection implicit.
	base := filepath.Dir(value)
	if stat, err := os.Stat(value); err == nil && stat.IsDir() {
		base = value
	}
	candidate := filepath.Join(base, "Script.pvf")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("S4A21 PVF %q: %w", candidate, err)
	}
	return candidate, nil
}

func s4a21DatabasePath(serverDirectory, configured string) (string, error) {
	if value := strings.TrimSpace(configured); value != "" {
		if _, err := os.Stat(value); err != nil {
			return "", fmt.Errorf("S4A21 database %q: %w", value, err)
		}
		return value, nil
	}
	value := strings.TrimSpace(serverDirectory)
	if value == "" {
		return "", fmt.Errorf("S4A21 database path cannot be resolved from empty ServerDirectory")
	}
	base := value
	if stat, err := os.Stat(value); err == nil && !stat.IsDir() {
		base = filepath.Dir(value)
	} else if filepath.Ext(value) != "" {
		base = filepath.Dir(value)
	}
	candidate := filepath.Join(base, "Data", "inventory.db")
	if _, err := os.Stat(candidate); err != nil {
		return "", fmt.Errorf("S4A21 database %q: %w", candidate, err)
	}
	return candidate, nil
}
