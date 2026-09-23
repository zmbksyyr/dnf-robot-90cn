package scheduler

import "robot/internal/shared"

// requireBackendCapability keeps unsupported operations from entering the
// shared actor workflow. Legacy tests may leave backendInfo empty and retain
// the original all-capabilities behavior.
func (m *RobotManager) requireBackendCapability(capability shared.BackendCapability) error {
	if m == nil || m.backendInfo.ID == "" {
		return nil
	}
	return m.backendInfo.Require(capability)
}

func (m *RobotManager) supportsBackendCapability(capability shared.BackendCapability) bool {
	return m == nil || m.backendInfo.ID == "" || m.backendInfo.Supports(capability)
}

func (m *RobotManager) RequireCapability(capability shared.BackendCapability) error {
	return m.requireBackendCapability(capability)
}
