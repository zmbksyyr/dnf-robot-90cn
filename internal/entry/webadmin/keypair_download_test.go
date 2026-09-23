package webadmin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestSimulatorKeypairDownloadIsUnsupported(t *testing.T) {
	s := newTestServerForBackend(&config.SysConfig{}, shared.BackendS4A21)
	rec := httptest.NewRecorder()
	s.handleKeypairDownload(rec, httptest.NewRequest("GET", "/api/keypair-download", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("status=%d body=%q", rec.Code, rec.Body.String())
	}
}
