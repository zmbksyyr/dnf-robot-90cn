package robotaction

import (
	"fmt"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotspawn "robot/internal/capability/robotspawn"
	"robot/internal/foundation/mathx"
	"robot/internal/shared"
	"time"
)

type MoveService struct {
	Env MoveEnv
}

type MoveEnv interface {
	DispatchMoveStep(info robotcap.Info, targetVillage, targetArea, targetX, targetY, step, steps, speed int, rc robotconfig.RuntimeConfig) error
	LoadMapCatalog() []shared.MapCatalogItem
	RandBetween(min, max int) int
	RuntimeStatus(uid int) (robotcap.RuntimeStatus, bool)
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
}

type FollowTarget struct {
	Village int
	Area    int
	X       int
	Y       int
}

func (s MoveService) Move(req robotcap.CommandRequest, rc robotconfig.RuntimeConfig) (robotcap.CommandResult, error) {
	env := s.Env
	robots, err := env.SelectRobots(req)
	if err != nil {
		return robotcap.CommandResult{}, err
	}
	maps := env.LoadMapCatalog()
	status := env.RuntimeStatusMap()
	result := robotcap.NewCommandResult(len(robots))
	type movePlan struct {
		robot       robotcap.Info
		steps       int
		target      [2]int
		speeds      []int
		failed      bool
		resultIndex int
	}
	var plans []movePlan
	for _, robot := range robots {
		if st, ok := status[robot.UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			if st.Village != 0 || st.Area != 0 || st.X != 0 || st.Y != 0 {
				robot.Village, robot.Area, robot.X, robot.Y = st.Village, st.Area, st.X, st.Y
			}
			if st.RobotType == 2 || st.RobotType == 3 {
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: true, State: robotcap.ActionStateStore, Message: "skip moving store robot"})
				continue
			}
		}
		targetX, targetY := s.randomTarget(robot, rc, maps)
		steps := env.RandBetween(mathx.MaxInt(2, rc.MoveSteps-1), mathx.MinInt(8, rc.MoveSteps+2))
		speeds := make([]int, steps)
		for i := range speeds {
			speeds[i] = env.RandBetween(rc.MoveSpeedMin, rc.MoveSpeedMax)
		}
		result.Accepted++
		result.Robots = append(result.Robots, robotcap.ActionResult{UID: robot.UID, CID: robot.CID, OK: false, State: robotcap.ActionStateAccepted})
		plans = append(plans, movePlan{robot: robot, steps: steps, target: [2]int{targetX, targetY}, speeds: speeds, resultIndex: len(result.Robots) - 1})
	}
	maxSteps := 0
	for _, plan := range plans {
		maxSteps = mathx.MaxInt(maxSteps, plan.steps)
	}
	for step := 1; step <= maxSteps; step++ {
		for index := range plans {
			plan := &plans[index]
			if plan.failed || step > plan.steps {
				continue
			}
			if err := env.DispatchMoveStep(plan.robot, plan.robot.Village, plan.robot.Area, plan.target[0], plan.target[1], step, plan.steps, plan.speeds[step-1], rc); err != nil {
				plan.failed = true
				result.Failed++
				item := &result.Robots[plan.resultIndex]
				item.OK = false
				item.State = robotcap.ActionStateFailed
				item.Message = err.Error()
			}
		}
		if step < maxSteps {
			delay := rc.MoveStepDelayMS
			if delay > 0 {
				delay = env.RandBetween(mathx.MaxInt(300, delay/2), mathx.MaxInt(300, delay*3/2))
				time.Sleep(time.Duration(delay) * time.Millisecond)
			}
		}
	}
	waitMS := rc.MoveStepDelayMS*rc.MoveSteps + 500
	if waitMS < 500 {
		waitMS = 500
	}
	time.Sleep(time.Duration(waitMS) * time.Millisecond)
	status = env.RuntimeStatusMap()
	for i := range result.Robots {
		if result.Robots[i].State == robotcap.ActionStateFailed {
			continue
		}
		if st, ok := status[result.Robots[i].UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			result.Robots[i].OK = true
			result.Robots[i].State = st.StateName
			result.Confirmed++
		} else if result.Robots[i].State == robotcap.ActionStateAccepted {
			result.Robots[i].State = robotcap.ActionStatePending
			result.Robots[i].Message = "move not confirmed by runtime state"
		}
	}
	return result, nil
}

