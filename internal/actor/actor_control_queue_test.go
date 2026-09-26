package actor

import (
	robotcap "robot/internal/capability/robot"
	"testing"
	"time"
)

func TestControlAndWaitWaitsForQueueSpace(t *testing.T) {
	a := NewActor(1, ModeAuto, &partyWaitRuntime{})
	for i := 0; i < cap(a.ctrls); i++ {
		a.ctrls <- control{}
	}
	served := make(chan struct{})
	go func() {
		// Drain the prefilled queue, then serve the pending control like the
		// actor loop would.
		for i := 0; i < cap(a.ctrls); i++ {
			<-a.ctrls
		}
		ctrl := <-a.ctrls
		ctrl.done <- controlResult{uid: 101, ok: true}
		close(served)
	}()

	res := a.controlAndWait(control{kind: controlAssign, uid: 101}, time.Second)
	<-served
	if !res.ok || res.uid != 101 {
		t.Fatalf("control result = %+v, want uid 101 delivered", res)
	}
}

func TestControlAndWaitTimesOutWhenNothingServes(t *testing.T) {
	a := NewActor(1, ModeAuto, &partyWaitRuntime{})
	for i := 0; i < cap(a.ctrls); i++ {
		a.ctrls <- control{}
	}
	start := time.Now()
	res := a.controlAndWait(control{kind: controlRelease}, 50*time.Millisecond)
	if res.ok || res.uid != 0 {
		t.Fatalf("control result = %+v, want timeout", res)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("control wait took %v", elapsed)
	}
}

func TestTickSafelyReleasesLostReleaseRequest(t *testing.T) {
	runtime := &partyWaitRuntime{status: robotcap.RuntimeStatus{UID: 101, StateName: robotcap.RuntimeStateRunning}}
	a := NewActor(1, ModeAuto, runtime)
	a.resetForUID(101)
	a.setReleaseRequested(true)

	a.tickSafely(time.Now())

	if uid := a.uidValue(); uid != 0 {
		t.Fatalf("uid = %d, want the lost release request to be recovered", uid)
	}
	if a.stateValue() == StateReleasing {
		t.Fatal("actor stayed in releasing state after a successful release")
	}
}
