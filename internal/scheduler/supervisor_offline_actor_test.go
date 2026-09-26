package scheduler

import (
	"testing"
	"time"

	actormodel "robot/internal/actor"
)

func TestSupervisorRecyclesOfflineAutoActor(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) {
		t.Fatal("assign actor")
	}
	if !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("lease uid")
	}
	result, ok := actor.Enqueue(actormodel.CommandLogout, time.Second)
	if !ok || !result.OK {
		t.Fatalf("logout result = %+v ok=%t", result, ok)
	}
	if snapshot := actor.Snapshot(); snapshot.State != actormodel.StateOffline || snapshot.OnlineDesired {
		t.Fatalf("snapshot after logout = %+v", snapshot)
	}

	supervisor.recycleDesiredOfflineAutoActors()

	if supervisor.ledger.HasUID(uid) {
		t.Fatal("uid lease retained after offline recycle")
	}
	if snapshot := actor.Snapshot(); snapshot.UID != 0 {
		t.Fatalf("actor retained uid after recycle: %+v", snapshot)
	}

	// An actor that still wants to be online must keep its lease.
	if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("reassign actor")
	}
	supervisor.recycleDesiredOfflineAutoActors()
	if !supervisor.ledger.HasUID(uid) {
		t.Fatal("online-desired actor lost its lease")
	}
}
