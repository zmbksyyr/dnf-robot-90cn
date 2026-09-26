package webadmin

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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
