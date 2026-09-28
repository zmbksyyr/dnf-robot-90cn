package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
)

func TestActorStateConstants(t *testing.T) {
	tests := map[State]string{
		StateIdle:      "idle",
		StateOffline:   "attached_offline",
		StateAssigned:  "assigned",
		StateOnline:    "online",
		StateRunning:   "running",
		StateBusy:      "busy",
		StateReleasing: "releasing",
	}
	for got, want := range tests {
		if string(got) != want {
			t.Fatalf("actor state got %q want %q", got, want)
		}
	}
}

func TestEvaluateStatusFailureCount(t *testing.T) {
	now := time.Now()
	dataStatus := EvaluateStatus(Snapshot{
		Mode:           ModeAuto,
		UID:            801,
		Failures:       5,
		FailureClass:   FailureClassData,
		FirstFailureAt: now.Add(-10 * time.Second),
	}, now, StatusConfig{BadFailures: 5}, nil)
	if !dataStatus.RecycleUID || dataStatus.HealthReason != "failure_count" {
		t.Fatalf("data failure status got recycle=%v reason=%q, want failure_count recycle", dataStatus.RecycleUID, dataStatus.HealthReason)
	}

	transportStatus := EvaluateStatus(Snapshot{
		Mode:           ModeAuto,
		UID:            802,
		Failures:       5,
		FailureClass:   FailureClassTransport,
		FirstFailureAt: now.Add(-10 * time.Second),
	}, now, StatusConfig{BadFailures: 5}, nil)
	if transportStatus.RecycleUID {
		t.Fatalf("transport failure must back off instead of recycling: %+v", transportStatus)
	}
	if transportStatus.Health != HealthUnhealthy || transportStatus.HealthReason != "failure_count" {
		t.Fatalf("transport failure status got health=%s reason=%q", transportStatus.Health, transportStatus.HealthReason)
	}
}

func TestEvaluateStatusBusyDoesNotRecycle(t *testing.T) {
	status := EvaluateStatus(Snapshot{
		Mode:     ModeAuto,
		UID:      801,
		Busy:     true,
		BusyKind: "store",
		Failures: 5,
	}, time.Now(), StatusConfig{BadFailures: 5}, nil)
	if status.RecycleUID || status.Health != HealthBusy {
		t.Fatalf("busy status got recycle=%v health=%s, want busy without recycle", status.RecycleUID, status.Health)
	}
}

func TestEvaluateStatusDoesNotRecycleBelowFailureThreshold(t *testing.T) {
	now := time.Now()
	for _, snapshot := range []Snapshot{
		{Mode: ModeAuto, UID: 1001, Failures: 1, FirstFailureAt: now.Add(-61 * time.Second)},
		{Mode: ModeAuto, UID: 1001, Failures: 0, FirstFailureAt: now.Add(-61 * time.Second)},
	} {
		status := EvaluateStatus(snapshot, now, StatusConfig{BadFailures: 3}, nil)
		if status.RecycleUID {
			t.Fatalf("status below failure threshold should not recycle: %+v", status)
		}
	}
}

func TestEvaluateStatusOnlineTimeout(t *testing.T) {
	now := time.Now()
	status := EvaluateStatus(Snapshot{
		Mode:          ModeAuto,
		UID:           1001,
		State:         StateOnline,
		LastOnlineTry: now.Add(-61 * time.Second),
	}, now, StatusConfig{BadFailures: 3, OnlineConfirmTimeoutMS: 60000}, func(uid int) (robotcap.RuntimeStatus, bool) {
		return robotcap.RuntimeStatus{}, false
	})
	if status.RecycleUID || status.HealthReason != "online_confirm_timeout" {
		t.Fatalf("timeout status got recycle=%v reason=%q, want timeout without recycle", status.RecycleUID, status.HealthReason)
	}
}

func TestSnapshotDerivedDisplayState(t *testing.T) {
	snap := Snapshot{State: StateOffline, OnlineDesired: false}
	if got := Operation(snap); got != "offline" {
		t.Fatalf("offline operation got %q", got)
	}
	snap = Snapshot{State: StateBusy, BusyKind: "store", OnlineDesired: true}
	if got := Operation(snap); got != "store" {
		t.Fatalf("busy operation got %q", got)
	}
}

func TestStopPriority(t *testing.T) {
	status := map[int]robotcap.RuntimeStatus{
		1: {UID: 1, StateName: robotcap.RuntimeStateRunning, DisconnectReason: 0},
		2: {UID: 2, StateName: robotcap.RuntimeStateRunning, DisconnectReason: 0, RobotType: 2, StoreDisplayAck: true},
		3: {UID: 3, StateName: robotcap.RuntimeStateLogin, DisconnectReason: 0},
		4: {UID: 4, StateName: robotcap.RuntimeStateRunning, DisconnectReason: 0, PartyActive: true},
	}
	tests := []struct {
		uid  int
		want int
	}{
		{uid: 0, want: 0},
		{uid: 99, want: 1},
		{uid: 3, want: 1},
		{uid: 2, want: 2},
		{uid: 1, want: 3},
		{uid: 4, want: 4},
	}
	for _, tt := range tests {
		if got := StopPriority(tt.uid, status); got != tt.want {
			t.Fatalf("uid %d priority got %d want %d", tt.uid, got, tt.want)
		}
	}
}
