package s4a21

import (
	"context"
	"fmt"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotlifecycle "robot/internal/capability/robotlifecycle"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

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
	plans, err := robotlifecycle.BuildProtocolRobotPlans(robotlifecycle.ProtocolPlanOptions{
		Backend: shared.BackendS4A21, Count: request.Count, IDStart: c.IDStart, AccountPrefix: c.AccountPrefix,
		PasswordHash: c.PasswordHash, Config: c.Config, Names: c.Names, Maps: c.Maps, RandIntn: c.RandIntn, RandBetween: c.RandBetween,
	})
	if err != nil {
		return nil, err
	}
	requests := make([]shared.ProvisionCharacterRequest, len(plans))
	for i := range plans {
		requests[i] = plans[i].Request
	}
	batchID := fmt.Sprintf("s4a21-%d", time.Now().UnixNano())
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

var _ interface {
	CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error)
} = RobotCreator{}
