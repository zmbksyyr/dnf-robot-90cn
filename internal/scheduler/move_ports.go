package scheduler

import (
	"context"
	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func (m *RobotManager) moveService() robotaction.MoveService {
	return robotaction.MoveService{Env: moveActionEnv{manager: m}}
}

type moveActionEnv struct {
	manager *RobotManager
}

func (e moveActionEnv) DispatchMoveStep(info robotcap.Info, targetVillage, targetArea, targetX, targetY, step, steps, speed int, rc robotconfig.RuntimeConfig) error {
	x := info.X + (targetX-info.X)*step/steps
	y := info.Y + (targetY-info.Y)*step/steps
	command := shared.RuntimeMoveCommand{
		UID:      info.UID,
		Village:  targetVillage,
		Area:     targetArea,
		X:        x,
		Y:        y,
		MoveType: rc.MoveType,
		Speed:    speed,
	}
	var err error
	if e.manager.backendActions != nil {
		err = e.manager.backendActions.MoveTown(context.Background(), command)
	} else {
		err = e.manager.doll.Move(command)
	}
	if err != nil {
		return err
	}
	if step == steps && e.manager.positionWrites != nil {
		_ = e.manager.positionWrites.Queue(info, targetVillage, targetArea, targetX, targetY)
	}
	return nil
}

func (e moveActionEnv) LoadMapCatalog() []shared.MapCatalogItem {
	return e.manager.loadMapCatalog()
}

func (e moveActionEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e moveActionEnv) RuntimeStatus(uid int) (robotcap.RuntimeStatus, bool) {
	return e.manager.runtimeStatus(uid)
}

func (e moveActionEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMap()
}

func (e moveActionEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}
