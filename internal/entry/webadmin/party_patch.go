package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"robot/internal/capability/catalog"
	"robot/internal/foundation/atomicfile"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

type partyCompatConfig struct {
	Enabled      bool   `json:"enabled"`
	AccountStart uint32 `json:"account_start"`
	AccountEnd   uint32 `json:"account_end"`
}

type partyCompatStatus struct {
	DesiredEnabled     bool   `json:"desired_enabled"`
	Enabled            bool   `json:"enabled"`
	SkillEnabled       bool   `json:"skill_enabled"`
	State              string `json:"state"`
	PID                int    `json:"pid,omitempty"`
	Port               int    `json:"port"`
	AccountStart       uint32 `json:"account_start"`
	AccountEnd         uint32 `json:"account_end"`
	Message            string `json:"message,omitempty"`
	FailCount          int    `json:"fail_count,omitempty"`
	NextRetrySec       int    `json:"next_retry_sec,omitempty"`
	processUnavailable bool
	orphanedCave       bool
}

type partyCompatRequest struct {
	Action       *string `json:"action"`
	AccountStart *uint32 `json:"account_start"`
	AccountEnd   *uint32 `json:"account_end"`
	SkillEnabled *bool   `json:"skill_enabled"`
}

func (s *Server) handlePartyCompat(w http.ResponseWriter, r *http.Request) {
	if s.nativeBackendOnly(w, shared.CapabilityParty) {
		return
	}
	s.partyCompatMu.Lock()
	defer s.partyCompatMu.Unlock()

	cfg, err := s.loadPartyCompatConfig()
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	switch r.Method {
	case http.MethodGet:
		status := s.inspectPartyCompatLocked(cfg)
		writeJSON(w, map[string]interface{}{"ok": true, "result": status})
	case http.MethodPost:
		var req partyCompatRequest
		if err := foundationconfig.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if req.Action == nil || req.AccountStart == nil || req.AccountEnd == nil || req.SkillEnabled == nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": "action, account_start, account_end, and skill_enabled are required"})
			return
		}
		if err := validatePartyCompatRange(*req.AccountStart, *req.AccountEnd); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		enable := false
		switch *req.Action {
		case "on":
			enable = true
		case "off":
		default:
			writeJSON(w, map[string]interface{}{"ok": false, "error": "action must be on or off"})
			return
		}
		cfg = partyCompatConfig{Enabled: enable, AccountStart: *req.AccountStart, AccountEnd: *req.AccountEnd}
		if err := s.savePartyCompatConfig(cfg); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		skillErr := s.savePartySkillEnabled(*req.SkillEnabled)
		actualSkillEnabled := s.loadPartySkillEnabled()
		partialFailure := skillErr != nil && actualSkillEnabled != *req.SkillEnabled
		skillMessage := ""
		if skillErr != nil {
			if partialFailure {
				skillMessage = "; partial persistence failure: " + skillErr.Error()
			} else {
				skillMessage = "; skill apply pending: " + skillErr.Error()
			}
		}
		s.resetPartyCompatFailuresLocked()
		s.wakePartyCompatSupervisor()
		status, applyErr := setPartyCompat(s.cfg.RobotGamePort, cfg, enable)
		status.DesiredEnabled = cfg.Enabled
		status.SkillEnabled = actualSkillEnabled
		if applyErr != nil {
			status = s.inspectPartyCompatLocked(cfg)
			status.SkillEnabled = actualSkillEnabled
			status.Message = "desired state saved; apply pending: " + applyErr.Error() + skillMessage
		} else {
			status.Message = "desired state saved and applied" + skillMessage
		}
		response := map[string]interface{}{"ok": !partialFailure, "result": status}
		if partialFailure {
			response["partial"] = true
			response["error"] = "party compatibility was saved but party skill configuration was not"
		}
		writeJSON(w, response)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) partySkillCatalogPath() string {
	return layout.New(s.cfg.ConfigDir).PartySkills()
}

func (s *Server) savePartySkillEnabled(enabled bool) error {
	if err := catalog.SetPartySkillCatalogEnabled(s.partySkillCatalogPath(), enabled); err != nil {
		return err
	}
	s.partySkillSnapshot.Store(&partySkillFileState{enabled: enabled})
	_, err := callRobot(s.robotAddr, "partySkillReload", nil, robotCallTimeout("partySkillReload"), s.cfg.MaxResponseBytes)
	return err
}

