package webadmin

import (
	"net"
	"net/http"
	"strconv"
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

func (s *Server) handleStopRobot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]interface{}{"ok": true, "message": "robot stop requested"})
	go s.requestLifecycle(LifecycleStop)
}
