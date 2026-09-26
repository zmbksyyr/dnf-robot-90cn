package scheduler

import (
	"context"
	"testing"

	robotcap "robot/internal/capability/robot"
)

type purgePortRecorder struct {
	planned  bool
	executed bool
	plan     robotcap.DangerousDeletePlan
}

func (p *purgePortRecorder) PlanDangerousDelete(context.Context, robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	p.planned = true
	return p.plan, nil
}

func (p *purgePortRecorder) ExecuteDangerousDelete(_ context.Context, plan robotcap.DangerousDeletePlan) (robotcap.DangerousDeleteResult, error) {
	p.executed = true
	return robotcap.DangerousDeleteResult{
		Mode: plan.Mode, UID: plan.UID, AccountCount: plan.AccountCount,
		CharacterCount: plan.CharacterCount, RegistryCount: plan.RegistryCount, Deleted: true,
	}, nil
}

func TestDangerousDeleteRoutesThroughBackendPurgerWithoutSchemaRepository(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	purger := &purgePortRecorder{plan: robotcap.DangerousDeletePlan{
		Mode: robotcap.DangerousDeleteModeUID, UID: 17000001,
		AccountCount: 1, CharacterCount: 1,
	}}
	m.SetBackendRobotPurger(purger)
	result, err := m.DangerousDelete(robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeUID, UID: 17000001})
	if err != nil || !purger.planned || !purger.executed || !result.Deleted || result.AccountCount != 1 {
		t.Fatalf("result=%+v planned=%t executed=%t err=%v", result, purger.planned, purger.executed, err)
	}
}
