package scheduler

import (
	"errors"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

type failingActionRuntime struct {
	noopRuntime
	moveErr  error
	shoutErr error
}

func (r failingActionRuntime) Move(shared.RuntimeMoveCommand) error   { return r.moveErr }
func (r failingActionRuntime) Shout(shared.RuntimeShoutCommand) error { return r.shoutErr }

func TestRobotRuntimeCountsAutomaticTransportFailures(t *testing.T) {
	wantMove := errors.New("move queue full")
	wantShout := errors.New("shout send failed")
	manager := testRobotManagerWithConfig(t, "[move]\nmove_steps = 2\n[shout]\nshout_send_enabled = true\n")
	manager.doll = failingActionRuntime{moveErr: wantMove, shoutErr: wantShout}
	manager.runtimeState.snapshot = map[int]robotcap.RuntimeStatus{
		17000001: {UID: 17000001, CID: 2001, StateName: robotcap.RuntimeStateRunning},
	}
	manager.runtimeState.updatedAt = time.Now()
	runtime := NewRobotRuntime(manager)

	move := runtime.AutoMove(17000001)
	if move.OK || move.Message != wantMove.Error() || move.State != robotcap.ActionStateFailed {
		t.Fatalf("move result = %+v", move)
	}
	shout := runtime.AutoShout(17000001, false, "hello")
	if shout.OK || shout.Message != wantShout.Error() || shout.State != robotcap.ActionStateFailed {
		t.Fatalf("shout result = %+v", shout)
	}

	manager.autoMu.Lock()
	stats := manager.autoStats
	manager.autoMu.Unlock()
	if stats.MoveSuccess != 0 || stats.MoveFailed != 1 {
		t.Fatalf("move stats = %d/%d", stats.MoveSuccess, stats.MoveFailed)
	}
	if stats.ShoutLocalSuccess != 0 || stats.ShoutLocalFailed != 1 {
		t.Fatalf("local shout stats = %d/%d", stats.ShoutLocalSuccess, stats.ShoutLocalFailed)
	}
}

func TestRobotRuntimeReturnsFailureWhenActionPanics(t *testing.T) {
	runtime := NewRobotRuntime(nil)
	result := runtime.run(17000001, func() robotcap.ActionResult {
		panic("boom")
	})
	if result.UID != 17000001 || result.OK || result.State != robotcap.ActionStateFailed || result.Message != "runtime panic" {
		t.Fatalf("panic result = %+v", result)
	}
}
