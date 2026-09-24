package s4a21

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotlifecycle "robot/internal/capability/robotlifecycle"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	foundationlog "robot/internal/foundation/log"
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
	options := robotlifecycle.ProtocolPlanOptions{
		Backend: shared.BackendS4A21, Count: request.Count, IDStart: idStart, AccountPrefix: c.AccountPrefix,
		PasswordHash: c.PasswordHash, Config: c.Config, Names: c.Names, Maps: c.Maps, RandIntn: c.RandIntn, RandBetween: c.RandBetween,
		NameExists: nameExists,
	}
	plans, err := robotlifecycle.BuildProtocolRobotPlans(options)
	if err != nil {
		return nil, err
	}
	reservedNames := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		reservedNames[robottemplate.DBName(plan.Info.Name)] = struct{}{}
	}
	nameTaken := func(name string) bool {
		if nameExists(name) {
			return true
		}
		_, exists := reservedNames[robottemplate.DBName(name)]
		return exists
	}
	nextID := idStart + len(plans)
	const maxConflictSkips = 64
	conflictSkips := 0
	robots := make([]robotcap.Info, 0, request.Count)
	for i := 0; i < len(plans) && len(robots) < request.Count; i++ {
		plan := plans[i]
		batchID := fmt.Sprintf("s4a21-%d-%d-%d", time.Now().UnixNano(), creatorBatchSequence.Add(1), i)
		result, provisionErr := robotlifecycle.ProvisionProtocolBatch(
			ctx, c.BatchStore, c.IdentityStore, c.Provisioner, batchID,
			shared.BackendS4A21, []shared.ProvisionCharacterRequest{plan.Request},
		)
		if provisionErr != nil {
			var conflict *AccountRosterConflictError
			if errors.As(provisionErr, &conflict) {
				conflictSkips++
				foundationlog.Robotf("S4A21_ACCOUNT_ADOPTION_SKIPPED account=%s characters=%d uid=%d\n", conflict.Account, conflict.Count, plan.Info.UID)
				if conflictSkips > maxConflictSkips {
					return robots, fmt.Errorf("S4A21 account recovery exceeded %d roster conflicts: %w", maxConflictSkips, provisionErr)
				}
				replacementOptions := options
				replacementOptions.Count = 1
				replacementOptions.IDStart = nextID
				replacementOptions.NameExists = nameTaken
				replacements, buildErr := robotlifecycle.BuildProtocolRobotPlans(replacementOptions)
				if buildErr != nil {
					return robots, buildErr
				}
				nextID++
				reservedNames[robottemplate.DBName(replacements[0].Info.Name)] = struct{}{}
				plans = append(plans, replacements[0])
				continue
			}
			return robots, provisionErr
		}
		if len(result.Results) != 1 || !result.Results[0].Created {
			return robots, fmt.Errorf("S4A21 provision uid=%d returned no usable character", plan.Info.UID)
		}
		provisioned := result.Results[0]
		info := plan.Info
		plannedLevel := info.Level
		info.Name = provisioned.CharacterName
		if provisioned.ProfileKnown {
			info.Job, info.Grow, info.Level = provisioned.Job, provisioned.Grow, provisioned.Level
		}
		if profiles, ok := c.Profiles.(CharacterProfileAdapter); ok && !provisioned.Reused {
			info, err = profiles.ApplyPlannedCharacterLevel(ctx, plan.Request.AccountName, info, plannedLevel)
			if err != nil {
				return robots, fmt.Errorf("apply S4A21 planned level uid=%d: %w", info.UID, err)
			}
		} else if c.Profiles != nil && !provisioned.Reused {
			info, err = c.Profiles.ResolveCharacterProfile(ctx, plan.Request.AccountName, info)
			if err != nil {
				return robots, fmt.Errorf("resolve S4A21 profile uid=%d: %w", info.UID, err)
			}
		}
		if c.Loadouts != nil {
			if err := c.Loadouts.ApplyCharacterLoadout(ctx, plan.Request.AccountName, info); err != nil {
				return robots, fmt.Errorf("apply S4A21 loadout uid=%d: %w", info.UID, err)
			}
		}
		if err := c.RobotCatalog.RegisterRobots(ctx, []robotcap.Info{info}); err != nil {
			return robots, fmt.Errorf("register S4A21 robot directory uid=%d: %w", info.UID, err)
		}
		robots = append(robots, info)
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
