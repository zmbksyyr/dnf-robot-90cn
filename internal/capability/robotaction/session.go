package robotaction

import (
	"fmt"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type SessionService struct {
	Env SessionEnv
}

type SessionEnv interface {
	CountRuntimeRunning() int
	EnsureWorldHornByCID(cid int) error
	InvalidateCharacterCache(uid int) error
	RobotConnectIP() string
	RobotGamePort() int
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
	RuntimeStatusMapFresh() map[int]robotcap.RuntimeStatus
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
	SendLogout(uid int) error
	SendOnline(userinfos []shared.RuntimeOnlineUser) error
}

type sessionLoginIPEnv interface {
	RobotInnerIP() string
}

type sessionOnlinePrepareEnv interface {
	PrepareOnlineRobot(robotcap.Info, robotconfig.RuntimeConfig) (robotcap.Info, error)
}

func (s SessionService) Online(req robotcap.CommandRequest, confirm bool, rc robotconfig.RuntimeConfig) (robotcap.CommandResult, error) {
	return s.online(req, confirm, rc)
}

func (s SessionService) online(req robotcap.CommandRequest, confirm bool, rc robotconfig.RuntimeConfig) (robotcap.CommandResult, error) {
	env := s.Env
	robots, err := env.SelectRobots(req)
	if err != nil {
		return robotcap.CommandResult{}, err
	}
	result := robotcap.NewCommandResult(len(robots))
	if rc.SystemPacketRatePerSec > 0 {
		minInterval := 1000 / rc.SystemPacketRatePerSec
		if minInterval > 0 && rc.OnlineDispatchIntervalMS < minInterval {
			rc.OnlineDispatchIntervalMS = minInterval
		}
	}
	if len(robots) > rc.MaxOnlinePerCommand {
		return result, fmt.Errorf("requested %d robots exceeds max_online_per_command=%d", len(robots), rc.MaxOnlinePerCommand)
	}
	running := env.CountRuntimeRunning()
	alreadyRunning := 0
	status := env.RuntimeStatusMap()
	for _, robot := range robots {
		if st, ok := status[robot.UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			alreadyRunning++
		}
	}
	newLogins := len(robots) - alreadyRunning
	if running+newLogins > rc.MaxOnlineRobots {
		return result, fmt.Errorf("online limit exceeded: running=%d new=%d max_online_robots=%d", running, newLogins, rc.MaxOnlineRobots)
	}
	if rc.OnlineDispatchIntervalMS <= 0 {
		userinfos := make([]shared.RuntimeOnlineUser, 0, len(robots))
		for _, robot := range robots {
			robot, err = prepareOnlineRobot(env, robot, rc)
			if err != nil {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
				continue
			}
			if err := env.EnsureWorldHornByCID(robot.CID); err != nil {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
				continue
			}
			userinfos = append(userinfos, s.onlinePayload(robot))
			result.Accepted++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateAccepted})
		}
		if len(userinfos) > 0 {
			if err := env.SendOnline(userinfos); err != nil {
				accepted := result.Accepted
				result.Failed += accepted
				result.Accepted = 0
				for i := range result.Robots {
					if result.Robots[i].State == robotcap.ActionStateAccepted {
						result.Robots[i].State = robotcap.ActionStateFailed
						result.Robots[i].Message = err.Error()
					}
				}
			}
		}
	} else {
		for _, robot := range robots {
			robot, err = prepareOnlineRobot(env, robot, rc)
			if err != nil {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
				continue
			}
			if err := env.EnsureWorldHornByCID(robot.CID); err != nil {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
				continue
			}
			if err := env.SendOnline([]shared.RuntimeOnlineUser{s.onlinePayload(robot)}); err == nil {
				result.Accepted++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateAccepted})
			} else {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
			}
			time.Sleep(time.Duration(rc.OnlineDispatchIntervalMS) * time.Millisecond)
		}
	}
	if result.Accepted == 0 || !confirm {
		return result, nil
	}
	s.confirmOnline(&result, time.Duration(rc.OnlineConfirmTimeoutMS)*time.Millisecond)
	return result, nil
}

