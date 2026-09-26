package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

type storeCleanupCall struct {
	uid    int
	cid    int
	reason string
}

type storeCleanupRuntime struct {
	*partyWaitRuntime
	cleanups []storeCleanupCall
}

type failedReleaseRuntime struct {
	*partyWaitRuntime
}

func (r *failedReleaseRuntime) Logout(uid int) robotcap.ActionResult {
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStatePending}
}

type forceReleaseRuntime struct {
	*failedReleaseRuntime
	forceCalls int
}

type panicCommandRuntime struct {
	*partyWaitRuntime
	panicMove bool
}

func (r *panicCommandRuntime) Move(uid int) robotcap.ActionResult {
	if r.panicMove {
		r.panicMove = false
		panic("move failed unexpectedly")
	}
	return r.partyWaitRuntime.Move(uid)
}

func (r *forceReleaseRuntime) ForceClose(int) bool {
	r.forceCalls++
	return true
}

func (r *storeCleanupRuntime) FinishStoreState(uid, cid int, reason string) {
	r.cleanups = append(r.cleanups, storeCleanupCall{uid: uid, cid: cid, reason: reason})
}

func TestLogoutSkipsStoreCleanupForNormalRobot(t *testing.T) {
	runtime := &storeCleanupRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, CID: 201, RobotType: 0},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	a.logoutCurrentUID()
	if len(runtime.cleanups) != 0 {
		t.Fatalf("normal logout cleanup calls got %d want 0", len(runtime.cleanups))
	}
}

func TestLogoutCleansScheduledStoreState(t *testing.T) {
	runtime := &storeCleanupRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, CID: 201, RobotType: 0},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)
	a.setStoreUntil(time.Now().Add(time.Minute))

	a.logoutCurrentUID()
	if len(runtime.cleanups) != 1 || runtime.cleanups[0] != (storeCleanupCall{uid: 101, cid: 201, reason: "logout"}) {
		t.Fatalf("scheduled store cleanup calls got %+v", runtime.cleanups)
	}
}

func TestReleaseSkipsStoreCleanupForNormalRobot(t *testing.T) {
	runtime := &storeCleanupRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, CID: 201, RobotType: 0},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	a.releaseCurrentUID()
	if len(runtime.cleanups) != 0 {
		t.Fatalf("normal release cleanup calls got %d want 0", len(runtime.cleanups))
	}
}

func TestReleaseCleansRuntimeStoreState(t *testing.T) {
	runtime := &storeCleanupRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, CID: 201, RobotType: 3},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	a.releaseCurrentUID()
	if len(runtime.cleanups) != 1 || runtime.cleanups[0] != (storeCleanupCall{uid: 101, cid: 201, reason: "release"}) {
		t.Fatalf("runtime store cleanup calls got %+v", runtime.cleanups)
	}
}

func TestReleaseRetainsUIDWhenRuntimeCannotClose(t *testing.T) {
	runtime := &failedReleaseRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, StateName: robotcap.RuntimeStateRunning},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	if released := a.releaseCurrentUID(); released != 0 {
		t.Fatalf("released uid = %d, want pending", released)
	}
	if snap := a.Snapshot(); snap.UID != 101 || snap.State != StateReleasing {
		t.Fatalf("pending release snapshot = %+v", snap)
	}
}

