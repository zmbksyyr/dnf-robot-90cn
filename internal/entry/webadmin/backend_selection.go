package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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
	Settings           map[string]string    `json:"settings,omitempty"`
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
		state.Settings = s.backendSettingsWithDefaults(state)
		writeJSON(w, backendSelectionResponse(state, persisted, ""))
	case http.MethodPost:
		var req struct {
			BackendID shared.BackendID  `json:"backend_id"`
			Settings  map[string]string `json:"settings"`
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
		settings, err := validateBackendSettings(info, req.Settings)
		if err != nil {
			writeJSON(w, backendSelectionPayload{OK: false, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Error: err.Error()})
			return
		}
		if state.BackendID == info.ID && equalStringMap(state.Settings, settings) {
			state.Settings = settings
			writeJSON(w, backendSelectionResponse(state, persisted, "backend is already selected"))
			return
		}
		state.BackendID = info.ID
		state.Settings = settings
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
	return backendSelectionPayload{OK: true, Selected: state.BackendID, ConfigGeneration: state.ConfigGeneration, SelectedAt: state.SelectedAt, Persisted: persisted, Platform: runtime.GOOS, Backends: backendCatalogForPlatform(runtime.GOOS), Message: message, Settings: state.Settings}
}

func (s *Server) backendSettingsWithDefaults(state backendSelectionState) map[string]string {
	settings := make(map[string]string)
	for key, value := range state.Settings {
		settings[key] = value
	}
	if s.cfg == nil {
		return settings
	}
	var selected shared.BackendInfo
	for _, info := range shared.KnownBackends() {
		if info.ID == state.BackendID {
			selected = info
			break
		}
	}
	for _, field := range selected.Settings {
		if settings[field.Key] != "" {
			continue
		}
		value := s.runtimeSettingDefault(field.RuntimeSource)
		if value == "" && field.DerivedFrom != "" && settings[field.DerivedFrom] != "" {
			value = filepath.Join(append([]string{settings[field.DerivedFrom]}, field.PathSuffix...)...)
		}
		if value == "" {
			value = field.Default
		}
		if value != "" {
			settings[field.Key] = value
		}
	}
	return settings
}

func (s *Server) runtimeSettingDefault(source string) string {
	switch source {
	case "server_directory":
		value := strings.TrimSpace(s.cfg.DFGameR)
		if filepath.Ext(value) != "" {
			value = filepath.Dir(value)
		}
		return value
	case "game_host":
		return strings.TrimSpace(s.cfg.RobotConnectIP)
	case "game_port":
		if s.cfg.RobotGamePort > 0 {
			return strconv.Itoa(s.cfg.RobotGamePort)
		}
	}
	return ""
}

func validateBackendSettings(info shared.BackendInfo, input map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(info.Settings))
	for _, field := range info.Settings {
		value := strings.TrimSpace(input[field.Key])
		if value == "" {
			value = field.Default
		}
		if field.Required && value == "" {
			return nil, fmt.Errorf("%s is required", field.Label)
		}
		if field.InputType == "number" && value != "" {
			port, err := strconv.Atoi(value)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("%s must be between 1 and 65535", field.Label)
			}
		}
		if field.InputType == "path" && value != "" && !filepath.IsAbs(value) {
			return nil, fmt.Errorf("%s must be an absolute path", field.Label)
		}
		if value != "" {
			result[field.Key] = value
		}
	}
	return result, nil
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
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