func prepareOnlineRobot(env SessionEnv, robot robotcap.Info, rc robotconfig.RuntimeConfig) (robotcap.Info, error) {
	prepareEnv, ok := env.(sessionOnlinePrepareEnv)
	if !ok {
		return robot, nil
	}
	prepared, err := prepareEnv.PrepareOnlineRobot(robot, rc)
	if err != nil {
		return robot, err
	}
	return prepared, nil
}

func (s SessionService) Logout(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	robots, err := s.Env.SelectRobots(req)
	if err != nil {
		return robotcap.CommandResult{}, err
	}
	return s.logoutRobots(robots), nil
}

func (s SessionService) LogoutUID(uid int) (robotcap.CommandResult, error) {
	if uid <= 0 {
		return robotcap.CommandResult{}, fmt.Errorf("invalid uid %d", uid)
	}
	return s.logoutRobots([]robotcap.Info{{UID: uid}}), nil
}

func (s SessionService) logoutRobots(robots []robotcap.Info) robotcap.CommandResult {
	result := robotcap.NewCommandResult(len(robots))
	for _, robot := range robots {
		if err := s.Env.SendLogout(robot.UID); err == nil {
			result.Accepted++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateAccepted})
		} else {
			result.Failed++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()})
		}
	}
	time.Sleep(500 * time.Millisecond)
	status := s.Env.RuntimeStatusMapFresh()
	for i := range result.Robots {
		if result.Robots[i].State != robotcap.ActionStateAccepted {
			continue
		}
		uid := result.Robots[i].UID
		if st, ok := status[uid]; ok && robotcap.ActiveRuntimeStatus(st) {
			result.Robots[i].State = robotcap.ActionStatePending
			result.Robots[i].Message = "runtime connection still exists"
			continue
		}
		if err := s.Env.InvalidateCharacterCache(uid); err != nil {
			result.Robots[i].State = robotcap.ActionStateFailed
			result.Robots[i].Message = fmt.Sprintf("runtime closed but character cache invalidation failed: %v", err)
			result.Failed++
			continue
		}
		result.Robots[i].OK = true
		result.Robots[i].State = robotcap.ActionStateClosed
		result.Confirmed++
	}
	return result
}

func (s SessionService) ConfirmAccepted(result *robotcap.CommandResult, timeout time.Duration) {
	s.confirmOnline(result, timeout)
}

func (s SessionService) onlinePayload(robot robotcap.Info) shared.RuntimeOnlineUser {
	loginIP := s.Env.RobotConnectIP()
	if env, ok := s.Env.(sessionLoginIPEnv); ok && env.RobotInnerIP() != "" {
		loginIP = env.RobotInnerIP()
	}
	return shared.RuntimeOnlineUser{
		BirthArea:     robot.Area,
		BirthVillage:  robot.Village,
		BirthX:        robot.X,
		BirthY:        robot.Y,
		CID:           robot.CID,
		GuildID:       robot.GuildID,
		CharacterSlot: 0,
		IP:            s.Env.RobotConnectIP(),
		LoginIP:       loginIP,
		Port:          s.Env.RobotGamePort(),
		UID:           robot.UID,
	}
}

func (s SessionService) confirmOnline(result *robotcap.CommandResult, timeout time.Duration) {
	if result == nil || result.Accepted == 0 {
		return
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		confirmed := 0
		status := s.Env.RuntimeStatusMap()
		for _, robot := range result.Robots {
			if robot.State != robotcap.ActionStateAccepted {
				continue
			}
			if st, ok := status[robot.UID]; ok && robotcap.ActiveRuntimeStatus(st) {
				confirmed++
			}
		}
		if confirmed >= result.Accepted {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	status := s.Env.RuntimeStatusMap()
	result.Confirmed = 0
	result.Failed = 0
	for i := range result.Robots {
		if result.Robots[i].State != robotcap.ActionStateAccepted {
			if !result.Robots[i].OK {
				result.Failed++
			}
			continue
		}
		if st, ok := status[result.Robots[i].UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			result.Robots[i].OK = true
			result.Robots[i].State = st.StateName
			if result.Robots[i].State == "" {
				result.Robots[i].State = robotcap.ActionStateRunning
			}
			result.Confirmed++
		} else {
			result.Robots[i].State = robotcap.ActionStatePending
			result.Robots[i].Message = "not confirmed running"
		}
	}
}
