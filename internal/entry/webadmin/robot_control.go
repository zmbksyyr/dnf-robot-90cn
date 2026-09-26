package webadmin

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"robot/internal/capability/robotconfig"
	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
)

func (s *Server) handleGamePort(w http.ResponseWriter, _ *http.Request) {
	cfg := s.cfg
	if cfg == nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": "robot configuration is unavailable"})
		return
	}
	writeJSON(w, map[string]interface{}{
		"ok":           true,
		"addr":         net.JoinHostPort(cfg.RobotConnectIP, strconv.Itoa(cfg.RobotGamePort)),
		"game_port":    cfg.RobotGamePort,
		"max_user_num": 600,
	})
}

func (s *Server) handleGameEndpoint(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.loadDiskConfig()
		if err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, s.gameEndpointPayload(cfg, ""))
	case http.MethodPost:
		var req struct {
			GamePort int `json:"game_port"`
		}
		if err := config.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if err := validateExternalPorts(req.GamePort); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		cfg, err := s.writeExternalPort(req.GamePort)
		if err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		payload := s.gameEndpointPayload(cfg, "saved; restart robot to apply")
		payload["restart_required"] = true
		writeJSON(w, payload)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleRestartRobot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	exe, err := os.Executable()
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if err := startRobotRestartHelper(exe, s.cfg.ConfigDir); err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "message": "robot restart started", "exe": exe})
}

func (s *Server) gameEndpointPayload(cfg *config.SysConfig, message string) map[string]interface{} {
	connectIP := ""
	addr := ""
	connectSetting := ""
	innerIP := ""
	if cfg != nil {
		connectIP = cfg.RobotConnectIP
		addr = net.JoinHostPort(connectIP, strconv.Itoa(cfg.RobotGamePort))
		connectSetting = cfg.RobotConnectIPSetting
		innerIP = cfg.RobotInnerIP
	}
	out := map[string]interface{}{
		"ok":               true,
		"connect_ip":       connectIP,
		"game_port":        cfg.RobotGamePort,
		"ports":            map[string]int{"game": cfg.RobotGamePort},
		"connect_setting":  connectSetting,
		"connect_resolved": connectIP,
		"inner_ip":         innerIP,
		"addr":             addr,
		"config_path":      s.configPath(),
	}
	delete(out, "inner_ip")
	if s != nil && s.cfg != nil && cfg != nil {
		fields := restartConfigDiff(s.cfg, cfg)
		out["restart_required"] = len(fields) > 0
		out["restart_fields"] = fields
		out["running_config"] = restartConfigView(s.cfg)
		out["disk_config"] = restartConfigView(cfg)
	}
	if message != "" {
		out["message"] = message
	}
	return out
}

func restartConfigView(cfg *config.SysConfig) map[string]interface{} {
	if cfg == nil {
		return nil
	}
	return map[string]interface{}{
		"robot_port": cfg.RobotPort, "web_port": cfg.WebPort,
		"game_port": cfg.RobotGamePort, "party_route0_port": cfg.PartyRoute0Port,
		"server_directory": cfg.ServerDirectory,
		"connect_ip":       cfg.RobotConnectIP, "connect_setting": cfg.RobotConnectIPSetting, "inner_ip": cfg.RobotInnerIP,
		"web_password_set": cfg.WebPassword != "",
		"log_max_size_mb":  cfg.LogMaxSizeMB, "log_max_backups": cfg.LogMaxBackups,
		"max_response_bytes": cfg.MaxResponseBytes,
	}
}

func restartConfigDiff(running, disk *config.SysConfig) []string {
	if running == nil || disk == nil {
		return nil
	}
	var fields []string
	checks := []struct {
		name      string
		different bool
	}{
		{"robot_port", running.RobotPort != disk.RobotPort}, {"web_port", running.WebPort != disk.WebPort},
		{"game_port", running.RobotGamePort != disk.RobotGamePort}, {"party_route0_port", running.PartyRoute0Port != disk.PartyRoute0Port},
		{"server_directory", running.ServerDirectory != disk.ServerDirectory},
		{"robot_connect_ip", running.RobotConnectIP != disk.RobotConnectIP}, {"robot_inner_ip", running.RobotInnerIP != disk.RobotInnerIP},
		{"robot_connect_setting", running.RobotConnectIPSetting != disk.RobotConnectIPSetting},
		{"web_password", running.WebPassword != disk.WebPassword},
		{"log_max_size_mb", running.LogMaxSizeMB != disk.LogMaxSizeMB}, {"log_max_backups", running.LogMaxBackups != disk.LogMaxBackups},
		{"max_response_bytes", running.MaxResponseBytes != disk.MaxResponseBytes},
	}
	for _, check := range checks {
		if check.different {
			fields = append(fields, check.name)
		}
	}
	return fields
}

func (s *Server) loadDiskConfig() (*config.SysConfig, error) {
	cfg, err := config.LoadConfig(s.configPath())
	if err != nil {
		return nil, err
	}
	cfg.ConfigDir = s.cfg.ConfigDir
	return cfg, nil
}

func (s *Server) writeExternalPort(game int) (*config.SysConfig, error) {
	path := s.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := robotconfig.UpdateINIText(string(data), map[string]string{
		"Ports.Game": strconv.Itoa(game),
	})
	cfg, err := config.ParseConfig(text)
	if err != nil {
		return nil, err
	}
	if err := atomicfile.WriteFile(path, []byte(text), 0600); err != nil {
		return nil, err
	}
	cfg.ConfigDir = s.cfg.ConfigDir
	return cfg, nil
}

func validateExternalPorts(ports ...int) error {
	for _, port := range ports {
		if port <= 0 || port > 65535 {
			return fmt.Errorf("ports must be between 1 and 65535")
		}
	}
	return nil
}

func (s *Server) configPath() string {
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.ConfigDir) == "" {
		return ""
	}
	return layout.New(s.cfg.ConfigDir).MainConfig()
}

func buildRobotRestartScript(exe, configDir string) string {
	paths := layout.New(configDir)
	logPath := paths.StdoutLog()
	errPath := paths.StartErrorLog()
	workDir := filepath.Dir(exe)
	return fmt.Sprintf(`(
sleep 1
exe=%s
stop_robot_processes() {
  signal=$1
  for d in /proc/[0-9]*; do
    pid=${d#/proc/}
    target=$(readlink "$d/exe" 2>/dev/null || true)
    [ "$target" = "$exe" ] || continue
    mode=$(tr '\000' '\n' < "$d/cmdline" 2>/dev/null | sed -n '2p')
    if [ -z "$mode" ]; then
      kill "-$signal" "$pid" 2>/dev/null || true
    fi
  done
}
stop_robot_processes TERM
sleep 2
stop_robot_processes KILL
cd %s || exit 1
nohup "$exe" >>%s 2>>%s < /dev/null &
) >/dev/null 2>&1 &`, shellQuote(exe), shellQuote(workDir), shellQuote(logPath), shellQuote(errPath))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
