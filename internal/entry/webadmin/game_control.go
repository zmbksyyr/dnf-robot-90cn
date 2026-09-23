package webadmin

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"robot/internal/foundation/atomicfile"
	foundationconfig "robot/internal/foundation/config"
	"robot/internal/shared"
)

const (
	gameMaxUserCacheTTL = 30 * time.Second
	gameMaxUserLimit    = 600
)

func (s *Server) handleServicePorts(w http.ResponseWriter, _ *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilityServiceControl) {
		return
	}
	services, err := discoverServicePorts(s.cfg)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "services": services, "checked_at": time.Now().UTC().Format(time.RFC3339)})
}

var (
	maxUserValuePattern   = regexp.MustCompile(`(?m)^\s*max_user_num\s*=\s*([0-9]+)\s*$`)
	maxUserReplacePattern = regexp.MustCompile(`(?m)^(\s*max_user_num\s*=\s*)[0-9]+(\s*)$`)
	writeGameConfigFile   = atomicfile.WriteFile
)

type gameMaxUserCache struct {
	checkedAt time.Time
	maxUser   int
	cfgName   string
	cfgPath   string
	ok        bool
}

func (s *Server) handleGamePort(w http.ResponseWriter, _ *http.Request) {
	addr := net.JoinHostPort(s.cfg.RobotConnectIP, strconv.Itoa(s.cfg.RobotGamePort))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "addr": addr, "error": err.Error()})
		return
	}
	_ = conn.Close()
	out := map[string]interface{}{"ok": true, "addr": addr}
	if s.supportsBackendCapability(shared.CapabilityServiceControl) {
		if maxUser, cfgName, cfgPath, ok := s.gameMaxUserNum(); ok {
			out["max_user_num"] = maxUser
			out["game_cfg_name"] = cfgName
			out["game_cfg_path"] = cfgPath
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleMaxUser(w http.ResponseWriter, r *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilityServiceControl) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		maxUser, files, err := s.readMaxUserNum()
		if err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		writeJSON(w, map[string]interface{}{"ok": true, "max_user_num": maxUser, "files": files, "running": dfGameRRunning()})
	case http.MethodPost:
		var req struct {
			MaxUserNum int `json:"max_user_num"`
		}
		if err := foundationconfig.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		if req.MaxUserNum <= 0 || req.MaxUserNum > gameMaxUserLimit {
			writeJSON(w, map[string]interface{}{"ok": false, "error": "max_user_num must be between 1 and 600"})
			return
		}
		files, changed, err := s.writeMaxUserNum(req.MaxUserNum)
		if err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		running := dfGameRRunning()
		out := map[string]interface{}{"ok": true, "max_user_num": req.MaxUserNum, "files": files, "changed": changed, "running": running, "restart_required": changed}
		if !changed {
			out["message"] = "max_user_num already configured"
		} else {
			out["message"] = "max_user_num updated; restart /root/run for the change to take effect"
		}
		writeJSON(w, out)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleServerScript(w http.ResponseWriter, r *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilityServiceControl) {
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, s.serverScriptSnapshot())
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := foundationconfig.DecodeJSONLimit(r.Body, 64*1024, &req); err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	script := ""
	runScript := strings.TrimSpace(s.cfg.ServiceRunScript)
	if runScript == "" {
		runScript = "/root/run"
	}
	switch strings.TrimSpace(req.Action) {
	case "run":
		script = runScript
	case "stop":
		script = filepath.Join(filepath.Dir(runScript), "stop")
	default:
		writeJSON(w, map[string]interface{}{"ok": false, "error": "unknown script action"})
		return
	}
	info, err := os.Stat(script)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "script": script, "error": err.Error()})
		return
	}
	if info.IsDir() {
		writeJSON(w, map[string]interface{}{"ok": false, "script": script, "error": "script path is a directory"})
		return
	}
	writeJSON(w, s.startServerScript(strings.TrimSpace(req.Action), script))
}

type serverScriptStatus struct {
	OK         bool   `json:"ok"`
	Running    bool   `json:"running"`
	Action     string `json:"action,omitempty"`
	Script     string `json:"script,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output,omitempty"`
}

func (s *Server) serverScriptSnapshot() serverScriptStatus {
	s.serverScriptMu.Lock()
	defer s.serverScriptMu.Unlock()
	status := s.serverScript
	status.OK = true
	return status
}

