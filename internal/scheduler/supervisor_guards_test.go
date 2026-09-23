package scheduler

import (
	"errors"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

func TestSupervisorUsesInjectedGameCommandGate(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	manager.SetGameCommandGate(rejectingGameCommandGate{err: errors.New("backend runtime unavailable")})
	manager.autoEnabled = true

	supervisor := NewRobotSupervisor(manager, NewRobotRuntime(manager))
	supervisor.handleAutoGuards(time.Now(), robotconfig.RuntimeConfig{
		AutoActions:                 true,
		SchedulerMetricsIntervalSec: 1,
	}, adaptiveSchedulerSignals{})
	if supervisor.nextGameGateLog.IsZero() {
		t.Fatal("supervisor did not apply the injected game command gate")
	}
}
