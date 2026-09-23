package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestLoadBackendTownMapCatalogNativeUsesRuntimeFallback(t *testing.T) {
	maps, err := loadBackendTownMapCatalog(context.Background(), shared.BackendInfo{ID: shared.BackendNative}, &config.SysConfig{DFGameR: filepath.Join(t.TempDir(), "missing")})
	if err != nil || maps != nil {
		t.Fatalf("native catalog = %v, %v", maps, err)
	}
}

func TestS4A21PVFPathResolvesBesideExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := s4a21PVFPath(filepath.Join(dir, "DfoServer.exe"))
	if err != nil || got != path {
		t.Fatalf("path = %q, err = %v", got, err)
	}
}

func TestS4A21PVFPathResolvesInsideServerDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := s4a21PVFPath(dir)
	if err != nil || got != path {
		t.Fatalf("directory path = %q, err = %v", got, err)
	}
}

func TestS4A21DatabasePathResolvesBesideExecutable(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "Data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dataDir, "inventory.db")
	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := s4a21DatabasePath(filepath.Join(dir, "DfoServer.exe"))
	if err != nil || got != path {
		t.Fatalf("path = %q, err = %v", got, err)
	}
}