func (s *Server) startServerScript(action, script string) serverScriptStatus {
	s.serverScriptMu.Lock()
	defer s.serverScriptMu.Unlock()

	// Server scripts are command dispatchers, not long-running service jobs.
	// Cancel an older startup dispatcher before submitting another run or stop,
	// so a delayed /root/run cannot relaunch services after a stop request.
	if s.serverScriptCancel != nil {
		s.serverScriptCancel()
		s.serverScriptCancel = nil
	}
	status := serverScriptStatus{
		OK:        false,
		Running:   false,
		Action:    action,
		Script:    script,
		StartedAt: time.Now().Format(time.RFC3339),
	}
	cancel, err := launchDetachedServerScript(script)
	if err != nil {
		status.Error = err.Error()
		s.serverScript = status
		return status
	}
	s.serverScriptCancel = cancel
	status.OK = true
	s.serverScript = status
	return status
}

var launchDetachedServerScript = func(script string) (func(), error) {
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "/bin/sh", script)
	cmd.Dir = "/root"
	cmd.Stdin = devNull
	cmd.Stdout = devNull
	cmd.Stderr = devNull
	configureCommandProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		_ = devNull.Close()
		return nil, err
	}
	go func() {
		_ = cmd.Wait()
		cancel()
		_ = devNull.Close()
	}()
	return func() {
		cancel()
		killCommandProcessGroup(cmd)
	}, nil
}

func (s *Server) stopServerScript() {
	s.serverScriptMu.Lock()
	defer s.serverScriptMu.Unlock()
	if s.serverScriptCancel == nil {
		return
	}
	s.serverScriptCancel()
	s.serverScriptCancel = nil
}

func (s *Server) handleMonitorService(w http.ResponseWriter, _ *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilitySystemAnnouncement) {
		return
	}
	s.handleLocalTCPService(w, s.cfg.MonitorPort)
}

func (s *Server) handleRelayService(w http.ResponseWriter, _ *http.Request) {
	if s.rejectUnsupportedCapability(w, shared.CapabilityServiceControl) {
		return
	}
	s.handleLocalTCPService(w, s.cfg.RelayPort)
}

func (s *Server) handleLocalTCPService(w http.ResponseWriter, port int) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "addr": addr, "state": "closed", "error": err.Error()})
		return
	}
	_ = conn.Close()
	writeJSON(w, map[string]interface{}{"ok": true, "addr": addr, "state": "open"})
}

func (s *Server) readMaxUserNum() (int, []string, error) {
	paths, err := s.gameCfgPaths()
	if err != nil {
		return 0, nil, err
	}
	files := make([]string, 0, len(paths))
	maxUser := 0
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return 0, nil, err
		}
		m := maxUserValuePattern.FindSubmatch(data)
		if len(m) != 2 {
			continue
		}
		n, err := strconv.Atoi(string(m[1]))
		if err != nil || n <= 0 {
			continue
		}
		files = append(files, path)
		if maxUser == 0 {
			maxUser = n
		}
	}
	if len(files) == 0 {
		return 0, nil, fmt.Errorf("no max_user_num found under %s", filepath.Join(filepath.Dir(s.cfg.DFGameR), "cfg"))
	}
	return maxUser, files, nil
}

func (s *Server) writeMaxUserNum(maxUser int) ([]string, bool, error) {
	paths, err := s.gameCfgPaths()
	if err != nil {
		return nil, false, err
	}
	matched := make([]string, 0, len(paths))
	type pendingUpdate struct {
		path     string
		original []byte
		next     []byte
		mode     os.FileMode
	}
	updates := make([]pendingUpdate, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, false, err
		}
		if !maxUserReplacePattern.Match(data) {
			continue
		}
		matched = append(matched, path)
		next := maxUserReplacePattern.ReplaceAll(data, []byte("${1}"+strconv.Itoa(maxUser)+"${2}"))
		if bytes.Equal(next, data) {
			continue
		}
		mode := os.FileMode(0644)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		updates = append(updates, pendingUpdate{path: path, original: data, next: next, mode: mode})
	}
	if len(matched) == 0 {
		return nil, false, fmt.Errorf("no max_user_num found under %s", filepath.Join(filepath.Dir(s.cfg.DFGameR), "cfg"))
	}
	if len(updates) == 0 {
		return matched, false, nil
	}
	written := make([]pendingUpdate, 0, len(updates))
	for _, update := range updates {
		if err := writeGameConfigFile(update.path, update.next, update.mode); err != nil {
			rollbackErrors := make([]string, 0)
			for i := len(written) - 1; i >= 0; i-- {
				previous := written[i]
				if rollbackErr := writeGameConfigFile(previous.path, previous.original, previous.mode); rollbackErr != nil {
					rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", previous.path, rollbackErr))
				}
			}
			if len(rollbackErrors) > 0 {
				return nil, false, fmt.Errorf("update %s: %w; rollback failed: %s", update.path, err, strings.Join(rollbackErrors, "; "))
			}
			return nil, false, fmt.Errorf("update %s: %w", update.path, err)
		}
		written = append(written, update)
	}
	s.invalidateGameMaxUserCache()
	return matched, true, nil
}

