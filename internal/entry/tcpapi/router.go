package tcpapi

import (
	"strings"

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
	fields, err := parseRequestPacket(pkt)
	if err != nil {
		return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()})
	}

	cmd := strings.TrimSpace(fields["c"])
	if capabilities := commandCapabilities(cmd); len(capabilities) > 0 {
		var capabilityErr error
		for _, capability := range capabilities {
			capabilityErr = manager.RequireCapability(capability)
			if capabilityErr == nil {
				break
			}
		}
		if capabilityErr != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": capabilityErr.Error()})
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
	logRobotActionf("unknown command: %s\n", cmd)
	return wrapResult(map[string]interface{}{"ok": false, "error": "unknown command"})
}

// commandCapabilities returns alternatives: satisfying any listed capability
// admits the command. Most commands have exactly one requirement; preferred
// shout intentionally supports either world or local delivery.
func commandCapabilities(cmd string) []shared.BackendCapability {
	switch cmd {
	case "createRobots":
		return []shared.BackendCapability{shared.CapabilityProvision}
	case "robotsMove":
		return []shared.BackendCapability{shared.CapabilityTownMove}
	case "robotsShout":
		return []shared.BackendCapability{shared.CapabilityWorldShout, shared.CapabilityShout}
	case "robotsShoutLocal":
		return []shared.BackendCapability{shared.CapabilityShout}
	case "robotsShoutWorld":
		return []shared.BackendCapability{shared.CapabilityWorldShout}
	case "robotsStore", "robotsStoreAsync":
		return []shared.BackendCapability{shared.CapabilityStore}
	case "cleanupRobots", "cleanupRobotsAsync":
		return []shared.BackendCapability{shared.CapabilityCleanup}
	case "partySkillReload":
		return []shared.BackendCapability{shared.CapabilitySkill}
	case "partyDebugStart", "partyDebugStop", "partyDebugStatus":
		return []shared.BackendCapability{shared.CapabilityPartyDebug}
	case "systemAnnouncement":
		return []shared.BackendCapability{shared.CapabilitySystemAnnouncement}
	case "dangerousDeleteUnlock", "dangerousDeleteAsync":
		return []shared.BackendCapability{shared.CapabilityDangerousDelete}
	default:
		return nil
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
