package scheduler

import (
	"errors"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func TestSimulatorStoreIsRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	_, err := m.StoreManaged(robotcap.CommandRequest{Count: 1})
	if err == nil {
		t.Fatal("simulator store unexpectedly entered actor workflow")
	}
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendS4A21 || unsupported.Operation != shared.CapabilityStore {
		t.Fatalf("error = %v, want S4A21 store unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}