func (s *Server) gameCfgPaths() ([]string, error) {
	if s.cfg == nil || strings.TrimSpace(s.cfg.DFGameR) == "" {
		return nil, fmt.Errorf("DfGameR is not configured")
	}
	cfgDir := filepath.Join(filepath.Dir(s.cfg.DFGameR), "cfg")
	paths, err := filepath.Glob(filepath.Join(cfgDir, "*.cfg"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no cfg files found under %s", cfgDir)
	}
	return paths, nil
}

func dfGameRRunning() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		parts := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		for _, part := range parts {
			base := filepath.Base(part)
			if base == "df_game_r" || strings.HasSuffix(part, "/df_game_r") {
				return true
			}
		}
	}
	return false
}

func (s *Server) gameMaxUserNum() (int, string, string, bool) {
	s.gameMaxUserMu.Lock()
	defer s.gameMaxUserMu.Unlock()
	if !s.gameMaxUser.checkedAt.IsZero() && time.Since(s.gameMaxUser.checkedAt) < gameMaxUserCacheTTL {
		cache := s.gameMaxUser
		return cache.maxUser, cache.cfgName, cache.cfgPath, cache.ok
	}
	s.gameMaxUser = s.readGameMaxUserNum()
	cache := s.gameMaxUser
	return cache.maxUser, cache.cfgName, cache.cfgPath, cache.ok
}

func (s *Server) readGameMaxUserNum() gameMaxUserCache {
	cache := gameMaxUserCache{checkedAt: time.Now()}
	cfgName, ok := gameConfigNameForPort(s.cfg.RobotGamePort)
	if !ok || cfgName == "" || strings.ContainsAny(cfgName, `/\`) {
		return cache
	}
	cfgPath := filepath.Join(filepath.Dir(s.cfg.DFGameR), "cfg", cfgName+".cfg")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return cache
	}
	m := maxUserValuePattern.FindSubmatch(data)
	if len(m) != 2 {
		return cache
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil || n <= 0 {
		return cache
	}
	cache.maxUser = n
	cache.cfgName = cfgName
	cache.cfgPath = cfgPath
	cache.ok = true
	return cache
}

func (s *Server) invalidateGameMaxUserCache() {
	s.gameMaxUserMu.Lock()
	s.gameMaxUser = gameMaxUserCache{}
	s.gameMaxUserMu.Unlock()
}

func gameConfigNameForPort(port int) (string, bool) {
	cmd := exec.Command("ss", "-lntp")
	data, err := cmd.Output()
	if err != nil || len(data) == 0 {
		return "", false
	}
	portPattern := regexp.MustCompile(`:` + regexp.QuoteMeta(strconv.Itoa(port)) + `\s`)
	var line []byte
	for _, candidate := range bytes.Split(data, []byte{'\n'}) {
		if portPattern.Match(candidate) {
			line = candidate
			break
		}
	}
	if len(line) == 0 {
		return "", false
	}
	_, pid, ok := parseSSProcess(string(line))
	if !ok {
		return "", false
	}
	cmdline, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return "", false
	}
	parts := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
	for i, part := range parts {
		if strings.HasSuffix(part, "df_game_r") || part == "df_game_r" || strings.Contains(part, "/df_game_r") {
			if i+1 < len(parts) && parts[i+1] != "" {
				return parts[i+1], true
			}
		}
	}
	return "", false
}
