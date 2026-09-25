package webadmin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestSimulatorWebRejectsNativeCompatibilityOperations(t *testing.T) {
	s := newTestServerForBackend(&config.SysConfig{ConfigDir: t.TempDir()}, shared.BackendS4A21)
	for _, path := range []string{"/api/compat", "/api/party-compat", "/api/max-user", "/api/server-script", "/api/service-ports", "/api/monitor-service", "/api/relay-service", "/api/diagnostics"} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			switch path {
			case "/api/compat":
				s.handleCompat(rec, req)
			case "/api/party-compat":
				s.handlePartyCompat(rec, req)
			case "/api/max-user":
				s.handleMaxUser(rec, req)
			case "/api/server-script":
				s.handleServerScript(rec, req)
			case "/api/service-ports":
				s.handleServicePorts(rec, req)
			case "/api/monitor-service":
				s.handleMonitorService(rec, req)
			case "/api/relay-service":
				s.handleRelayService(rec, req)
			case "/api/diagnostics":
				s.handleDiagnostics(rec, req)
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
	s := NewRecovery(&config.SysConfig{ConfigDir: t.TempDir()}, "", "", shared.BackendS4A21)
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
