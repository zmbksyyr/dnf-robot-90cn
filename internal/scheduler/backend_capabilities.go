package scheduler

import (
	"fmt"

	"robot/internal/shared"
)

// requireBackendCapability keeps unsupported operations from entering the
// shared actor workflow. Native managers leave backendRobotBackend empty and
// retain their existing behavior.
func (m *RobotManager) requireBackendCapability(capability shared.BackendCapability) error {
	if m == nil || m.backendRobotBackend == "" || m.backendRobotBackend == shared.BackendNative {
		return nil
	}
	for _, info := range shared.KnownBackends() {
		if info.ID == m.backendRobotBackend {
			return info.Require(capability)
		}
	}
	return fmt.Errorf("unknown backend %q", m.backendRobotBackend)
}