func (s *Server) reloadPartySkillFile(path string) error {
	report, err := catalog.ReadPartySkillCatalog(path)
	if err != nil {
		return err
	}
	if len(report.Issues) > 0 {
		return &catalog.PartySkillCatalogValidationError{Issues: report.Issues}
	}
	s.partySkillSnapshot.Store(&partySkillFileState{enabled: report.Enabled})
	foundationlog.Robotf("[WEB_RUNTIME_FILE] applied name=party_skills path=%s enabled=%t entries=%d\n", path, report.Enabled, len(report.Entries))
	return nil
}

func (s *Server) partyCompatConfigPath() string {
	return layout.New(s.cfg.ConfigDir).PartyCompatibility()
}

func (s *Server) loadPartyCompatConfig() (partyCompatConfig, error) {
	if current := s.partyCompatSnapshot.Load(); current != nil {
		return *current, nil
	}
	cfg, err := s.readPartyCompatConfig(s.partyCompatConfigPath())
	if err != nil {
		return partyCompatConfig{}, err
	}
	s.partyCompatSnapshot.Store(&cfg)
	return cfg, nil
}

func (s *Server) readPartyCompatConfig(path string) (partyCompatConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return partyCompatConfig{}, err
	}
	var raw struct {
		Enabled      *bool   `json:"enabled"`
		AccountStart *uint32 `json:"account_start"`
		AccountEnd   *uint32 `json:"account_end"`
	}
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return partyCompatConfig{}, fmt.Errorf("read party compatibility config: %w", err)
	}
	if raw.Enabled == nil || raw.AccountStart == nil || raw.AccountEnd == nil {
		return partyCompatConfig{}, fmt.Errorf("read party compatibility config: enabled, account_start, and account_end are required")
	}
	cfg := partyCompatConfig{Enabled: *raw.Enabled, AccountStart: *raw.AccountStart, AccountEnd: *raw.AccountEnd}
	if err := validatePartyCompatRange(cfg.AccountStart, cfg.AccountEnd); err != nil {
		return cfg, fmt.Errorf("read party compatibility config: %w", err)
	}
	return cfg, nil
}

func (s *Server) savePartyCompatConfig(cfg partyCompatConfig) error {
	path := s.partyCompatConfigPath()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return err
	}
	s.partyCompatSnapshot.Store(&cfg)
	s.wakePartyCompatSupervisor()
	return nil
}

func (s *Server) reloadPartyCompatFile(path string) error {
	cfg, err := s.readPartyCompatConfig(path)
	if err != nil {
		return err
	}
	if current := s.partyCompatSnapshot.Load(); current != nil && *current == cfg {
		return nil
	}
	s.partyCompatSnapshot.Store(&cfg)
	s.partyCompatMu.Lock()
	s.resetPartyCompatFailuresLocked()
	s.partyCompatMu.Unlock()
	s.wakePartyCompatSupervisor()
	foundationlog.Robotf("[WEB_RUNTIME_FILE] applied name=party_compatibility path=%s enabled=%t range=%d..%d\n", path, cfg.Enabled, cfg.AccountStart, cfg.AccountEnd)
	return nil
}

func (s *Server) inspectPartyCompatLocked(cfg partyCompatConfig) partyCompatStatus {
	status := inspectPartyCompat(s.cfg.RobotGamePort, cfg)
	status.DesiredEnabled = cfg.Enabled
	status.SkillEnabled = s.loadPartySkillEnabled()
	status.FailCount = s.partyCompatFailures
	if !s.partyCompatNextRetry.IsZero() {
		next := int(time.Until(s.partyCompatNextRetry).Round(time.Second) / time.Second)
		if next > 0 {
			status.NextRetrySec = next
		}
	}
	if status.Message == "" && s.partyCompatLastError != "" {
		status.Message = s.partyCompatLastError
	}
	if cfg.Enabled && status.processUnavailable {
		status.Message = partyCompatWaitingMessage(status.Message)
	}
	return status
}

func (s *Server) loadPartySkillEnabled() bool {
	if s == nil || s.cfg == nil || s.cfg.ConfigDir == "" {
		return false
	}
	if current := s.partySkillSnapshot.Load(); current != nil {
		return current.enabled
	}
	report, err := catalog.ReadPartySkillCatalog(s.partySkillCatalogPath())
	if err != nil || len(report.Issues) > 0 {
		return false
	}
	s.partySkillSnapshot.Store(&partySkillFileState{enabled: report.Enabled})
	return report.Enabled
}

func validatePartyCompatRange(start, end uint32) error {
	if start == 0 || end == 0 || start >= end {
		return fmt.Errorf("account range must be positive and start must be less than end")
	}
	return nil
}
