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
	Loadouts      CharacterLoadoutApplier
	Profiles      CharacterProfileReader
}

func (c RobotCreator) CreateRobots(ctx context.Context, request robotcap.CreateRequest) ([]robotcap.Info, error) {
	if c.Provisioner == nil || c.BatchStore == nil || c.IdentityStore == nil || c.RobotCatalog == nil {
		return nil, fmt.Errorf("S4A21 creator dependencies are incomplete")
	}
	if c.IDStart <= 0 {
		return nil, fmt.Errorf("S4A21 creator id start is required")
	}
	idStart, nameExists, err := c.nextProvisioningRange(ctx)
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
	robots := make([]robotcap.Info, 0, len(plans))
	for i, plan := range plans {
		batchID := fmt.Sprintf("s4a21-%d-%d-%d", time.Now().UnixNano(), creatorBatchSequence.Add(1), i)
		result, provisionErr := robotlifecycle.ProvisionProtocolBatch(
			ctx, c.BatchStore, c.IdentityStore, c.Provisioner, batchID,
			shared.BackendS4A21, []shared.ProvisionCharacterRequest{plan.Request},
		)
		if provisionErr != nil {
			return robots, provisionErr
		}
		if len(result.Results) != 1 || !result.Results[0].Created {
			return robots, fmt.Errorf("S4A21 provision uid=%d returned no created character", plan.Info.UID)
		}
		provisioned := result.Results[0]
		info := plan.Info
		info.Name = provisioned.CharacterName
		if provisioned.ProfileKnown {
			info.Job, info.Grow, info.Level = provisioned.Job, provisioned.Grow, provisioned.Level
		} else if c.Profiles != nil {
			info, err = c.Profiles.ResolveCharacterProfile(ctx, plan.Request.AccountName, info)
			if err != nil {
				return robots, fmt.Errorf("resolve S4A21 profile uid=%d: %w", info.UID, err)
			}
		}
		if err := c.RobotCatalog.RegisterRobots(ctx, []robotcap.Info{info}); err != nil {
			return robots, fmt.Errorf("register S4A21 robot directory uid=%d: %w", info.UID, err)
		}
		robots = append(robots, info)
		if c.Loadouts != nil {
			if err := c.Loadouts.ApplyCharacterLoadout(ctx, plan.Request.AccountName, info); err != nil {
				return robots, fmt.Errorf("apply S4A21 loadout uid=%d: %w", info.UID, err)
			}
		}
	}
	return robots, nil
}

func (c RobotCreator) nextProvisioningRange(ctx context.Context) (int, func(string) bool, error) {
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
