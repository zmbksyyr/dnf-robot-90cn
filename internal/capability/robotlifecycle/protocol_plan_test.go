package robotlifecycle

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

func TestBuildProtocolRobotPlansReusesCommonNameAndSpawnPolicies(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 2, IDStart: 17000000, AccountPrefix: "acct", PasswordHash: "hash",
		Config:   robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:    robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}},
		Maps:     []shared.MapCatalogItem{{Village: 1, Area: 3, Level: 1, Use: true, XMin: 90, XMax: 110, YMin: 190, YMax: 210}},
		JobGrows: map[int][]int{1: {2}},
	})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// first grow 2 (transfer branch) + second grow 2 (awakening) = 0x22.
	if plans[0].Info.Job != 1 || plans[0].Info.Grow != 0x22 || plans[0].Info.Level != 50 || plans[0].Info.Area != 3 {
		t.Fatalf("first=%+v", plans[0])
	}
	if plans[0].Request.AccountName != "acct17000000" || plans[0].Request.RobotUID != 17000000 {
		t.Fatalf("request=%+v", plans[0].Request)
	}
	if plans[0].Info.Name == plans[1].Info.Name {
		t.Fatalf("duplicate names: %+v", plans)
	}
}

func TestBuildProtocolRobotPlansKeepsUntransferableJobsAtZero(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17000000, AccountPrefix: "acct",
		Config:   robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{9}, GrowTypes: []int{1, 2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:    robottemplate.NameTemplates{Common: []string{"Alpha"}},
		JobGrows: map[int][]int{0: {1, 2}},
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	// Job 9 has no released transfer branch, so an awakening must not be planned.
	if plans[0].Info.Grow != 0 {
		t.Fatalf("untransferable job grow=%d", plans[0].Info.Grow)
	}
}

func TestBuildProtocolRobotPlansBalancesSpawnAreas(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 2, IDStart: 17000000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{LevelMin: 80, LevelMax: 80, Jobs: []int{0}, GrowTypes: []int{0}, SpawnFallbackVillage: 1, SpawnArea: 0},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}},
		Maps: []shared.MapCatalogItem{
			{Village: 2, Area: 5, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
			{Village: 3, Area: 7, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	first, second := plans[0].Info, plans[1].Info
	if first.Village == second.Village && first.Area == second.Area {
		t.Fatalf("both plans landed in the same area: %d/%d and %d/%d", first.Village, first.Area, second.Village, second.Area)
	}
	if first.Village == 1 && first.Area == 0 {
		t.Fatalf("plan kept the fallback default instead of a catalog map: %+v", first)
	}
}

func TestBuildProtocolRobotPlansHonoursSeededOccupancy(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Count: 1, IDStart: 17000000, AccountPrefix: "acct",
		Config: robotconfig.RuntimeConfig{LevelMin: 80, LevelMax: 80, Jobs: []int{0}, GrowTypes: []int{0}, SpawnFallbackVillage: 1, SpawnArea: 0},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha"}},
		Maps: []shared.MapCatalogItem{
			{Village: 2, Area: 5, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
			{Village: 3, Area: 7, Level: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		},
		Locations:   []shared.MapLocation{{Village: 2, Area: 5, X: 1, Y: 1}},
		RandIntn:    func(int) int { return 0 },
		RandBetween: func(min, max int) int { return min },
	})
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if plans[0].Info.Village != 3 || plans[0].Info.Area != 7 {
		t.Fatalf("occupied area was not avoided: %+v", plans[0].Info)
	}
}
