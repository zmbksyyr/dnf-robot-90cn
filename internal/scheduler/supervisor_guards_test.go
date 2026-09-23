package scheduler

import (
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestSupervisorSkipsNativeKeypairGuardForSimulatorBackend(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	manager.SetBackendRobotCreator(shared.BackendS4A21, nil)
	manager.SetNativeKeypairRequired(false)
	manager.autoEnabled = true
	if status := manager.KeypairStatus(); status.GameValid || status.Error == "" {
		t.Fatalf("test requires unsupported simulator keypair status, got %+v", status)
	}

	supervisor := NewRobotSupervisor(manager, NewRobotRuntime(manager))
	supervisor.handleAutoGuards(time.Now(), robotconfig.RuntimeConfig{
		AutoActions:                 true,
		SchedulerMetricsIntervalSec: 1,
	}, adaptiveSchedulerSignals{})
	if !supervisor.nextKeyLog.IsZero() {
		t.Fatalf("simulator auto scheduling entered native keypair guard at %s", supervisor.nextKeyLog)
	}
}
