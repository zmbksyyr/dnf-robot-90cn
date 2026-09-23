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
	"robot/internal/shared"
)

func TestSimulatorWebRejectsNativeCompatibilityOperations(t *testing.T) {
	s := New(&config.SysConfig{ConfigDir: t.TempDir()}, "", "", shared.BackendS4A21)
	for _, path := range []string{"/api/compat", "/api/party-compat"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			if path == "/api/compat" {
				s.handleCompat(rec, req)
			} else {
				s.handlePartyCompat(rec, req)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["ok"] != false || !strings.Contains(payload["error"].(string), shared.CodeBackendCapabilityUnsupported) {
				t.Fatalf("payload=%v", payload)
			}
		})
	}
}

func TestRecoveryWebRejectsNativeCompatibilityOperations(t *testing.T) {
	s := NewRecovery(&config.SysConfig{ConfigDir: t.TempDir()}, "", "", shared.BackendNative)
	req := httptest.NewRequest(http.MethodGet, "/api/compat", nil)
	rec := httptest.NewRecorder()
	s.handleCompat(rec, req)
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["ok"] != false || !strings.Contains(payload["error"].(string), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("payload=%v", payload)
	}
}

func TestSimulatorDiagnosticsSkipsNativeSections(t *testing.T) {
	s := New(&config.SysConfig{ConfigDir: t.TempDir()}, "127.0.0.1:1", "", shared.BackendS4A21)
	report := s.buildDiagnostics()
	for _, section := range report.Sections {
		switch section.Name {
		case "Database", "Market", "Party", "Skill":
			t.Fatalf("simulator diagnostics included native section %q", section.Name)
		}
	}
}

func TestWebAdminChildReceivesBackendIdentity(t *testing.T) {
	cmd := newCommand(&config.SysConfig{RobotPort: 8111, WebPort: 8112}, shared.BackendS4A21)
	if cmd == nil {
		t.Fatal("web admin child command is nil")
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "--backend-id sim_a21") {
		t.Fatalf("child args=%q", args)
	}
}

func TestSimulatorPVFPathAcceptsConfiguredDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	if err := os.WriteFile(path, []byte("pvf"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := simulatorPVFPath(dir); got != path {
		t.Fatalf("pvf path=%q, want %q", got, path)
	}
}
