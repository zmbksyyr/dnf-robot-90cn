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
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

var creatorBatchSequence atomic.Uint64
var robotCreationMu lockhub.Locker

type RobotCreator struct {
	Provisioner   shared.BatchCharacterProvisioner
	BatchStore    robotstate.BatchDirectory
	IdentityStore robotstate.IdentityDirectory
	RobotCatalog  robotstate.RobotCatalog
	Config        robotconfig.RuntimeConfig
	Names         robottemplate.NameTemplates
	Maps          []shared.MapCatalogItem
	JobGrows      map[int][]int
	AccountPrefix string
	PasswordHash  string
	IDStart       int
	RandIntn      func(int) int
	RandBetween   func(int, int) int
	Loadouts      CharacterLoadoutApplier
	Profiles      CharacterProfileReader
}

func (c RobotCreator) CreateRobots(ctx context.Context, request robotcap.CreateRequest) ([]robotcap.Info, error) {
	// UID holes and names are derived from the in-memory directory. Keep the
	// complete reservation/provision/register sequence atomic so concurrent
	// scheduler fills cannot plan the same identities.
	robotCreationMu.Lock()
	defer robotCreationMu.Unlock()

	if c.Provisioner == nil || c.BatchStore == nil || c.IdentityStore == nil || c.RobotCatalog == nil {
		return nil, fmt.Errorf("S4A21 creator dependencies are incomplete")
	}
	if c.IDStart <= 0 {
		return nil, fmt.Errorf("S4A21 creator id start is required")
	}
	if request.Count <= 0 {
		return nil, fmt.Errorf("S4A21 create count must be positive")
	}
	const maxConflictSkips = 64
	candidateIDs, nameExists, err := c.nextProvisioningIDs(ctx, request.Count+maxConflictSkips)
	if err != nil {
		return nil, err
	}
	if len(candidateIDs) < request.Count {
		return nil, fmt.Errorf("S4A21 robot UID range has %d free identities, need %d", len(candidateIDs), request.Count)
	}
	reservedNames := make(map[string]struct{}, request.Count+maxConflictSkips)
	nameTaken := func(name string) bool {
		if nameExists(name) {
			return true
		}
		_, exists := reservedNames[robottemplate.DBName(name)]
		return exists
	}
	buildPlan := func(uid int) (robotlifecycle.ProtocolRobotPlan, error) {
		plans, err := robotlifecycle.BuildProtocolRobotPlans(robotlifecycle.ProtocolPlanOptions{
			Backend: BackendID, Count: 1, IDStart: uid, AccountPrefix: c.AccountPrefix,
			PasswordHash: c.PasswordHash, Config: c.Config, Names: c.Names, Maps: c.Maps, JobGrows: c.JobGrows, RandIntn: c.RandIntn, RandBetween: c.RandBetween,
			NameExists: nameTaken,
		})
		if err != nil {
			return robotlifecycle.ProtocolRobotPlan{}, err
		}
		reservedNames[robottemplate.DBName(plans[0].Info.Name)] = struct{}{}
		return plans[0], nil
	}
	plans := make([]robotlifecycle.ProtocolRobotPlan, 0, request.Count+maxConflictSkips)
	for _, uid := range candidateIDs[:request.Count] {
		plan, err := buildPlan(uid)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	nextCandidate := request.Count
	conflictSkips := 0
	robots := make([]robotcap.Info, 0, request.Count)
	for i := 0; i < len(plans) && len(robots) < request.Count; i++ {
		plan := plans[i]
		batchID := fmt.Sprintf("s4a21-%d-%d-%d", time.Now().UnixNano(), creatorBatchSequence.Add(1), i)
		result, provisionErr := robotlifecycle.ProvisionProtocolBatch(
			ctx, c.BatchStore, c.IdentityStore, c.Provisioner, batchID,
			BackendID, []shared.ProvisionCharacterRequest{plan.Request},
		)
		if provisionErr != nil {
			var conflict *AccountRosterConflictError
			if errors.As(provisionErr, &conflict) {
				conflictSkips++
				foundationlog.Robotf("S4A21_ACCOUNT_ADOPTION_SKIPPED account=%s characters=%d uid=%d\n", conflict.Account, conflict.Count, plan.Info.UID)
				if conflictSkips > maxConflictSkips {
					return robots, fmt.Errorf("S4A21 account recovery exceeded %d roster conflicts: %w", maxConflictSkips, provisionErr)
				}
				if nextCandidate >= len(candidateIDs) {
					return robots, fmt.Errorf("S4A21 robot UID range exhausted after account conflict: %w", provisionErr)
				}
				replacement, buildErr := buildPlan(candidateIDs[nextCandidate])
				if buildErr != nil {
					return robots, buildErr
				}
				nextCandidate++
				plans = append(plans, replacement)
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
		plannedGrow := info.Grow
		info.Name = provisioned.CharacterName
		if provisioned.ProfileKnown {
			info.Job, info.Grow, info.Level = provisioned.Job, provisioned.Grow, provisioned.Level
		}
		loadoutApplied := false
		if initializer, ok := c.Profiles.(CharacterInitializer); ok && !provisioned.Reused {
			info, err = initializer.InitializeCharacter(ctx, plan.Request.AccountName, info, plannedLevel, plannedGrow)
			if err != nil {
				return robots, fmt.Errorf("initialize S4A21 character uid=%d: %w", info.UID, err)
			}
			loadoutApplied = true
		} else if profiles, ok := c.Profiles.(CharacterProfileAdapter); ok && !provisioned.Reused {
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
		if c.Loadouts != nil && !loadoutApplied {
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

func (c RobotCreator) nextProvisioningIDs(ctx context.Context, count int) ([]int, func(string) bool, error) {
	var existing []robotcap.Info
	if directory, ok := c.RobotCatalog.(robotstate.Directory); ok {
		robots, err := directory.SelectRobots(ctx, robotcap.CommandRequest{Count: 1 << 30})
		if err != nil {
			return nil, nil, fmt.Errorf("read S4A21 robot directory: %w", err)
		}
		existing = robots
	}
	usedUIDs := make(map[int]struct{}, len(existing))
	usedNames := make(map[string]struct{}, len(existing))
	for _, robot := range existing {
		usedUIDs[robot.UID] = struct{}{}
		usedNames[robottemplate.DBName(robot.Name)] = struct{}{}
	}
	end := c.Config.RobotUIDEnd
	if end < c.IDStart {
		end = c.IDStart + count - 1
	}
	ids := make([]int, 0, count)
	for uid := c.IDStart; uid <= end && len(ids) < count; uid++ {
		if _, used := usedUIDs[uid]; !used {
			ids = append(ids, uid)
		}
	}
	return ids, func(name string) bool {
		_, exists := usedNames[robottemplate.DBName(name)]
		return exists
	}, nil
}

var _ interface {
	CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error)
} = RobotCreator{}
