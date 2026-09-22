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
