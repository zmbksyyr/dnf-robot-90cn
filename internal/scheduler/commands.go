package scheduler

import (
	"fmt"
	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
	"time"
)

const robotOnlineConfirmBudget = 75 * time.Second

func (m *RobotManager) OnlineManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	registry, robots, rc, early, err := m.prepareUserActorCommand(req, "online", true)
	if err != nil || early != nil {
		return resultOrZero(early), err
	}
	result := robotcap.NewCommandResult(len(robots))
	timeout := time.Duration(rc.SystemManualActionTimeoutSec) * time.Second
	for _, robot := range robots {
		if !registry.HasUID(robot.UID) {
			if !registry.AttachUID(robot.UID, 10*time.Second) {
				result.Failed++
				result.Robots = append(result.Robots, actorFullResult(robot, "online"))
				continue
			}
		}
		item, ok := registry.Command(robot.UID, actormodel.CommandOnline, timeout)
		item.CID = robot.CID
		if ok && (item.OK || item.State == robotcap.ActionStateAccepted || item.State == robotcap.ActionStateRunning) {
			result.Accepted++
			result.Robots = append(result.Robots, robotActionResult(robot, false, robotcap.ActionStateAccepted, ""))
		} else {
			result.Failed++
			result.Robots = append(result.Robots, failedActorResult(robot, item, "online actor command failed"))
		}
	}
	m.waitManagedConfirm(&result, robotOnlineConfirmBudget)
	return result, nil
}

func (m *RobotManager) MoveManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	if err := m.requireBackendCapability(shared.CapabilityTownMove); err != nil {
		return robotcap.CommandResult{}, err
	}
	return m.actorCommandManaged(req, actormodel.CommandMove, "move")
}

func (m *RobotManager) ShoutManaged(req robotcap.CommandRequest, world bool) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	if world {
		if err := m.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
			return robotcap.CommandResult{}, err
		}
	} else if err := m.requireBackendCapability(shared.CapabilityShout); err != nil {
		return robotcap.CommandResult{}, err
	}
	if world {
		return m.actorCommandManaged(req, actormodel.CommandShoutWorld, "shout_world")
	}
	cmd := actormodel.CommandShoutLocal
	name := "shout_local"
	return m.actorCommandManaged(req, cmd, name)
}

// ShoutPreferredManaged keeps one user action and selects the strongest
// channel declared by the active backend.
func (m *RobotManager) ShoutPreferredManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	if m.requireBackendCapability(shared.CapabilityWorldShout) == nil {
		return m.ShoutManaged(req, true)
	}
	return m.ShoutManaged(req, false)
}

func (m *RobotManager) ShoutBothManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	if err := m.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
		return robotcap.CommandResult{}, err
	}
	registry, robots, _, early, err := m.prepareUserActorCommand(req, "shout", true)
	if err != nil || early != nil {
		return resultOrZero(early), err
	}
	result := robotcap.NewCommandResult(len(robots))
	timeout := time.Duration(m.loadRobotConfig().SystemManualActionTimeoutSec) * time.Second
	for _, robot := range robots {
		local, localOK := registry.Command(robot.UID, actormodel.CommandShoutLocal, timeout)
		world, worldOK := registry.Command(robot.UID, actormodel.CommandShoutWorld, timeout)
		if localOK && worldOK && local.OK && world.OK {
			result.Accepted++
			if local.State == robotcap.ActionStatePending || world.State == robotcap.ActionStatePending {
				result.Robots = append(result.Robots, robotActionResult(robot, true, robotcap.ActionStatePending, "actor commands are still running"))
			} else {
				result.Confirmed++
				result.Robots = append(result.Robots, robotActionResult(robot, true, robotcap.ActionStateSent, ""))
			}
		} else {
			result.Failed++
			msg := "actor command failed"
			if !localOK || !local.OK {
				msg = local.Message
				if msg == "" {
					msg = local.State
				}
			} else if !worldOK || !world.OK {
				msg = world.Message
				if msg == "" {
					msg = world.State
				}
			}
			result.Robots = append(result.Robots, robotActionResult(robot, false, robotcap.ActionStateFailed, msg))
		}
	}
	return result, nil
}

func (m *RobotManager) StoreManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	if err := m.requireBackendCapability(shared.CapabilityStore); err != nil {
		return robotcap.CommandResult{}, err
	}
	return m.actorCommandManaged(req, actormodel.CommandStore, "store")
}

func (m *RobotManager) LogoutManaged(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	m.mutationMu.RLock()
	defer m.mutationMu.RUnlock()
	registry, robots, rc, early, err := m.prepareUserActorCommand(req, "logout", true)
	if err != nil || early != nil {
		return resultOrZero(early), err
	}
	result := robotcap.NewCommandResult(len(robots))
	timeout := time.Duration(rc.SystemManualActionTimeoutSec) * time.Second
	for _, robot := range robots {
		item, ok := registry.LogoutUID(robot.UID, timeout)
		item.CID = robot.CID
		if ok && item.OK {
			result.Accepted++
			if item.State != robotcap.ActionStatePending {
				result.Confirmed++
			}
			result.Robots = append(result.Robots, item)
		} else {
			result.Failed++
			result.Robots = append(result.Robots, failedActorResult(robot, item, "logout actor command failed"))
		}
	}
	return result, nil
}

