package scheduler

import (
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
)

func TestOnlineManagedReleasesStructuralLockDuringConfirm(t *testing.T) {
	robots := []robotcap.Info{{UID: 101, CID: 1}}
	runtime := &slowLogoutRuntime{
		statuses: map[int]robotcap.RuntimeStatus{
			101: {UID: 101, StateName: robotcap.RuntimeStateRunning},
		},
	}

	manager := testRobotManagerWithConfig(t, "[system]\nactor_poll_ms = 20\n")
	manager.doll = runtime
	manager.robotState = robotstate.NewMemoryStore(robots)
	manager.autoEnabled = false

	supervisor := NewRobotSupervisor(manager, NewRobotRuntime(manager))
	manager.supervisor = supervisor
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	if !actor.AssignAndWait(101, time.Second) {
		t.Fatal("assign actor")
	}
	if !supervisor.ledger.TryLeaseUID(101, actor) {
		t.Fatal("lease uid")
	}
	manager.invalidateRuntimeStatusCache()
	deadline := time.Now().Add(5 * time.Second)
	for actor.Snapshot().State != actormodel.StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("actor did not settle: %+v", actor.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Block the runtime status provider: the confirmation loop (or the tick
	// refresh) then sits outside the structural lock, proving OnlineManaged has
	// moved past the locked dispatch phase.
	runtime.statusBlock = make(chan struct{})
	runtime.statusEntered = make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		_, _ = manager.OnlineManaged(robotcap.CommandRequest{UIDs: []int{101}})
		close(done)
	}()
	select {
	case <-runtime.statusEntered:
	case <-time.After(3 * time.Second):
		close(runtime.statusBlock)
		t.Fatal("runtime status was never requested during online confirmation")
	}

	released := false
	lockDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(lockDeadline) {
		if manager.mutationMu.TryLock() {
			manager.mutationMu.Unlock()
			released = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(runtime.statusBlock)
	if !released {
		t.Fatal("structural lock stayed held during online confirmation")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("online confirmation did not finish")
	}
}
