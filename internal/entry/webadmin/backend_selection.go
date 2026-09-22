package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

type backendSelectionState = shared.BackendSelection

type backendSelectionPayload struct {
	OK                 bool                 `json:"ok"`
	Selected           shared.BackendID     `json:"selected"`
	ConfigGeneration   uint64               `json:"config_generation"`
	SelectedAt         time.Time            `json:"selected_at,omitempty"`
	Persisted          bool                 `json:"persisted"`
	RestartRequired    bool                 `json:"restart_required"`
	ReinitializeNeeded bool                 `json:"reinitialize_needed"`
	Platform           string               `json:"platform"`
	Backends           []shared.BackendInfo `json:"backends"`
	Error              string               `json:"error,omitempty"`
	Message            string               `json:"message,omitempty"`
}

func (s *Server) handleBackend(w http.ResponseWriter, r *http.Request) {
	s.backendSelectionMu.Lock()
	defer s.backendSelectionMu.Unlock()
	state, persisted, err := s.readBackendSelectionLocked()
	if err != nil {
		writeJSON(w, backendSelectionPayload{OK: false, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Error: err.Error()})
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, backendSelectionResponse(state, persisted, ""))
	case http.MethodPost:
		var req struct {
			BackendID shared.BackendID `json:"backend_id"`
		}
		if err := config.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, backendSelectionPayload{OK: false, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Error: err.Error()})
			return
		}
		info, err := shared.SelectBackend(req.BackendID, runtime.GOOS)
		if err != nil {
			writeJSON(w, backendSelectionPayload{OK: false, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Error: err.Error()})
			return
		}
		if state.BackendID == info.ID {
			writeJSON(w, backendSelectionResponse(state, persisted, "backend is already selected"))
			return
		}
		state.BackendID = info.ID
		state.ConfigGeneration++
		state.SelectedAt = time.Now().UTC()
		if err := s.writeBackendSelectionLocked(state); err != nil {
			writeJSON(w, backendSelectionPayload{OK: false, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Error: err.Error()})
			return
		}
		payload := backendSelectionResponse(state, true, "backend selected; stop robot, back up and reinitialize runtime/config before restart")
		payload.RestartRequired = true
		payload.ReinitializeNeeded = true
		writeJSON(w, payload)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func backendSelectionResponse(state backendSelectionState, persisted bool, message string) backendSelectionPayload {
	return backendSelectionPayload{OK: true, Selected: state.BackendID, ConfigGeneration: state.ConfigGeneration, SelectedAt: state.SelectedAt, Persisted: persisted, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Message: message}
}

func backendCatalogForPlatform(platform string) []shared.BackendInfo {
	backends := shared.KnownBackends()
	for i := range backends {
		if !backends[i].Selectable {
			continue
		}
		supported := false
		for _, candidate := range backends[i].SupportedOS {
			if candidate == platform {
				supported = true
				break
			}
		}
		if !supported {
			backends[i].Selectable = false
			backends[i].Reason = fmt.Sprintf("backend does not support %s", platform)
		}
	}
	return backends
}

func (s *Server) readBackendSelectionLocked() (backendSelectionState, bool, error) {
	defaultState := backendSelectionState{BackendID: shared.BackendNative}
	path := layout.New(s.cfg.ConfigDir).BackendSelection()
	if strings.TrimSpace(path) == "" {
		return defaultState, false, fmt.Errorf("backend selection path is unavailable")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return defaultState, false, nil
	}
	if err != nil {
		return defaultState, false, err
	}
	selection, err := shared.DecodeBackendSelection(data)
	if err != nil {
		return defaultState, true, err
	}
	return selection, true, nil
}

func (s *Server) writeBackendSelectionLocked(state backendSelectionState) error {
	path := layout.New(s.cfg.ConfigDir).BackendSelection()
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'), 0600)
}
