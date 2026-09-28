package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

// gatedRuntime wraps the reconnect runtime and controls the online attempt
// gate, so the actor-level pacing contract is covered end to end.
type gatedRuntime struct {
	reconnectRuntime
	allow bool
}

func (r *gatedRuntime) TryAcquireOnlineAttempt() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.allow
}

func (r *gatedRuntime) ReleaseOnlineAttempt() {}

func TestActorOnlineAttemptsRespectSchedulerGate(t *testing.T) {
	runtime := &gatedRuntime{
		reconnectRuntime: reconnectRuntime{config: robotconfig.RuntimeConfig{
			SystemActorPollMS:             50,
			OnlineConfirmTimeoutMS:        5000,
			SchedulerOnlineRetryBaseMS:    1000,
			SchedulerOnlineRetryMaxMS:     1000,
			SchedulerOnlineRetryJitterPct: 0,
		}},
	}
	actor := NewActor(1, ModeAuto, runtime)
	actor.Start()
	defer actor.StopAndWait(time.Second)
	if !actor.AssignAndWait(17000001, time.Second) {
		t.Fatal("failed to assign actor")
	}
	time.Sleep(400 * time.Millisecond)
	if got := runtime.onlineCount(); got != 0 {
		t.Fatalf("closed gate allowed %d online attempts, want 0", got)
	}
	runtime.mu.Lock()
	runtime.allow = true
	runtime.mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && runtime.onlineCount() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runtime.onlineCount(); got == 0 {
		t.Fatal("open gate did not admit an online attempt")
	}
}

// initStatusRuntime keeps the session in the login state so the actor waits for
// confirmation until the confirm timeout fires.
type initStatusRuntime struct {
	reconnectRuntime
}

func (r *initStatusRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	return robotcap.RuntimeStatus{UID: uid, StateName: robotcap.RuntimeStateInit}, true
}

func (r *initStatusRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	r.mu.Lock()
	r.online++
	r.mu.Unlock()
	return robotcap.ActionResult{UID: uid, State: robotcap.ActionStateAccepted}
}

func TestActorConfirmTimeoutCountsAttemptFailure(t *testing.T) {
	runtime := &initStatusRuntime{reconnectRuntime: reconnectRuntime{config: robotconfig.RuntimeConfig{
		SystemActorPollMS:             50,
		OnlineConfirmTimeoutMS:        50,
		SchedulerOnlineRetryBaseMS:    1000,
		SchedulerOnlineRetryMaxMS:     1000,
		SchedulerOnlineRetryJitterPct: 0,
	}}}
	actor := NewActor(1, ModeAuto, runtime)
	actor.Start()
	defer actor.StopAndWait(time.Second)
	if !actor.AssignAndWait(17000001, time.Second) {
		t.Fatal("failed to assign actor")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && actor.Snapshot().Failures == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := actor.Snapshot()
	if snapshot.Failures == 0 {
		t.Fatal("confirm timeout did not record a failure")
	}
	if snapshot.FailureClass != FailureClassTransport {
		t.Fatalf("failure class got %q want %q", snapshot.FailureClass, FailureClassTransport)
	}
}
