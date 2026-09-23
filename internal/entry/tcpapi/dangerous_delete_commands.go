package tcpapi

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/lockhub"
	"robot/internal/scheduler"
	"strings"
	"time"
)

const dangerousDeleteUnlockCode = "123"

const maxDangerousDeleteTokens = 32

type dangerousDeleteUnlockRequest struct {
	Code string `json:"code"`
}

type dangerousDeleteCommandRequest struct {
	Token  string `json:"token"`
	Mode   string `json:"mode"`
	CID    int    `json:"cid"`
	UID    int    `json:"uid"`
	MinUID int    `json:"uid_min"`
	MaxUID int    `json:"uid_max"`
}

var dangerousDeleteTokens = struct {
	access lockhub.Locker
	values map[string]dangerousDeleteToken
}{values: make(map[string]dangerousDeleteToken)}

type dangerousDeleteToken struct {
	Expires  time.Time
	ClientIP string
}

func handleDangerousDeleteCommand(clientID, cmd, pkt string, manager *scheduler.RobotManager) (string, bool) {
	if !isDangerousDeleteCommand(cmd) {
		return "", false
	}
	switch cmd {
	case "dangerousDeleteUnlock":
		clientIP, ok := loopbackClientIP(clientID)
		if !ok {
			return wrapResult(map[string]interface{}{"ok": false, "error": "dangerous delete is only available through the local web admin"}), true
		}
		var req dangerousDeleteUnlockRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		if strings.TrimSpace(req.Code) != dangerousDeleteUnlockCode {
			return wrapResult(map[string]interface{}{"ok": false, "error": "invalid unlock code"}), true
		}
		token, err := issueDangerousDeleteToken(clientIP)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		uidStart, uidEnd := manager.DangerousDeleteDefaults()
		return wrapResult(map[string]interface{}{"ok": true, "result": map[string]interface{}{
			"token": token, "uid_min": uidStart, "uid_max": uidEnd,
		}}), true
	case "dangerousDeleteAsync":
		clientIP, ok := loopbackClientIP(clientID)
		if !ok {
			return wrapResult(map[string]interface{}{"ok": false, "error": "dangerous delete is only available through the local web admin"}), true
		}
		var req dangerousDeleteCommandRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		if !consumeDangerousDeleteToken(req.Token, clientIP, time.Now()) {
			return wrapResult(map[string]interface{}{"ok": false, "error": "dangerous delete is not unlocked or token expired"}), true
		}
		deleteReq := robotcap.DangerousDeleteRequest{Mode: req.Mode, CID: req.CID, UID: req.UID, MinUID: req.MinUID, MaxUID: req.MaxUID}
		return queueExclusiveAction(manager, "dangerousDeleteAsync", func() {
			res, err := manager.DangerousDelete(deleteReq)
			if err != nil {
				logRobotActionf("[WebAction] dangerousDeleteAsync failed mode=%s err=%v\n", req.Mode, err)
				return
			}
			logRobotActionf("[WebAction] dangerousDeleteAsync done mode=%s accounts=%d characters=%d registry=%d\n",
				res.Mode, res.AccountCount, res.CharacterCount, res.RegistryCount)
		}), true
	}
	return "", false
}

func isDangerousDeleteCommand(cmd string) bool {
	return cmd == "dangerousDeleteUnlock" || cmd == "dangerousDeleteAsync"
}

func issueDangerousDeleteToken(clientIP string) (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate dangerous delete token: %w", err)
	}
	token := hex.EncodeToString(raw)
	now := time.Now()
	dangerousDeleteTokens.access.Lock()
	defer dangerousDeleteTokens.access.Unlock()
	pruneDangerousDeleteTokensLocked(now)
	if len(dangerousDeleteTokens.values) >= maxDangerousDeleteTokens {
		var oldestToken string
		var oldestExpiry time.Time
		for candidate, issued := range dangerousDeleteTokens.values {
			if oldestToken == "" || issued.Expires.Before(oldestExpiry) {
				oldestToken = candidate
				oldestExpiry = issued.Expires
			}
		}
		delete(dangerousDeleteTokens.values, oldestToken)
	}
	dangerousDeleteTokens.values[token] = dangerousDeleteToken{
		Expires:  now.Add(10 * time.Minute),
		ClientIP: clientIP,
	}
	return token, nil
}

func pruneDangerousDeleteTokensLocked(now time.Time) {
	for token, issued := range dangerousDeleteTokens.values {
		if !now.Before(issued.Expires) {
			delete(dangerousDeleteTokens.values, token)
		}
	}
}

func consumeDangerousDeleteToken(token, clientIP string, now time.Time) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	dangerousDeleteTokens.access.Lock()
	defer dangerousDeleteTokens.access.Unlock()
	issued, ok := dangerousDeleteTokens.values[token]
	if !ok {
		return false
	}
	delete(dangerousDeleteTokens.values, token)
	return issued.ClientIP == clientIP && now.Before(issued.Expires)
}

func loopbackClientIP(clientID string) (string, bool) {
	host, _, err := net.SplitHostPort(strings.TrimSpace(clientID))
	if err != nil {
		return "", false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	return ip.String(), true
}
