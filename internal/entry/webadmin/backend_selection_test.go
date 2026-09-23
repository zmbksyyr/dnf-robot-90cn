package webadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestBackendSelectionDefaultsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	s := NewWithCatalog(&config.SysConfig{ConfigDir: dir}, "", "", shared.BackendNative, testBackendCatalog())
	req := httptest.NewRequest(http.MethodGet, "/api/backend", nil)
	rec := httptest.NewRecorder()
	s.handleBackend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got backendSelectionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Selected != shared.BackendNative || got.Persisted {
		t.Fatalf("payload = %+v", got)
	}
	if _, err := os.Stat(layout.New(dir).BackendSelection()); !os.IsNotExist(err) {
		t.Fatalf("GET must not create selection file: %v", err)
	}
}

func TestBackendCatalogDisablesPlatformUnsupportedBackends(t *testing.T) {
	windows := backendCatalogForPlatform(testBackendCatalog(), "windows")
	linux := backendCatalogForPlatform(testBackendCatalog(), "linux")
	find := func(backends []shared.BackendInfo, id shared.BackendID) shared.BackendInfo {
		for _, backend := range backends {
			if backend.ID == id {
				return backend
			}
		}
		t.Fatalf("backend %s missing", id)
		return shared.BackendInfo{}
	}
	if native := find(windows, shared.BackendNative); native.Selectable || native.Reason == "" {
		t.Fatalf("native backend must be disabled on Windows: %+v", native)
	}
	if simulator := find(windows, shared.BackendS4A21); !simulator.Selectable {
		t.Fatalf("S4A21 unexpectedly disabled on Windows: %+v", simulator)
	}
	if native := find(linux, shared.BackendNative); !native.Selectable {
		t.Fatalf("native unexpectedly disabled on Linux: %+v", native)
	}
}

func TestBackendSelectionRejectsUnsupportedPlatform(t *testing.T) {
	dir := t.TempDir()
	s := NewWithCatalog(&config.SysConfig{ConfigDir: dir}, "", "", shared.BackendNative, testBackendCatalog())
	req := httptest.NewRequest(http.MethodPost, "/api/backend", strings.NewReader(`{"backend_id":"missing"}`))
	rec := httptest.NewRecorder()
	s.handleBackend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got backendSelectionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || got.Error == "" {
		t.Fatalf("payload = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "backend_selection.json")); !os.IsNotExist(err) {
		t.Fatalf("rejected selection must not persist: %v", err)
	}
}

func TestBackendSelectionPersistsSimulatorAndRequestsReinitialize(t *testing.T) {
	dir := t.TempDir()
	s := NewWithCatalog(&config.SysConfig{ConfigDir: dir}, "", "", shared.BackendNative, testBackendCatalog())
	serverDir := filepath.Join(dir, "DfoServer")
	databasePath := filepath.Join(serverDir, "Data", "inventory.db")
	body, err := json.Marshal(map[string]interface{}{
		"backend_id": shared.BackendS4A21,
		"settings": map[string]string{
			"server_directory": serverDir,
			"server_host":      "127.0.0.1",
			"game_port":        "10011",
			"database_path":    databasePath,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/backend", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	s.handleBackend(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got backendSelectionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Selected != shared.BackendS4A21 || !got.Persisted || !got.RestartRequired || !got.ReinitializeNeeded || got.ConfigGeneration != 1 {
		t.Fatalf("payload = %+v", got)
	}
	var simulator *shared.BackendInfo
	for i := range got.Backends {
		if got.Backends[i].ID == shared.BackendS4A21 {
			simulator = &got.Backends[i]
			break
		}
	}
	if simulator == nil {
		t.Fatal("backend payload does not include S4A21")
	}
	for _, capability := range []shared.BackendCapability{shared.CapabilityTownMove, shared.CapabilityDungeonFollow, shared.CapabilityShout, shared.CapabilityCleanup, shared.CapabilityDatabase} {
		if !simulator.Supports(capability) {
			t.Fatalf("S4A21 capability %s unexpectedly disabled: %+v", capability, simulator.Capabilities[capability])
		}
	}
	if simulator.Capabilities[shared.CapabilityDungeonFollow].Mode != "toggle" {
		t.Fatalf("S4A21 dungeon follower mode is not adapter-declared: %+v", simulator.Capabilities[shared.CapabilityDungeonFollow])
	}
	if simulator.Capabilities[shared.CapabilityDatabase].Mode != "sqlite_health" {
		t.Fatalf("S4A21 database mode is not adapter-declared: %+v", simulator.Capabilities[shared.CapabilityDatabase])
	}
	for _, capability := range []shared.BackendCapability{shared.CapabilityDungeonMove, shared.CapabilityWorldShout, shared.CapabilityStore, shared.CapabilityParty, shared.CapabilitySkill, shared.CapabilityDangerousDelete, shared.CapabilityCompatibility, shared.CapabilityKeypair, shared.CapabilityDiagnostics, shared.CapabilitySystemAnnouncement} {
		status := simulator.Capabilities[capability]
		if status.Enabled || status.Reason == "" {
			t.Fatalf("S4A21 capability %s must be disabled with a reason: %+v", capability, status)
		}
	}
	data, err := os.ReadFile(layout.New(dir).BackendSelection())
	if err != nil {
		t.Fatal(err)
	}
	selection, err := shared.DecodeBackendSelection(data)
	if err != nil || selection.BackendID != shared.BackendS4A21 || selection.ConfigGeneration != 1 || selection.Settings["database_path"] != databasePath {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}

func TestBackendSelectionRejectsIncompleteSimulatorSettings(t *testing.T) {
	dir := t.TempDir()
	s := NewWithCatalog(&config.SysConfig{ConfigDir: dir}, "", "", shared.BackendNative, testBackendCatalog())
	req := httptest.NewRequest(http.MethodPost, "/api/backend", strings.NewReader(`{"backend_id":"sim_a21","settings":{"server_host":"127.0.0.1"}}`))
	rec := httptest.NewRecorder()
	s.handleBackend(rec, req)

	var got backendSelectionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK || !strings.Contains(got.Error, "Server directory") {
		t.Fatalf("payload=%+v", got)
	}
}

func TestBackendSelectionAcceptsDerivedSimulatorDatabase(t *testing.T) {
	dir := t.TempDir()
	s := NewWithCatalog(&config.SysConfig{ConfigDir: dir}, "", "", shared.BackendNative, testBackendCatalog())
	body, err := json.Marshal(map[string]interface{}{
		"backend_id": shared.BackendS4A21,
		"settings": map[string]string{
			"server_directory": filepath.Join(dir, "DfoServer"),
			"server_host":      "127.0.0.1",
			"game_port":        "10011",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/backend", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	s.handleBackend(rec, req)
	var got backendSelectionPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK || got.Settings["database_path"] != "" {
		t.Fatalf("payload=%+v", got)
	}
}
