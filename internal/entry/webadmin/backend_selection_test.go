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