func (m *RobotManager) actorCommandManaged(req robotcap.CommandRequest, cmd actormodel.Command, action string) (robotcap.CommandResult, error) {
	registry, robots, rc, early, err := m.prepareUserActorCommand(req, action, true)
	if err != nil || early != nil {
		return resultOrZero(early), err
	}
	result := robotcap.NewCommandResult(len(robots))
	timeout := time.Duration(rc.SystemManualActionTimeoutSec) * time.Second
	for _, robot := range robots {
		item, ok := registry.Command(robot.UID, cmd, timeout)
		item.CID = robot.CID
		if ok && item.OK {
			result.Accepted++
			if item.State != robotcap.ActionStatePending {
				result.Confirmed++
			}
			result.Robots = append(result.Robots, item)
		} else {
			result.Failed++
			result.Robots = append(result.Robots, failedActorResult(robot, item, fmt.Sprintf("%s actor command failed", action)))
		}
	}
	return result, nil
}

func (m *RobotManager) prepareUserActorCommand(req robotcap.CommandRequest, action string, attachInManual bool) (actorRegistry, []robotcap.Info, robotconfig.RuntimeConfig, *robotcap.CommandResult, error) {
	registry := m.currentActorRegistry()
	if registry == nil {
		return nil, nil, robotconfig.RuntimeConfig{}, nil, errActorRegistryUnavailable
	}
	robots, err := m.selectRobots(req)
	if err != nil {
		return nil, nil, robotconfig.RuntimeConfig{}, nil, err
	}
	rc := m.loadRobotConfig()
	if busy, reason := m.userActorCommandBusy(registry, rc); busy {
		return nil, nil, rc, rejectedCommandResult(robots, robotcap.ActionStateSchedulerBusy, reason), nil
	}
	missing := make([]robotcap.Info, 0)
	for _, robot := range robots {
		if !registry.HasUID(robot.UID) {
			missing = append(missing, robot)
		}
	}
	if len(missing) == 0 {
		return registry, robots, rc, nil, nil
	}
	if !attachInManual || m.autoActionsEnabled(rc) {
		return nil, nil, rc, rejectedCommandResult(robots, robotcap.ActionStateUIDNotAttached, fmt.Sprintf("%s requires actor attachment", action)), nil
	}
	if busy, reason := m.userActorCommandBusy(registry, rc); busy {
		return nil, nil, rc, rejectedCommandResult(robots, robotcap.ActionStateSchedulerBusy, reason), nil
	}
	end := m.beginActorContainerOp("manual_attach")
	defer end()
	timeout := time.Duration(rc.SystemManualActionTimeoutSec) * time.Second
	out := robotcap.NewCommandResult(len(robots))
	for _, robot := range missing {
		if registry.AttachUID(robot.UID, timeout) {
			continue
		}
		out.Failed++
		out.Robots = append(out.Robots, actorFullResult(robot, action))
	}
	if out.Failed > 0 {
		for _, robot := range robots {
			if registry.HasUID(robot.UID) {
				out.Accepted++
				out.Robots = append(out.Robots, robotActionResult(robot, true, robotcap.ActionStateAttached, ""))
			}
		}
		return nil, nil, rc, &out, nil
	}
	return registry, robots, rc, nil, nil
}

func (m *RobotManager) userActorCommandBusy(registry actorRegistry, rc robotconfig.RuntimeConfig) (bool, string) {
	if op, _, active := m.structuralOperation(); active {
		return true, schedulerReasonStructuralPrefix + op
	}
	if op, _, active := m.actorContainerOperation(); active {
		return true, schedulerReasonActorPrefix + op
	}
	snapshots := registry.actorSnapshots()
	manual := !m.autoActionsEnabled(rc)
	target := robotconfig.TargetCapacity(rc)
	actors := len(snapshots)
	idle := 0
	for _, snap := range snapshots {
		if snap.UID <= 0 {
			idle++
		}
		switch snap.State {
		case actormodel.StateAssigned, actormodel.StateOnline, actormodel.StateReleasing:
			return true, "actor_state=" + string(snap.State)
		}
	}
	if !manual {
		if actors < target {
			return true, fmt.Sprintf("auto_filling actors=%d target=%d", actors, target)
		}
		if idle > 0 {
			return true, fmt.Sprintf("auto_loading idle=%d", idle)
		}
	}
	return false, ""
}

func rejectedCommandResult(robots []robotcap.Info, state, message string) *robotcap.CommandResult {
	out := robotcap.NewCommandResult(len(robots))
	out.Failed = len(robots)
	for _, robot := range robots {
		out.Robots = append(out.Robots, robotActionResult(robot, false, state, message))
	}
	return &out
}

func resultOrZero(result *robotcap.CommandResult) robotcap.CommandResult {
	if result == nil {
		return robotcap.CommandResult{}
	}
	return *result
}

func robotActionResult(robot robotcap.Info, ok bool, state, message string) robotcap.ActionResult {
	return robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: ok, State: state, Message: message}
}

func failedActorResult(robot robotcap.Info, item robotcap.ActionResult, fallbackMessage string) robotcap.ActionResult {
	if item.UID == 0 {
		item.UID = robot.UID
	}
	if item.CID == 0 {
		item.CID = robot.CID
	}
	if item.State == "" {
		item.State = robotcap.ActionStateFailed
	}
	if item.Message == "" {
		item.Message = fallbackMessage
	}
	return item
}

func actorFullResult(robot robotcap.Info, action string) robotcap.ActionResult {
	return robotcap.ActionResult{
		UID:     robot.UID,
		CID:     robot.CID,
		OK:      false,
		State:   robotcap.ActionStateActorFull,
		Message: fmt.Sprintf("%s requires an empty actor slot", action),
	}
}

func (m *RobotManager) waitManagedConfirm(result *robotcap.CommandResult, timeout time.Duration) {
	m.sessionService().ConfirmAccepted(result, timeout)
}
