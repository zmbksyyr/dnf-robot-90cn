package scheduler

import (
	"testing"

	robotstate "robot/internal/capability/robotstate"
)

func TestSimulatorRobotStateDoesNotRequireNativeSchedulerSchema(t *testing.T) {
	manager := NewRobotManager(nil, nil, nil)
	if err := manager.ensureSchedulerStorage(); err == nil {
		t.Fatal("missing native repository unexpectedly passed schema initialization")
	}
	manager.SetRobotStateDirectory(robotstate.NewMemoryStore(nil))
	if err := manager.ensureSchedulerStorage(); err != nil {
		t.Fatalf("robot-owned scheduler storage required native schema: %v", err)
	}
}
