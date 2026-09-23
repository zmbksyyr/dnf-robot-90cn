package webadmin

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"robot/internal/capability/robotconfig"
	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

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
			GamePort    int `json:"game_port"`
			MonitorPort int `json:"monitor_port"`
			AuctionPort int `json:"auction_port"`
			PointPort   int `json:"point_port"`
			RelayPort   int `json:"relay_port"`
		}
		if err := config.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if !s.supportsBackendCapability(shared.CapabilityServiceControl) {
			disk, err := s.loadDiskConfig()
			if err != nil {
				writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
				return
			}
			req.MonitorPort = disk.MonitorPort
			req.AuctionPort = disk.AuctionPort
			req.PointPort = disk.PointPort
			req.RelayPort = disk.RelayPort
		}
		if err := validateExternalPorts(req.GamePort, req.MonitorPort, req.AuctionPort, req.PointPort, req.RelayPort); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		cfg, err := s.writeExternalPorts(req.GamePort, req.MonitorPort, req.AuctionPort, req.PointPort, req.RelayPort)
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
	writeJSON(w, map[string]interface{}{"ok": true, "message": "robot restart queued", "exe": exe})
}

func (s *Server) gameEndpointPayload(cfg *config.SysConfig, message string) map[string]interface{} {
	connectIP := ""
	addr := ""
	ports := map[string]int{}
	hosts := map[string]string{}
	serviceRoot := ""
	runScript := ""
	connectSetting := ""
	innerIP := ""
	if cfg != nil {
		connectIP = cfg.RobotConnectIP
		addr = net.JoinHostPort(connectIP, strconv.Itoa(cfg.RobotGamePort))
		ports = map[string]int{
			"game":    cfg.RobotGamePort,
			"monitor": cfg.MonitorPort,
			"auction": cfg.AuctionPort,
			"point":   cfg.PointPort,
			"relay":   cfg.RelayPort,
		}
		hosts = map[string]string{"auction": cfg.AuctionHost, "point": cfg.PointHost, "relay": cfg.RelayHost}
		serviceRoot = cfg.ServiceRoot
		runScript = cfg.ServiceRunScript
		connectSetting = cfg.RobotConnectIPSetting
		innerIP = cfg.RobotInnerIP
	}
	out := map[string]interface{}{
		"ok":               true,
		"connect_ip":       connectIP,
		"game_port":        ports["game"],
		"ports":            ports,
		"hosts":            hosts,
		"service_root":     serviceRoot,
		"run_script":       runScript,
		"connect_setting":  connectSetting,
		"connect_resolved": connectIP,
		"inner_ip":         innerIP,
		"addr":             addr,
		"config_path":      s.configPath(),
	}
	if !s.supportsBackendCapability(shared.CapabilityServiceControl) {
		out["ports"] = map[string]int{"game": ports["game"]}
		delete(out, "hosts")
		delete(out, "service_root")
		delete(out, "run_script")
		delete(out, "inner_ip")
	}
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
		"game_port": cfg.RobotGamePort, "monitor_port": cfg.MonitorPort,
		"auction_port": cfg.AuctionPort, "point_port": cfg.PointPort,
		"relay_port": cfg.RelayPort, "party_route0_port": cfg.PartyRoute0Port,
		"df_game_r": cfg.DFGameR, "game_server_group": cfg.GameServerGroup,
		"connect_ip": cfg.RobotConnectIP, "connect_setting": cfg.RobotConnectIPSetting, "inner_ip": cfg.RobotInnerIP,
		"service_root": cfg.ServiceRoot, "service_run_script": cfg.ServiceRunScript,
		"auction_host": cfg.AuctionHost, "point_host": cfg.PointHost, "relay_host": cfg.RelayHost,
		"database_host": cfg.DBHost, "database_port": cfg.DBPort,
		"database_name": cfg.DBName, "database_user": cfg.DBUser,
		"database_init_size": cfg.DBInitSize, "database_max_size": cfg.DBMaxSize,
		"database_dial_timeout_sec": cfg.DBDialTimeoutSec, "database_read_timeout_sec": cfg.DBReadTimeoutSec,
		"database_write_timeout_sec": cfg.DBWriteTimeoutSec, "database_conn_max_lifetime_sec": cfg.DBConnMaxLifetimeSec,
		"database_password_set": cfg.DBPassword != "",
		"web_password_set":      cfg.WebPassword != "",
		"log_max_size_mb":       cfg.LogMaxSizeMB, "log_max_backups": cfg.LogMaxBackups,
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
		{"game_port", running.RobotGamePort != disk.RobotGamePort}, {"monitor_port", running.MonitorPort != disk.MonitorPort},
		{"auction_port", running.AuctionPort != disk.AuctionPort}, {"point_port", running.PointPort != disk.PointPort},
		{"relay_port", running.RelayPort != disk.RelayPort}, {"party_route0_port", running.PartyRoute0Port != disk.PartyRoute0Port},
		{"df_game_r", running.DFGameR != disk.DFGameR}, {"game_server_group", running.GameServerGroup != disk.GameServerGroup},
		{"robot_connect_ip", running.RobotConnectIP != disk.RobotConnectIP}, {"robot_inner_ip", running.RobotInnerIP != disk.RobotInnerIP},
		{"robot_connect_setting", running.RobotConnectIPSetting != disk.RobotConnectIPSetting},
		{"service_root", running.ServiceRoot != disk.ServiceRoot}, {"service_run_script", running.ServiceRunScript != disk.ServiceRunScript},
		{"auction_host", running.AuctionHost != disk.AuctionHost}, {"point_host", running.PointHost != disk.PointHost}, {"relay_host", running.RelayHost != disk.RelayHost},
		{"database_host", running.DBHost != disk.DBHost}, {"database_port", running.DBPort != disk.DBPort},
		{"database_name", running.DBName != disk.DBName}, {"database_user", running.DBUser != disk.DBUser},
		{"database_password", running.DBPassword != disk.DBPassword}, {"web_password", running.WebPassword != disk.WebPassword},
		{"database_init_size", running.DBInitSize != disk.DBInitSize}, {"database_max_size", running.DBMaxSize != disk.DBMaxSize},
		{"database_dial_timeout_sec", running.DBDialTimeoutSec != disk.DBDialTimeoutSec},
		{"database_read_timeout_sec", running.DBReadTimeoutSec != disk.DBReadTimeoutSec},
		{"database_write_timeout_sec", running.DBWriteTimeoutSec != disk.DBWriteTimeoutSec},
		{"database_conn_max_lifetime_sec", running.DBConnMaxLifetimeSec != disk.DBConnMaxLifetimeSec},
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

func (s *Server) writeExternalPorts(game, monitor, auction, point, relay int) (*config.SysConfig, error) {
	path := s.configPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := robotconfig.UpdateINIText(string(data), map[string]string{
		"Ports.Game":    strconv.Itoa(game),
		"Ports.Monitor": strconv.Itoa(monitor),
		"Ports.Auction": strconv.Itoa(auction),
		"Ports.Point":   strconv.Itoa(point),
		"Ports.Relay":   strconv.Itoa(relay),
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

func startRobotRestartHelper(exe, configDir string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("restart robot is only supported on linux")
	}
	if strings.TrimSpace(exe) == "" {
		return fmt.Errorf("empty executable path")
	}
	cmd := exec.Command("/bin/sh", "-c", buildRobotRestartScript(exe, configDir))
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func buildRobotRestartScript(exe, configDir string) string {
	paths := layout.New(configDir)
	logPath := paths.StdoutLog()
	errPath := paths.StartErrorLog()
	workDir := filepath.Dir(exe)
	return fmt.Sprintf(`(
sleep 1
exe=%s
log_path=%s
stop_robot_processes() {
  signal=$1
  for d in /proc/[0-9]*; do
    pid=${d#/proc/}
    target=$(readlink "$d/exe" 2>/dev/null || true)
    [ "$target" = "$exe" ] || continue
    mode=$(tr '\000' '\n' < "$d/cmdline" 2>/dev/null | sed -n '2p')
    sink=$(tr '\000' '\n' < "$d/cmdline" 2>/dev/null | sed -n '3p')
    if [ -z "$mode" ] || [ "$mode" = "--web-admin" ] || { [ "$mode" = "--bounded-log-sink" ] && [ "$sink" = "$log_path" ]; }; then
      kill "-$signal" "$pid" 2>/dev/null || true
    fi
  done
}
stop_robot_processes TERM
sleep 2
stop_robot_processes KILL
cd %s || exit 1
nohup sh -c '"$1" 2>&1 | "$1" --bounded-log-sink "$2"' sh "$exe" %s >/dev/null 2>%s < /dev/null &
) >/dev/null 2>&1 &`, shellQuote(exe), shellQuote(logPath), shellQuote(workDir), shellQuote(logPath), shellQuote(errPath))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
