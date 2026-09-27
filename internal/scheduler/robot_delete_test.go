package scheduler

import (
	"testing"

	"robot/internal/foundation/config"
)

// prepareRobotDelete guards adapter deletes: robots must be marked cleanup
// pending while the adapter removes their rows, and the mark must be cleared
// afterwards even when no actor registry exists.
func TestPrepareRobotDeleteMarksAndClearsCleanupPending(t *testing.T) {
	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, nil)
	t.Cleanup(func() { _ = manager.Shutdown() })

	finish := manager.prepareRobotDelete([]int{17000001, 17000002}, true)
	if !manager.isCleanupPending(17000001) || !manager.isCleanupPending(17000002) {
		t.Fatalf("cleanup pending not marked before delete")
	}
	finish()
	if manager.isCleanupPending(17000001) || manager.isCleanupPending(17000002) {
		t.Fatalf("cleanup pending not cleared after delete")
	}
}
