package tcpapi

import (
	robotcap "robot/internal/capability/robot"
	"robot/internal/scheduler"
)

func handleRobotCommand(cmd, pkt string, manager *scheduler.RobotManager) (string, bool) {
	switch cmd {
	case "createRobots":
		var req robotcap.CreateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		robots, err := manager.CreateRobots(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "robots": robots}), true
	case "robotsOnline":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.OnlineManaged(req)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsOnlineAsync":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		return queueRobotAction(manager, "robotsOnlineAsync", robotcap.CommandRequestScope(req), func() (string, error) {
			res, err := manager.OnlineManaged(req)
			logRobotCommandResult("robotsOnlineAsync", res, err)
			return robotcap.CommandOperationSummary(res, err), err
		}), true
	case "robotsMove":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.MoveManaged(req)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsShout":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.ShoutPreferredManaged(req)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsShoutWorld":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.ShoutManaged(req, true)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsShoutLocal":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.ShoutManaged(req, false)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsStore":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.StoreManaged(req)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsStoreAsync":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		return queueRobotAction(manager, "robotsStoreAsync", robotcap.CommandRequestScope(req), func() (string, error) {
			res, err := manager.StoreManaged(req)
			logRobotCommandResult("robotsStoreAsync", res, err)
			return robotcap.CommandOperationSummary(res, err), err
		}), true
	case "robotsStatus":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.RobotsStatus(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "robotsLogout":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.LogoutManaged(req)
		return wrapResult(map[string]interface{}{"ok": err == nil && res.Failed == 0, "error": errString(err), "result": res}), true
	case "robotsLogoutAsync":
		req, err := parseRobotCommand(pkt)
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		return queueRobotAction(manager, "robotsLogoutAsync", robotcap.CommandRequestScope(req), func() (string, error) {
			res, err := manager.LogoutManaged(req)
			logRobotCommandResult("robotsLogoutAsync", res, err)
			return robotcap.CommandOperationSummary(res, err), err
		}), true
	case "autoStart":
		res, err := manager.SetAutoEnabled(true)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "autoStop":
		res, err := manager.SetAutoEnabled(false)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "robotConfigGet":
		res, err := manager.RobotConfig()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "robotConfigUpdate":
		var req robotcap.ConfigUpdateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := manager.UpdateRobotConfig(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "partySkillReload":
		res, err := manager.ReloadPartySkills()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "partyDebugStart":
		res, err := manager.PartyDebugStart()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "partyDebugStop":
		res, err := manager.PartyDebugStop()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "partyDebugStatus":
		res, err := manager.PartyDebugStatus()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "cleanupRobots":
		var req robotcap.CleanupRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		if cleanupRequiresAsync(req) {
			return wrapResult(map[string]interface{}{"ok": false, "error": "full cleanup must use cleanupRobotsAsync"}), true
		}
		res, err := manager.CleanupRobots(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "cleanupRobotsAsync":
		var req robotcap.CleanupRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		return queueExclusiveAction(manager, "cleanupRobotsAsync", func() {
			res, err := manager.CleanupRobots(req)
			if err != nil {
				logRobotActionf("[WebAction] cleanupRobotsAsync failed err=%v\n", err)
				return
			}
			logRobotActionf("[WebAction] cleanupRobotsAsync done candidates=%d deleted=%d skipped=%d\n",
				len(res.Candidates), res.Deleted, res.Skipped)
		}), true
	default:
		return "", false
	}
}

func cleanupRequiresAsync(req robotcap.CleanupRequest) bool {
	return req.Force && len(req.UIDs) == 0 && req.MinUID <= 0 && req.MaxUID <= 0
}

func parseRobotCommand(pkt string) (robotcap.CommandRequest, error) {
	var req robotcap.CommandRequest
	if err := decodePayload(pkt, &req); err != nil {
		return req, err
	}
	return req, nil
}
