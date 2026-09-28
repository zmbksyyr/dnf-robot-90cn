package actor

import (
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

func TestNextServerNoticeDueHonorsEnableAndInterval(t *testing.T) {
	runtime := &partyWaitRuntime{}
	a := NewActor(1, ModeAuto, runtime)

	disabled := robotconfig.RuntimeConfig{AutoServerNotice: false, ServerNoticeRobotIntervalMinSec: 60, ServerNoticeRobotIntervalMaxSec: 60}
	if a.nextServerNoticeDue(time.Now(), disabled) {
		t.Fatal("disabled server notice became due")
	}

	enabled := robotconfig.RuntimeConfig{AutoServerNotice: true, ServerNoticeRobotIntervalMinSec: 1, ServerNoticeRobotIntervalMaxSec: 1}
	now := time.Now()
	if a.nextServerNoticeDue(now, enabled) {
		t.Fatal("first call must only arm the per-robot timer")
	}
	if !a.nextServerNoticeDue(now.Add(2*time.Second), enabled) {
		t.Fatal("per-robot timer did not fire after its interval")
	}
	if a.nextServerNoticeDue(now.Add(2*time.Second), enabled) {
		t.Fatal("per-robot timer fired twice for one interval")
	}
}
