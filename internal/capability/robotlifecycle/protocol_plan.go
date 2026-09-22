package robotlifecycle

import (
	"fmt"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

type ProtocolPlanOptions struct {
	Backend       shared.BackendID
	Count         int
	IDStart       int
	AccountPrefix string
	PasswordHash  string
	Config        robotconfig.RuntimeConfig
	Names         robottemplate.NameTemplates
	Maps          []shared.MapCatalogItem
	NameExists    func(string) bool
	RandIntn      func(int) int
	RandBetween   func(int, int) int
}

type ProtocolRobotPlan struct {
	Info    robotcap.Info
	Request shared.ProvisionCharacterRequest
}

func BuildProtocolRobotPlans(options ProtocolPlanOptions) ([]ProtocolRobotPlan, error) {
	if options.Count <= 0 || options.Count > 1000 {
		return nil, fmt.Errorf("protocol plan count must be between 1 and 1000")
	}
	if options.IDStart <= 0 {
		return nil, fmt.Errorf("protocol plan id start must be positive")
	}
	if len(options.Config.Jobs) == 0 {
		return nil, fmt.Errorf("protocol plan requires at least one job")
	}
	if len(options.Config.GrowTypes) == 0 {
		options.Config.GrowTypes = []int{0}
	}
	if options.AccountPrefix == "" {
		options.AccountPrefix = "robot"
	}
	levels := options.Config.LevelMin
	if levels <= 0 {
		levels = 1
	}
	levelMax := options.Config.LevelMax
	if levelMax < levels {
		levelMax = levels
	}
	plans := make([]ProtocolRobotPlan, 0, options.Count)
	used := make(map[string]struct{}, options.Count)
	env := protocolPlanRandom{randIntn: options.RandIntn, randBetween: options.RandBetween}
	for index := 0; index < options.Count; index++ {
		uid := options.IDStart + index
		job := chooseInt(options.Config.Jobs, options.RandIntn)
		grow := chooseInt(options.Config.GrowTypes, options.RandIntn)
		level := env.RandBetween(levels, levelMax)
		name := robottemplate.AllocateName(uid, job, grow, used, options.Config, options.Names, options.NameExists, options.RandBetween)
		info := robotcap.Info{UID: uid, Name: name, Level: level, Job: job, Grow: grow, Port: 0, Village: options.Config.SpawnFallbackVillage, Area: options.Config.SpawnArea, X: options.Config.SpawnXMin, Y: options.Config.SpawnYMin}
		if mp, ok := robotspawn.RandomMap(env, options.Maps, level); ok {
			info.Village, info.Area = mp.Village, mp.Area
			if x, y, pointOK := robotspawn.RandomPointInMap(env, mp); pointOK {
				info.X, info.Y = x, y
			}
		}
		plans = append(plans, ProtocolRobotPlan{Info: info, Request: shared.ProvisionCharacterRequest{AccountName: fmt.Sprintf("%s%d", options.AccountPrefix, uid), PasswordHash: options.PasswordHash, CharacterName: name, Job: job, RobotUID: uid}})
	}
	return plans, nil
}

type protocolPlanRandom struct {
	randIntn    func(int) int
	randBetween func(int, int) int
}

func (protocolPlanRandom) FollowAccountVillage(string) (int, bool, error) { return 0, false, nil }

func (r protocolPlanRandom) RandIntn(n int) int {
	if n <= 0 || r.randIntn == nil {
		return 0
	}
	v := r.randIntn(n)
	if v < 0 || v >= n {
		return 0
	}
	return v
}
func (r protocolPlanRandom) RandBetween(min, max int) int {
	if max <= min || r.randBetween == nil {
		return min
	}
	return r.randBetween(min, max)
}
func chooseInt(values []int, randIntn func(int) int) int {
	if len(values) == 0 {
		return 0
	}
	if randIntn == nil {
		return values[0]
	}
	index := randIntn(len(values))
	if index < 0 || index >= len(values) {
		index = 0
	}
	return values[index]
}
