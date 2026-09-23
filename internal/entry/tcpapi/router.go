package tcpapi

import "robot/internal/scheduler"

func HandlePacket(clientID, pkt string, manager *scheduler.RobotManager) (response string) {
	defer func() {
		if r := recover(); r != nil {
			logRobotActionf("[handlePacket] panic recovered client=%s err=%v\n", clientID, r)
			response = wrapResult(map[string]interface{}{"ok": false, "error": "internal robot command failure"})
		}
	}()
	if err := validateRequestPacket(pkt); err != nil {
		return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()})
	}

	cmd := extractTagContent(pkt, "c")
	if RequiresGameRuntime(cmd) {
		if err := manager.CheckGameCommand(); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()})
		}
	}
	if response, handled := handleProtocolCommand(cmd); handled {
		return response
	}
	if response, handled := handleRobotCommand(cmd, pkt, manager); handled {
		return response
	}
	if response, handled := handleDangerousDeleteCommand(clientID, cmd, pkt, manager); handled {
		return response
	}
	if response, handled := handleSystemCommand(cmd, pkt, manager); handled {
		return response
	}
	if response, handled := handleMarketCommand(cmd, pkt, manager); handled {
		return response
	}

	logRobotActionf("unknown command: %s\n", cmd)
	return wrapResult(map[string]interface{}{"ok": false, "error": "unknown command"})
}

func handleProtocolCommand(cmd string) (string, bool) {
	switch cmd {
	case "05":
		return "", true
	case "sys":
		return wrapResult(map[string]interface{}{"ok": true, "message": "sys ok"}), true
	default:
		return "", false
	}
}

func RequiresGameRuntime(cmd string) bool {
	switch cmd {
	case "createRobots",
		"robotsOnline",
		"robotsOnlineAsync",
		"robotsMove",
		"robotsShout",
		"robotsShoutWorld",
		"robotsShoutLocal",
		"robotsStore",
		"robotsStoreAsync",
		"robotsLogout",
		"robotsLogoutAsync",
		"autoStart":
		return true
	default:
		return false
	}
}
