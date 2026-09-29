package scheduler

import (
	"sync/atomic"
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
)

func TestSupervisorStopBeforeStartReturns(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	done := make(chan error, 1)
	go func() { done <- supervisor.StopWithError() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop before start blocked")
	}
}

func TestSupervisorStartIsIdempotent(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	supervisor.Start()
	supervisor.Start()
	if err := supervisor.StopWithError(); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorStopReapsActorsThatExitAfterTimeout(t *testing.T) {
	release := make(chan struct{})
	runtime := &blockingActorStopRuntime{
		started: make(chan int, 1),
		release: release,
	}
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	supervisor.shutdownTimeout = 50 * time.Millisecond
	supervisor.shutdownForceGrace = 20 * time.Millisecond
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("assign late-exit actor")
	}
	supervisor.Start()

	if err := supervisor.StopWithError(); err == nil {
		t.Fatal("initial stop should report the blocked actor")
	}
	close(release)
	select {
	case <-actor.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("actor did not exit after runtime release")
	}
	if err := supervisor.StopWithError(); err != nil {
		t.Fatalf("retry stop did not reap completed actor: %v", err)
	}
	if supervisor.ledger.HasUID(uid) || len(supervisor.ledger.ActorPointers()) != 0 {
		t.Fatal("retry stop retained a completed actor or uid lease")
	}
}

func TestManagerStartReplacesCompletedTimedOutSupervisor(t *testing.T) {
	release := make(chan struct{})
	runtime := &blockingActorStopRuntime{
		started: make(chan int, 1),
		release: release,
	}
	manager := testRobotManagerWithConfig(t, "")
	old := NewRobotSupervisor(manager, runtime)
	old.shutdownTimeout = 50 * time.Millisecond
	old.shutdownForceGrace = 20 * time.Millisecond
	actor := ensureSupervisorActors(t, old, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) || !old.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("assign restart actor")
	}
	manager.supervisor = old
	old.Start()

	if err := manager.stopAutoActions(); err == nil {
		t.Fatal("initial stop should report the blocked actor")
	}
	close(release)
	select {
	case <-actor.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("actor did not exit after runtime release")
	}

	manager.StartAutoActions()
	manager.autoMu.Lock()
	replacement := manager.supervisor
	manager.autoMu.Unlock()
	if replacement == nil || replacement == old {
		t.Fatal("start did not replace the completed timed-out supervisor")
	}
	if err := manager.stopAutoActions(); err != nil {
		t.Fatalf("stop replacement supervisor: %v", err)
	}
}

type unconfirmedReleaseRuntime struct {
	actorTestRuntime
	active atomic.Bool
}

func newUnconfirmedReleaseRuntime() *unconfirmedReleaseRuntime {
	runtime := &unconfirmedReleaseRuntime{}
	runtime.active.Store(true)
	return runtime
}

func (r *unconfirmedReleaseRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	if !r.active.Load() {
		return robotcap.RuntimeStatus{}, false
	}
	return robotcap.RuntimeStatus{UID: uid, StateName: robotcap.RuntimeStateRunning}, true
}

func (r *unconfirmedReleaseRuntime) IsActive(int) bool {
	return r.active.Load()
}

func (r *unconfirmedReleaseRuntime) Logout(uid int) robotcap.ActionResult {
	if !r.active.Load() {
		return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateClosed}
	}
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStatePending}
}

func TestRecycleRetainsLeaseUntilRuntimeCloseIsConfirmed(t *testing.T) {
	runtime := newUnconfirmedReleaseRuntime()
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("assign recycle actor")
	}

	supervisor.recycleActorUID(actor, actormodel.Status{Snapshot: actormodel.Snapshot{
		SlotID: actor.SlotIDValue(),
		UID:    uid,
	}})
	if !supervisor.ledger.HasUID(uid) {
		t.Fatal("unconfirmed release dropped the uid lease")
	}
	if supervisor.ledger.BlockedCount() != 0 {
		t.Fatal("unconfirmed release moved the uid into cleanup")
	}
	if snapshot := actor.Snapshot(); snapshot.UID != uid || snapshot.State != actormodel.StateReleasing {
		t.Fatalf("unconfirmed release snapshot = %+v", snapshot)
	}

	runtime.active.Store(false)
}
