package s4a21

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotlifecycle "robot/internal/capability/robotlifecycle"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

var creatorBatchSequence atomic.Uint64

type RobotCreator struct {
	Provisioner   shared.BatchCharacterProvisioner
	BatchStore    robotstate.BatchDirectory
	IdentityStore robotstate.IdentityDirectory
	RobotCatalog  robotstate.RobotCatalog
	Config        robotconfig.RuntimeConfig
	Names         robottemplate.NameTemplates
	Maps          []shared.MapCatalogItem
	AccountPrefix string
	PasswordHash  string
	IDStart       int
	RandIntn      func(int) int
	RandBetween   func(int, int) int
}

func (c RobotCreator) CreateRobots(ctx context.Context, request robotcap.CreateRequest) ([]robotcap.Info, error) {
	if c.Provisioner == nil || c.BatchStore == nil || c.IdentityStore == nil || c.RobotCatalog == nil {
		return nil, fmt.Errorf("S4A21 creator dependencies are incomplete")
	}
	if c.IDStart <= 0 {
		return nil, fmt.Errorf("S4A21 creator id start is required")
	}
	idStart, nameExists, err := c.nextProvisioningRange(ctx, request.Count)
	if err != nil {
		return nil, err
	}
	plans, err := robotlifecycle.BuildProtocolRobotPlans(robotlifecycle.ProtocolPlanOptions{
		Backend: shared.BackendS4A21, Count: request.Count, IDStart: idStart, AccountPrefix: c.AccountPrefix,
		PasswordHash: c.PasswordHash, Config: c.Config, Names: c.Names, Maps: c.Maps, RandIntn: c.RandIntn, RandBetween: c.RandBetween,
		NameExists: nameExists,
	})
	if err != nil {
		return nil, err
	}
	requests := make([]shared.ProvisionCharacterRequest, len(plans))
	for i := range plans {
		requests[i] = plans[i].Request
	}
	batchID := fmt.Sprintf("s4a21-%d-%d", time.Now().UnixNano(), creatorBatchSequence.Add(1))
	result, err := robotlifecycle.ProvisionProtocolBatch(ctx, c.BatchStore, c.IdentityStore, c.Provisioner, batchID, shared.BackendS4A21, requests)
	if err != nil {
		return nil, err
	}
	robots := make([]robotcap.Info, 0, len(result.Results))
	for i, provisioned := range result.Results {
		if i >= len(plans) {
			break
		}
		info := plans[i].Info
		info.Name = provisioned.CharacterName
		robots = append(robots, info)
	}
	if err := c.RobotCatalog.RegisterRobots(ctx, robots); err != nil {
		return nil, fmt.Errorf("register S4A21 robot directory: %w", err)
	}
	return robots, nil
}

func (c RobotCreator) nextProvisioningRange(ctx context.Context, count int) (int, func(string) bool, error) {
	idStart := c.IDStart
	var existing []robotcap.Info
	if directory, ok := c.RobotCatalog.(robotstate.Directory); ok {
		robots, err := directory.SelectRobots(ctx, robotcap.CommandRequest{Count: 1 << 30})
		if err != nil {
			return 0, nil, fmt.Errorf("read S4A21 robot directory: %w", err)
		}
		existing = robots
		maxUID := idStart - 1
		for _, robot := range robots {
			if robot.UID >= idStart && robot.UID > maxUID {
				maxUID = robot.UID
			}
		}
		if maxUID >= idStart {
			idStart = maxUID + 1
		}
	}
	usedNames := make(map[string]struct{}, len(existing))
	for _, robot := range existing {
		usedNames[robottemplate.DBName(robot.Name)] = struct{}{}
	}
	return idStart, func(name string) bool {
		_, exists := usedNames[robottemplate.DBName(name)]
		return exists
	}, nil
}

var _ interface {
	CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error)
} = RobotCreator{}
