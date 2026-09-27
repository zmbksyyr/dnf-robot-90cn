package scheduler

import (
	"context"
	"fmt"
	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func (m *RobotManager) DangerousDeleteDefaults() (int, int) {
	rc := m.loadRobotConfig()
	return rc.RobotUIDStart, rc.RobotUIDEnd
}

func (m *RobotManager) DangerousDelete(req robotcap.DangerousDeleteRequest) (robotcap.DangerousDeleteResult, error) {
	if err := m.requireBackendCapability(shared.CapabilityDangerousDelete); err != nil {
		return robotcap.DangerousDeleteResult{}, err
	}
	_, finishOperation, err := m.beginTrackedStructuralOperation("dangerous_delete", dangerousDeleteRequestScope(req))
	if err != nil {
		return robotcap.DangerousDeleteResult{}, err
	}
	var opErr error
	result := robotcap.DangerousDeleteResult{}
	defer func() {
		finishOperation(fmt.Sprintf("accounts=%d characters=%d registry=%d deleted=%v", result.AccountCount, result.CharacterCount, result.RegistryCount, result.Deleted), opErr)
	}()
	if m.backendRobotPurger == nil {
		opErr = fmt.Errorf("backend %s dangerous delete adapter is not configured", m.backendInfo.ID)
		return result, opErr
	}
	plan, err := m.backendRobotPurger.PlanDangerousDelete(context.Background(), req)
	if err != nil {
		opErr = err
		return robotcap.DangerousDeleteResult{}, err
	}
	result = robotcap.DangerousDeleteResult{
		Mode: plan.Mode, UID: plan.UID, CID: plan.CID, MinUID: plan.MinUID, MaxUID: plan.MaxUID,
		AccountCount: plan.AccountCount, CharacterCount: plan.CharacterCount, RegistryCount: plan.RegistryCount,
	}
	if len(plan.RegistryUIDs) > 0 {
		if _, err := m.SetAutoEnabled(false); err != nil {
			opErr = fmt.Errorf("disable automatic actions before dangerous delete: %w", err)
			return result, opErr
		}
		finishDelete := m.prepareRobotDelete(plan.RegistryUIDs, false)
		if finishDelete != nil {
			defer finishDelete()
		}
	}
	result, err = m.backendRobotPurger.ExecuteDangerousDelete(context.Background(), plan)
	if err != nil {
		opErr = err
		return result, err
	}
	for _, cid := range plan.CIDs {
		m.worldHornCache.Invalidate(cid)
	}
	m.invalidateLoginRepairs(plan.UIDs)
	return result, nil
}

func dangerousDeleteRequestScope(req robotcap.DangerousDeleteRequest) string {
	switch req.Mode {
	case robotcap.DangerousDeleteModeCID:
		return fmt.Sprintf("cid=%d", req.CID)
	case robotcap.DangerousDeleteModeUID:
		return fmt.Sprintf("uid=%d", req.UID)
	default:
		return fmt.Sprintf("range=%d-%d", req.MinUID, req.MaxUID)
	}
}