func (s MoveService) AutoMove(info robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem, follow *FollowTarget) error {
	targetVillage, targetArea := info.Village, info.Area
	var targetX, targetY int
	if follow != nil {
		targetVillage, targetArea = follow.Village, follow.Area
		if targetVillage != info.Village || targetArea != info.Area {
			if !hasUsableMap(maps, targetVillage, targetArea) {
				return fmt.Errorf("follow target town area %d/%d has no usable movement geometry", targetVillage, targetArea)
			}
		}
		targetInfo := info
		targetInfo.Village = targetVillage
		targetInfo.Area = targetArea
		targetX, targetY = s.followTarget(targetInfo, *follow, rc, maps)
	} else {
		targetX, targetY = s.randomTarget(info, rc, maps)
	}
	if (targetVillage != info.Village || targetArea != info.Area) && !shared.GenericAreaAllowed(info.GuildID, targetVillage) {
		return fmt.Errorf("generic area transport is unavailable for guild member destination %d/%d", targetVillage, targetArea)
	}
	steps := s.Env.RandBetween(mathx.MaxInt(2, rc.MoveSteps-1), mathx.MinInt(8, rc.MoveSteps+2))
	for step := 1; step <= steps; step++ {
		if st, ok := s.Env.RuntimeStatus(info.UID); ok && (st.RobotType == 2 || st.RobotType == 3 || st.StateName != robotcap.RuntimeStateRunning) {
			return nil
		}
		speed := s.Env.RandBetween(rc.MoveSpeedMin, rc.MoveSpeedMax)
		if err := s.Env.DispatchMoveStep(info, targetVillage, targetArea, targetX, targetY, step, steps, speed, rc); err != nil {
			return err
		}
		if step < steps && rc.MoveStepDelayMS > 0 {
			delay := s.Env.RandBetween(mathx.MaxInt(300, rc.MoveStepDelayMS/2), mathx.MaxInt(300, rc.MoveStepDelayMS*3/2))
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}
	}
	return nil
}

func hasUsableMap(maps []shared.MapCatalogItem, village, area int) bool {
	for _, mp := range maps {
		if mp.Use && mp.Village == village && mp.Area == area && len(robotspawn.MapRectangles(mp)) > 0 {
			return true
		}
	}
	return false
}

func (s MoveService) followTarget(info robotcap.Info, target FollowTarget, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (int, int) {
	xMin, xMax := target.X-rc.FollowRadiusX, target.X+rc.FollowRadiusX
	yMin, yMax := target.Y-rc.FollowRadiusY, target.Y+rc.FollowRadiusY
	if rc.FollowRadiusX <= 0 {
		xMin, xMax = target.X-120, target.X+120
	}
	if rc.FollowRadiusY <= 0 {
		yMin, yMax = target.Y-30, target.Y+30
	}
	for _, mp := range maps {
		if mp.Use && mp.Village == target.Village && mp.Area == target.Area {
			intersections := robotspawn.IntersectRectangles(robotspawn.MapRectangles(mp), shared.MapRectangle{XMin: xMin, XMax: xMax, YMin: yMin, YMax: yMax})
			if x, y, ok := robotspawn.RandomPoint(s.Env, intersections); ok {
				return x, y
			}
			return s.randomTarget(info, rc, maps)
		}
	}
	return s.randomTarget(info, rc, maps)
}

func (s MoveService) randomTarget(info robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (int, int) {
	var rectangles []shared.MapRectangle
	for _, mp := range maps {
		if mp.Use && mp.Village == info.Village && mp.Area == info.Area {
			rectangles = robotspawn.MapRectangles(mp)
			break
		}
	}
	if rc.SpawnFixed {
		configured := robotspawn.IntersectRectangles(rectangles, shared.MapRectangle{XMin: rc.SpawnXMin, XMax: rc.SpawnXMax, YMin: rc.SpawnYMin, YMax: rc.SpawnYMax})
		if len(configured) > 0 {
			rectangles = configured
		}
	}
	if len(rectangles) == 0 {
		rectangles = []shared.MapRectangle{{XMin: mathx.MaxInt(0, info.X-120), XMax: info.X + 120, YMin: mathx.MaxInt(0, info.Y-40), YMax: info.Y + 40}}
	}
	for i := 0; i < 12; i++ {
		x, y, ok := robotspawn.RandomPoint(s.Env, rectangles)
		if !ok {
			break
		}
		if mathx.AbsInt(x-info.X) >= 40 || mathx.AbsInt(y-info.Y) >= 15 {
			return x, y
		}
	}
	if x, y, ok := robotspawn.RandomPoint(s.Env, rectangles); ok {
		return x, y
	}
	return info.X, info.Y
}
