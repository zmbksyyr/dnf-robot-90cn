package webadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"robot/internal/foundation/atomicfile"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

type mailboxGuardConfig struct {
	Enabled bool `json:"mailbox_bad_node_guard"`
}

type mailboxGuardRequest struct {
	Enabled *bool `json:"mailbox_bad_node_guard"`
}

type mailboxGuardStatus struct {
	DesiredEnabled bool   `json:"desired_enabled"`
	Enabled        bool   `json:"enabled"`
	State          string `json:"state"`
	PID            int    `json:"pid,omitempty"`
	Port           int    `json:"port"`
	Message        string `json:"message,omitempty"`
}

func (s *Server) handleCompat(w http.ResponseWriter, r *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilityCompatibility) {
		return
	}
	// Serialize all df_game_r compatibility inspection and patch operations.
	s.partyCompatMu.Lock()
	defer s.partyCompatMu.Unlock()

	cfg, err := s.loadMailboxGuardConfig()
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}

	switch r.Method {
	case http.MethodGet:
		status := inspectMailboxGuard(s.cfg.RobotGamePort)
		status.DesiredEnabled = cfg.Enabled
		writeJSON(w, map[string]interface{}{"ok": true, "result": status})
	case http.MethodPost:
		var req mailboxGuardRequest
		if err := foundationconfig.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if req.Enabled == nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": "mailbox_bad_node_guard is required"})
			return
		}
		cfg.Enabled = *req.Enabled
		if err := s.saveMailboxGuardConfig(cfg); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		status, applyErr := setMailboxGuard(s.cfg.RobotGamePort, cfg.Enabled)
		status.DesiredEnabled = cfg.Enabled
		message := "desired state saved and applied"
		if applyErr != nil {
			status = inspectMailboxGuard(s.cfg.RobotGamePort)
			status.DesiredEnabled = cfg.Enabled
			status.Message = "desired state saved; apply pending: " + applyErr.Error()
			message = status.Message
		} else {
			status.Message = message
		}
		writeJSON(w, map[string]interface{}{
			"ok":               true,
			"restart_required": false,
			"message":          message,
			"result":           status,
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) mailboxGuardConfigPath() string {
	return layout.New(s.cfg.ConfigDir).MailboxGuard()
}

func (s *Server) loadMailboxGuardConfig() (mailboxGuardConfig, error) {
	if current := s.mailboxGuardSnapshot.Load(); current != nil {
		return *current, nil
	}
	cfg, err := s.readMailboxGuardConfig(s.mailboxGuardConfigPath())
	if err != nil {
		return mailboxGuardConfig{}, err
	}
	s.mailboxGuardSnapshot.Store(&cfg)
	return cfg, nil
}

func (s *Server) readMailboxGuardConfig(path string) (mailboxGuardConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mailboxGuardConfig{}, err
	}
	var raw struct {
		Enabled *bool `json:"mailbox_bad_node_guard"`
	}
	if err := foundationconfig.DecodeJSONBytes(data, &raw); err != nil {
		return mailboxGuardConfig{}, fmt.Errorf("read compatibility config: %w", err)
	}
	if raw.Enabled == nil {
		return mailboxGuardConfig{}, fmt.Errorf("read compatibility config: missing mailbox_bad_node_guard")
	}
	return mailboxGuardConfig{Enabled: *raw.Enabled}, nil
}

func (s *Server) saveMailboxGuardConfig(cfg mailboxGuardConfig) error {
	path := s.mailboxGuardConfigPath()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicfile.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return err
	}
	s.mailboxGuardSnapshot.Store(&cfg)
	s.wakeMailboxGuardSupervisor()
	return nil
}

func (s *Server) reloadMailboxGuardFile(path string) error {
	cfg, err := s.readMailboxGuardConfig(path)
	if err != nil {
		return err
	}
	if current := s.mailboxGuardSnapshot.Load(); current != nil && *current == cfg {
		return nil
	}
	s.mailboxGuardSnapshot.Store(&cfg)
	s.wakeMailboxGuardSupervisor()
	foundationlog.Robotf("[WEB_RUNTIME_FILE] applied name=mailbox_guard path=%s enabled=%t\n", path, cfg.Enabled)
	return nil
}
