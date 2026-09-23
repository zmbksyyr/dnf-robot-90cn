package scheduler

import (
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
	plan, err := m.schemaRepo().DangerousDeletePlan(req)
	if err != nil {
		opErr = err
		return robotcap.DangerousDeleteResult{}, err
	}
	result = dangerousDeleteResult(plan)
	if len(plan.RegistryUIDs) > 0 {
		if _, err := m.SetAutoEnabled(false); err != nil {
			opErr = fmt.Errorf("disable automatic actions before dangerous delete: %w", err)
			return result, opErr
		}
		finishDelete := (lifecycleCleanupEnv{manager: m}).PrepareDelete(plan.RegistryUIDs)
		if finishDelete != nil {
			defer finishDelete()
		}
	}
	if plan.Mode == robotcap.DangerousDeleteModeCID {
		if err := m.schemaRepo().DeleteCharacterAtomic(plan.UID, plan.CID, len(plan.RegistryUIDs) > 0); err != nil {
			opErr = err
			return result, err
		}
		m.worldHornCache.Invalidate(plan.CID)
		m.invalidateLoginRepairs([]int{plan.UID})
	} else {
		if err := m.schemaRepo().BatchDeleteRobotData(plan.UIDs, plan.CIDs); err != nil {
			opErr = err
			return result, err
		}
		for _, cid := range plan.CIDs {
			m.worldHornCache.Invalidate(cid)
		}
		m.invalidateLoginRepairs(plan.UIDs)
	}
	result.Deleted = true
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

func dangerousDeleteResult(plan robotcap.DangerousDeletePlan) robotcap.DangerousDeleteResult {
	return robotcap.DangerousDeleteResult{
		Mode: plan.Mode, UID: plan.UID, CID: plan.CID, MinUID: plan.MinUID, MaxUID: plan.MaxUID,
		AccountCount: plan.AccountCount, CharacterCount: plan.CharacterCount, RegistryCount: plan.RegistryCount,
	}
}

func dangerousDeleteScope(plan robotcap.DangerousDeletePlan) string {
	switch plan.Mode {
	case robotcap.DangerousDeleteModeCID:
		return fmt.Sprintf("cid=%d", plan.CID)
	case robotcap.DangerousDeleteModeUID:
		return fmt.Sprintf("uid=%d", plan.UID)
	default:
		return fmt.Sprintf("range=%d-%d", plan.MinUID, plan.MaxUID)
	}
}
