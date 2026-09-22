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
	s := New(&config.SysConfig{ConfigDir: dir}, "", "")
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

func TestBackendSelectionRejectsUnsupportedPlatform(t *testing.T) {
	dir := t.TempDir()
	s := New(&config.SysConfig{ConfigDir: dir}, "", "")
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
	s := New(&config.SysConfig{ConfigDir: dir}, "", "")
	req := httptest.NewRequest(http.MethodPost, "/api/backend", strings.NewReader(`{"backend_id":"sim_a21"}`))
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
	for _, capability := range []shared.BackendCapability{shared.CapabilityTownMove, shared.CapabilityShout} {
		if !simulator.Supports(capability) {
			t.Fatalf("S4A21 capability %s unexpectedly disabled: %+v", capability, simulator.Capabilities[capability])
		}
	}
	for _, capability := range []shared.BackendCapability{shared.CapabilityDungeonMove, shared.CapabilityWorldShout, shared.CapabilityStore, shared.CapabilityParty, shared.CapabilitySkill} {
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
	if err != nil || selection.BackendID != shared.BackendS4A21 || selection.ConfigGeneration != 1 {
		t.Fatalf("selection=%+v err=%v", selection, err)
	}
}