func TestReleaseForceClosesBeforeDroppingUID(t *testing.T) {
	runtime := &forceReleaseRuntime{failedReleaseRuntime: &failedReleaseRuntime{
		partyWaitRuntime: &partyWaitRuntime{
			status: robotcap.RuntimeStatus{UID: 101, StateName: robotcap.RuntimeStateRunning},
		},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	if released := a.releaseCurrentUID(); released != 101 {
		t.Fatalf("released uid = %d, want 101", released)
	}
	if runtime.forceCalls != 1 {
		t.Fatalf("force close calls = %d, want 1", runtime.forceCalls)
	}
	if snap := a.Snapshot(); snap.UID != 0 || snap.State != StateIdle {
		t.Fatalf("released snapshot = %+v", snap)
	}
}

func TestAssignDoesNotReplaceUIDUntilPreviousRuntimeCloses(t *testing.T) {
	runtime := &failedReleaseRuntime{partyWaitRuntime: &partyWaitRuntime{
		status: robotcap.RuntimeStatus{UID: 101, StateName: robotcap.RuntimeStateRunning},
	}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)

	res := a.handleControl(control{kind: controlAssign, uid: 202})
	if res.ok || res.uid != 101 {
		t.Fatalf("assign result = %+v, want previous uid retained", res)
	}
	if snap := a.Snapshot(); snap.UID != 101 || snap.State != StateReleasing {
		t.Fatalf("assign after failed release snapshot = %+v", snap)
	}
}

func TestActorRejectsCommandFromPreviousLeaseGeneration(t *testing.T) {
	runtime := &partyWaitRuntime{}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)
	uid, generation := a.leaseIdentity()
	a.resetForUID(202)

	result := a.handleRequestSafely(request{cmd: CommandMove, uid: uid, generation: generation})
	if result.OK || result.UID != 101 || result.State != robotcap.ActionStateFailed {
		t.Fatalf("stale command result = %+v", result)
	}
	if snap := a.Snapshot(); snap.UID != 202 {
		t.Fatalf("new lease changed by stale command: %+v", snap)
	}
}

func TestLedgerKeepsQuarantinedUIDBlocked(t *testing.T) {
	l := NewLedger()
	a := NewActor(1, ModeAuto, &partyWaitRuntime{})
	a.resetForUID(101)
	a.quarantineCurrentUID()
	l.actors[1] = a
	l.uidActors[101] = a
	l.draining[1] = a
	close(a.done)
	l.reapActorLocked(a)

	if !l.IsQuarantinedUID(101) {
		t.Fatal("quarantined UID was not recorded")
	}
	l.UnblockUID(101)
	if _, _, ok := l.ReserveManualActor(101, &partyWaitRuntime{}); ok {
		t.Fatal("quarantined UID became reusable")
	}
}

func TestLedgerClearQuarantineAllowsReuse(t *testing.T) {
	l := NewLedger()
	a := NewActor(1, ModeAuto, &partyWaitRuntime{})
	a.resetForUID(101)
	a.quarantineCurrentUID()
	l.actors[1] = a
	l.uidActors[101] = a
	l.draining[1] = a
	close(a.done)
	l.reapActorLocked(a)

	if !l.ClearQuarantine(101) {
		t.Fatal("ClearQuarantine reported nothing to clear")
	}
	if l.IsQuarantinedUID(101) {
		t.Fatal("quarantine entry survived ClearQuarantine")
	}
	if l.BlockedCount() != 0 {
		t.Fatalf("blocked count = %d after clearing", l.BlockedCount())
	}
	if l.ClearQuarantine(101) {
		t.Fatal("ClearQuarantine on an unquarantined UID reported success")
	}
}

func TestActorCommandPanicReleasesUIDAndKeepsLoopUsable(t *testing.T) {
	runtime := &panicCommandRuntime{
		partyWaitRuntime: &partyWaitRuntime{
			config: robotconfig.RuntimeConfig{SystemActorPollMS: 100},
			status: robotcap.RuntimeStatus{UID: 101, StateName: robotcap.RuntimeStateRunning},
		},
		panicMove: true,
	}
	a := NewActor(1, ModeAuto, runtime)
	a.Start()
	defer func() {
		a.RequestStop()
		select {
		case <-a.Done():
		case <-time.After(2 * time.Second):
			t.Fatal("actor did not stop")
		}
	}()

	if !a.AssignAndWait(101, time.Second) {
		t.Fatal("initial uid assignment failed")
	}
	result, accepted := a.Enqueue(CommandMove, time.Second)
	if !accepted || result.OK || result.State != robotcap.ActionStateFailed {
		t.Fatalf("panic command result = %+v accepted=%t", result, accepted)
	}

	deadline := time.Now().Add(2 * time.Second)
	for a.UIDValue() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if snap := a.Snapshot(); snap.UID != 0 || snap.State != StateIdle {
		t.Fatalf("panic cleanup snapshot = %+v", snap)
	}
	if !a.AssignAndWait(202, time.Second) {
		t.Fatal("actor loop was not usable after panic cleanup")
	}
}
