package webadmin

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	foundationconfig "robot/internal/foundation/config"
	foundationnetwork "robot/internal/foundation/network"
)

type callRequest struct {
	Command string                 `json:"command"`
	Payload map[string]interface{} `json:"payload"`
}

func (s *Server) handleCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req callRequest
	if err := foundationconfig.DecodeJSONLimit(r.Body, 2*1024*1024, &req); err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeJSON(w, map[string]interface{}{"ok": false, "error": "empty command"})
		return
	}
	raw, err := callRobot(s.robotAddr, req.Command, req.Payload, robotCallTimeout(req.Command), s.cfg.MaxResponseBytes)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	result, err := decodeRobotResult(raw)
	if err != nil {
		writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
		return
	}
	out := map[string]interface{}{"ok": true, "result": result}
	if business, ok := result.(map[string]interface{}); ok {
		if businessOK, exists := business["ok"].(bool); exists && !businessOK {
			out["ok"] = false
			if message, ok := business["error"].(string); ok {
				out["error"] = message
			}
		}
	}
	if r.URL.Query().Get("raw") == "1" {
		out["raw"] = raw
	}
	writeJSON(w, out)
}

func robotCallTimeout(command string) time.Duration {
	switch strings.TrimSpace(command) {
	case "robotsOnline":
		return 120 * time.Second
	case "robotsStore":
		return 90 * time.Second
	default:
		return 30 * time.Second
	}
}

func callRobot(addr, command string, payload map[string]interface{}, timeout time.Duration, maxResponseBytes int) (string, error) {
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if maxResponseBytes <= 0 {
		maxResponseBytes = 4 * 1024 * 1024
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	packet := fmt.Sprintf("<tw><c>%s</c><json>%s</json></tw>", command, body)
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if err := foundationnetwork.WriteFull(conn, []byte(packet)); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(conn, int64(maxResponseBytes)+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxResponseBytes {
		return "", fmt.Errorf("robot response too large")
	}
	raw := string(data)
	if _, err := decodeRobotResult(raw); err != nil {
		return "", err
	}
	return raw, nil
}

func decodeRobotResult(raw string) (interface{}, error) {
	const prefix = "<tw><result>"
	const suffix = "</result></tw>"
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, suffix) {
		return nil, fmt.Errorf("invalid robot response frame")
	}
	payload := raw[len(prefix) : len(raw)-len(suffix)]
	var out interface{}
	if err := foundationconfig.DecodeJSONBytes([]byte(payload), &out); err != nil {
		return nil, fmt.Errorf("invalid robot response JSON: %w", err)
	}
	return out, nil
}

func parseRobotResult(raw string) interface{} {
	result, _ := decodeRobotResult(raw)
	return result
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	reportResponseWriteError(w, json.NewEncoder(w).Encode(v))
}
