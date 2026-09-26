package robotlifecycle

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

func TestBuildProtocolRobotPlansReusesCommonNameAndSpawnPolicies(t *testing.T) {
	plans, err := BuildProtocolRobotPlans(ProtocolPlanOptions{
		Backend: shared.BackendID("test"), Count: 2, IDStart: 17000000, AccountPrefix: "acct", PasswordHash: "hash",
		Config: robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}},
		Maps:   []shared.MapCatalogItem{{Village: 1, Area: 3, Level: 1, Use: true, XMin: 90, XMax: 110, YMin: 190, YMax: 210}},
	})
	if err != nil || len(plans) != 2 {
		t.Fatalf("plans=%+v err=%v", plans, err)
	}
	if plans[0].Info.Job != 1 || plans[0].Info.Grow != 2 || plans[0].Info.Level != 50 || plans[0].Info.Area != 3 {
		t.Fatalf("first=%+v", plans[0])
	}
	if plans[0].Request.AccountName != "acct17000000" || plans[0].Request.RobotUID != 17000000 {
		t.Fatalf("request=%+v", plans[0].Request)
	}
	if plans[0].Info.Name == plans[1].Info.Name {
		t.Fatalf("duplicate names: %+v", plans)
	}
}
