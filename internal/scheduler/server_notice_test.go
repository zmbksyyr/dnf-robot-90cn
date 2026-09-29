package scheduler

import (
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func forceServerNoticeSlotDue(m *RobotManager) {
	m.serverNoticeMu.Lock()
	for _, window := range m.serverNoticeAreas {
		window.nextAt = time.Now().Add(-time.Second)
	}
	m.serverNoticeMu.Unlock()
}

func TestServerNoticeClaimPacesEachAreaSeparately(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	rc := robotconfig.RuntimeConfig{ServerNoticeMinGapSec: 60, ServerNoticeMaxGapSec: 60, ServerNoticeMaxPerHour: 0}
	if !m.claimServerNoticeSlot(rc, 1, 1) {
		t.Fatal("first claim in 1/1 rejected")
	}
	if m.claimServerNoticeSlot(rc, 1, 1) {
		t.Fatal("second immediate claim in 1/1 accepted inside the gap")
	}
	// A different area keeps its own stream.
	if !m.claimServerNoticeSlot(rc, 2, 3) {
		t.Fatal("first claim in 2/3 rejected while 1/1 was cooling down")
	}
	if m.claimServerNoticeSlot(rc, 2, 3) {
		t.Fatal("second immediate claim in 2/3 accepted inside the gap")
	}
	forceServerNoticeSlotDue(m)
	if !m.claimServerNoticeSlot(rc, 1, 1) {
		t.Fatal("claim after the per-area gap rejected")
	}
}

func TestServerNoticeClaimHonorsPerAreaHourlyCap(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	rc := robotconfig.RuntimeConfig{ServerNoticeMinGapSec: 30, ServerNoticeMaxGapSec: 30, ServerNoticeMaxPerHour: 2}
	for index := 0; index < 2; index++ {
		if !m.claimServerNoticeSlot(rc, 7, 1) {
			t.Fatalf("claim %d in 7/1 rejected below the hourly cap", index)
		}
		forceServerNoticeSlotDue(m)
	}
	if m.claimServerNoticeSlot(rc, 7, 1) {
		t.Fatal("claim above the 7/1 hourly cap accepted")
	}
	// The cap is per area: another area still accepts.
	if !m.claimServerNoticeSlot(rc, 7, 2) {
		t.Fatal("claim in 7/2 rejected by the 7/1 hourly cap")
	}
}

func TestServerNoticeKindSplitHonorsPercent(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	if kind := m.serverNoticeKind(robotconfig.RuntimeConfig{ServerNoticeLotteryPercent: 100}); kind != shared.ServerNoticeLottery {
		t.Fatalf("kind = %s, want lottery", kind.Name())
	}
	if kind := m.serverNoticeKind(robotconfig.RuntimeConfig{ServerNoticeLotteryPercent: 0}); kind != shared.ServerNoticeUpgrade {
		t.Fatalf("kind = %s, want upgrade", kind.Name())
	}
	lottery, upgrade := 0, 0
	for index := 0; index < 400; index++ {
		switch m.serverNoticeKind(robotconfig.RuntimeConfig{ServerNoticeLotteryPercent: 70}) {
		case shared.ServerNoticeLottery:
			lottery++
		case shared.ServerNoticeUpgrade:
			upgrade++
		}
	}
	if lottery == 0 || upgrade == 0 {
		t.Fatalf("70%% split produced lottery=%d upgrade=%d", lottery, upgrade)
	}
	if lottery < 200 || lottery > 360 {
		t.Fatalf("70%% split lottery=%d outside the expected band", lottery)
	}
}

func TestSimulatorAutoServerNoticeIsSkippedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	result := NewRobotRuntime(m).AutoServerNotice(7, nil)
	if result.State != robotcap.ActionStateCancelled || !strings.Contains(result.Message, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("result = %+v, want skipped unsupported server notice", result)
	}
}

type fakeServerNoticer struct {
	events   []shared.ServerNoticeEvent
	requests []shared.ServerNoticeTriggerRequest
}

func (f *fakeServerNoticer) TriggerServerNotice(request shared.ServerNoticeTriggerRequest) (shared.ServerNoticeTriggerResult, error) {
	f.requests = append(f.requests, request)
	return shared.ServerNoticeTriggerResult{Kind: request.Kind, UID: request.UID, CID: request.CID, Sent: true, Accepted: true}, nil
}

func (f *fakeServerNoticer) RecentServerNotices() []shared.ServerNoticeEvent {
	return f.events
}

func TestRecentServerNoticesPassThroughAdapterHistory(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	noticer := &fakeServerNoticer{events: []shared.ServerNoticeEvent{{Kind: "upgrade", UID: 7, ItemID: 1001, Level: 13}}}
	m.SetBackendServerNoticeRuntime(noticer)
	events := m.RecentServerNotices()
	if len(events) != 1 || events[0].ItemID != 1001 || events[0].Level != 13 {
		t.Fatalf("events = %+v", events)
	}
	if m.RecentServerNotices() == nil {
		t.Fatal("history became nil")
	}
}
