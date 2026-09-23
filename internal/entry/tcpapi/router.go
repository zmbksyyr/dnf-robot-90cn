package tcpapi

import (
	"robot/internal/scheduler"
	"robot/internal/shared"
)

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
	if capability, ok := commandCapability(cmd); ok {
		if err := manager.RequireCapability(capability); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()})
		}
	}
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

func commandCapability(cmd string) (shared.BackendCapability, bool) {
	switch cmd {
	case "createRobots":
		return shared.CapabilityProvision, true
	case "robotsMove":
		return shared.CapabilityTownMove, true
	case "robotsShout", "robotsShoutLocal":
		return shared.CapabilityShout, true
	case "robotsShoutWorld":
		return shared.CapabilityWorldShout, true
	case "robotsStore", "robotsStoreAsync":
		return shared.CapabilityStore, true
	case "cleanupRobots", "cleanupRobotsAsync":
		return shared.CapabilityCleanup, true
	case "partySkillReload":
		return shared.CapabilitySkill, true
	case "partyDebugStart", "partyDebugStop", "partyDebugStatus":
		return shared.CapabilityPartyDebug, true
	case "systemAnnouncement":
		return shared.CapabilitySystemAnnouncement, true
	case "keypairReleaseDefault":
		return shared.CapabilityKeypair, true
	case "dangerousDeleteUnlock", "dangerousDeleteAsync":
		return shared.CapabilityDangerousDelete, true
	default:
		if isMarketCommand(cmd) {
			return shared.CapabilityMarket, true
		}
		return "", false
	}
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
