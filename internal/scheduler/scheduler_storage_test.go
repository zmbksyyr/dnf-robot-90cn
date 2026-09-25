package scheduler

import (
	"testing"

	robotstate "robot/internal/capability/robotstate"
)

func TestAdapterRobotStateDoesNotRequireFallbackSchema(t *testing.T) {
	manager := NewRobotManager(nil, nil, nil)
	if err := manager.ensureSchedulerStorage(); err == nil {
		t.Fatal("missing adapter storage unexpectedly passed schema initialization")
	}
	manager.SetRobotStateDirectory(robotstate.NewMemoryStore(nil))
	if err := manager.ensureSchedulerStorage(); err != nil {
		t.Fatalf("adapter robot state required fallback schema: %v", err)
	}
}
