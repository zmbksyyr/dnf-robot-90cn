package scheduler

import (
	"testing"
)

func TestStartRuntimeFileWatcherWiresAndStopsPoller(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	manager.StartRuntimeFileWatcher()
	if manager.runtimeFilePoller == nil {
		t.Fatal("runtime file poller was not started")
	}
	if !manager.runtimeFilesWatched.Load() {
		t.Fatal("runtime file watcher flag was not set")
	}

	// Starting twice must keep the same poller.
	first := manager.runtimeFilePoller
	manager.StartRuntimeFileWatcher()
	if manager.runtimeFilePoller != first {
		t.Fatal("second start replaced the running poller")
	}

	if err := manager.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if manager.runtimeFilePoller != nil {
		t.Fatal("shutdown left the runtime file poller running")
	}
}
