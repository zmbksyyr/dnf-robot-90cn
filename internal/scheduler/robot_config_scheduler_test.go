package scheduler

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/layout"
)

func TestSchedulerPolicyReasonConstants(t *testing.T) {
	tests := []struct {
		got  string
		want string
	}{
		{schedulerReasonAutoDisabled, "auto_disabled"},
		{schedulerReasonGamePortUnstable, "game_port_unstable"},
		{schedulerReasonBreakerActive, "breaker_active"},
		{schedulerReasonTargetZero, "target_zero"},
		{schedulerReasonNoLiveSnapshot, "no_live_snapshot"},
		{schedulerReasonKeyInvalid, "key_invalid"},
		{schedulerReasonKeyInvalidPrefix, "key_invalid="},
		{schedulerReasonStructuralPrefix, "structural_op="},
		{schedulerReasonActorPrefix, "actor_container="},
		{schedulerReasonPendingBacklog, "pending_backlog"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Fatalf("scheduler policy reason constant got %q want %q", tt.got, tt.want)
		}
	}
}

func TestSetAutoEnabledReportsConfigPersistenceFailure(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	if _, err := m.SetAutoEnabled(false); err == nil {
		t.Fatal("missing robot config did not report persistence failure")
	}
}

func TestRetryDisjointInCurrentSessionOnlyForTransientPositionState(t *testing.T) {
	for _, reason := range []string{"set_area_failed", "ack_timeout", "disjoint_err_0x14", "disjoint_err_0x3e", "disjoint_err_0x52", "disjoint_err_0xbe"} {
		if !retryDisjointInCurrentSession(reason) {
			t.Fatalf("reason %q should retry in current session", reason)
		}
	}
	for _, reason := range []string{"disjoint_err_0x0a", "disjoint_err_0x13", "disjoint_err_0x15", "disjoint_err_0x16", "runtime_stopped", "online_failed", "profession_failed"} {
		if retryDisjointInCurrentSession(reason) {
			t.Fatalf("reason %q should stop current uid", reason)
		}
	}
}

func TestBeginAdaptiveStoreTypeBalancesPlannedStores(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.runtimeState.snapshot = map[int]robotcap.RuntimeStatus{
		1: {UID: 1, StateName: robotcap.RuntimeStateRunning, RobotType: 2, StoreDisplayAck: true},
		2: {UID: 2, StateName: robotcap.RuntimeStateRunning, RobotType: 2, StoreDisplayAck: true},
		3: {UID: 3, StateName: robotcap.RuntimeStateRunning, RobotType: 3, DisjointActive: true},
	}
	m.runtimeState.updatedAt = time.Now()

	disjoint, done := m.beginAdaptiveStoreType()
	if !disjoint {
		t.Fatal("expected disjoint store when item stores are ahead")
	}
	done()

	m.runtimeState.snapshot = map[int]robotcap.RuntimeStatus{
		1: {UID: 1, StateName: robotcap.RuntimeStateRunning, RobotType: 2, StoreDisplayAck: true},
		2: {UID: 2, StateName: robotcap.RuntimeStateRunning, RobotType: 3, DisjointActive: true},
	}
	firstDisjoint, firstDone := m.beginAdaptiveStoreType()
	secondDisjoint, secondDone := m.beginAdaptiveStoreType()
	defer firstDone()
	defer secondDone()
	if firstDisjoint || !secondDisjoint {
		t.Fatalf("balanced stores should plan item then disjoint, got first=%v second=%v", firstDisjoint, secondDisjoint)
	}
}

func TestLoadRobotConfigSchedulerOnlineDefaults(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	rc := m.loadRobotConfig()
	if rc.SchedulerOnlineBatchSize != 10 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want 10", rc.SchedulerOnlineBatchSize)
	}
	if rc.SchedulerOnlineStartRate != 4 {
		t.Fatalf("SchedulerOnlineStartRate got %d want 4", rc.SchedulerOnlineStartRate)
	}
	if rc.SchedulerOnlineFillTimeout != 45 {
		t.Fatalf("SchedulerOnlineFillTimeout got %d want 45", rc.SchedulerOnlineFillTimeout)
	}
	if rc.SchedulerBreakerAbnormalPct != 30 {
		t.Fatalf("SchedulerBreakerAbnormalPct got %d want 30", rc.SchedulerBreakerAbnormalPct)
	}
	if rc.SchedulerBreakerPauseSec != 180 {
		t.Fatalf("SchedulerBreakerPauseSec got %d want 180", rc.SchedulerBreakerPauseSec)
	}
	if rc.SchedulerBreakerReleaseBatch != 5 {
		t.Fatalf("SchedulerBreakerReleaseBatch got %d want 5", rc.SchedulerBreakerReleaseBatch)
	}
	if rc.SchedulerBreakerFloorPct != 70 {
		t.Fatalf("SchedulerBreakerFloorPct got %d want 70", rc.SchedulerBreakerFloorPct)
	}
	if rc.SchedulerPortDownReleaseBatch != 5 {
		t.Fatalf("SchedulerPortDownReleaseBatch got %d want 5", rc.SchedulerPortDownReleaseBatch)
	}
	assertIntSlice(t, rc.EquipSlots, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	if rc.EquipIntensifyMin != 7 || rc.EquipIntensifyMax != 10 {
		t.Fatalf("EquipIntensify got %d..%d want 7..10", rc.EquipIntensifyMin, rc.EquipIntensifyMax)
	}
	if !rc.AutoActions || !rc.AutoMailNotify || rc.AutoTargetOnlineCount != 20 || rc.AutoStoreProbabilityPercent != 35 {
		t.Fatalf("auto defaults got enabled=%v mail=%v target=%d store_probability=%d, want true/true/20/35", rc.AutoActions, rc.AutoMailNotify, rc.AutoTargetOnlineCount, rc.AutoStoreProbabilityPercent)
	}
	if rc.AutoMoveIntervalMinSec != 6 || rc.AutoMoveIntervalMaxSec != 18 {
		t.Fatalf("AutoMoveInterval got %d..%d want 6..18", rc.AutoMoveIntervalMinSec, rc.AutoMoveIntervalMaxSec)
	}
	if rc.AutoShoutIntervalMinSec != 45 || rc.AutoShoutIntervalMaxSec != 120 {
		t.Fatalf("AutoShoutInterval got %d..%d want 45..120", rc.AutoShoutIntervalMinSec, rc.AutoShoutIntervalMaxSec)
	}
}

func TestLoadRobotConfigSchedulerValuesAreAdaptive(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 600\nauto_shout_interval_min_sec = 300\nauto_shout_interval_max_sec = 600\nauto_store_probability_percent = 99\nauto_store_interval_min_sec = 1\nauto_store_interval_max_sec = 2\n[scheduler]\nstore_concurrent = 1\nonline_batch_size = 30\nonline_start_rate = 8\nonline_fill_timeout_sec = 90\nbreaker_abnormal_percent = 25\nbreaker_pause_sec = 120\nbreaker_release_batch = 10\nbreaker_floor_percent = 80\nport_down_release_batch = 15\n")
	rc := m.loadRobotConfig()
	if rc.SchedulerOnlineBatchSize != 60 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want 60", rc.SchedulerOnlineBatchSize)
	}
	if rc.SchedulerOnlineStartRate != 15 {
		t.Fatalf("SchedulerOnlineStartRate got %d want 15", rc.SchedulerOnlineStartRate)
	}
	if rc.SchedulerOnlineFillTimeout != 60 {
		t.Fatalf("SchedulerOnlineFillTimeout got %d want 60", rc.SchedulerOnlineFillTimeout)
	}
	if rc.SchedulerStoreConcurrent != 30 {
		t.Fatalf("SchedulerStoreConcurrent got %d want 30", rc.SchedulerStoreConcurrent)
	}
	if rc.AutoStoreProbabilityPercent != 20 {
		t.Fatalf("AutoStoreProbabilityPercent got %d want 20", rc.AutoStoreProbabilityPercent)
	}
	if rc.AutoMoveIntervalMinSec != 11 || rc.AutoMoveIntervalMaxSec != 33 {
		t.Fatalf("AutoMoveInterval got %d..%d want 11..33", rc.AutoMoveIntervalMinSec, rc.AutoMoveIntervalMaxSec)
	}
	if rc.AutoShoutIntervalMinSec != 300 || rc.AutoShoutIntervalMaxSec != 600 {
		t.Fatalf("AutoShoutInterval got %d..%d want configured 300..600", rc.AutoShoutIntervalMinSec, rc.AutoShoutIntervalMaxSec)
	}
	if rc.AutoStoreIntervalMinSec != 75 || rc.AutoStoreIntervalMaxSec != 195 {
		t.Fatalf("AutoStoreInterval got %d..%d want 75..195", rc.AutoStoreIntervalMinSec, rc.AutoStoreIntervalMaxSec)
	}
	if rc.SchedulerBreakerAbnormalPct != 30 {
		t.Fatalf("SchedulerBreakerAbnormalPct got %d want 30", rc.SchedulerBreakerAbnormalPct)
	}
	if rc.SchedulerBreakerPauseSec != 300 {
		t.Fatalf("SchedulerBreakerPauseSec got %d want 300", rc.SchedulerBreakerPauseSec)
	}
	if rc.SchedulerBreakerReleaseBatch != 20 {
		t.Fatalf("SchedulerBreakerReleaseBatch got %d want 20", rc.SchedulerBreakerReleaseBatch)
	}
	if rc.SchedulerBreakerFloorPct != 70 {
		t.Fatalf("SchedulerBreakerFloorPct got %d want 70", rc.SchedulerBreakerFloorPct)
	}
	if rc.SchedulerPortDownReleaseBatch != 24 {
		t.Fatalf("SchedulerPortDownReleaseBatch got %d want 24", rc.SchedulerPortDownReleaseBatch)
	}
}

func TestLoadRobotConfigDoesNotAutoPatchEquipSlots(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[equipment]\nequip_slots = 3,4,5\n")
	rc := m.loadRobotConfig()
	assertIntSlice(t, rc.EquipSlots, []int{3, 4, 5})
}

func TestLoadRobotConfigRejectsOutOfRangeSchedulerEditAndKeepsSnapshot(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 600\n")
	before := m.loadRobotConfig()
	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	invalid := "[auto]\nauto_target_online_count = 600\n[scheduler]\nonline_batch_size = 999\nonline_start_rate = 999\nonline_fill_timeout_sec = -1\nbreaker_abnormal_percent = 999\nbreaker_pause_sec = 9999\nbreaker_release_batch = 999\nbreaker_floor_percent = 999\nport_down_release_batch = 999\n"
	if err := os.WriteFile(path, []byte(invalid), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadRobotConfigFile(path); err == nil {
		t.Fatal("out-of-range scheduler edit unexpectedly loaded")
	}
	after := m.loadRobotConfig()
	if after.AutoTargetOnlineCount != before.AutoTargetOnlineCount ||
		after.SchedulerOnlineBatchSize != before.SchedulerOnlineBatchSize ||
		after.SchedulerOnlineStartRate != before.SchedulerOnlineStartRate ||
		after.SchedulerBreakerPauseSec != before.SchedulerBreakerPauseSec {
		t.Fatalf("invalid scheduler edit replaced snapshot: before=%+v after=%+v", before, after)
	}
}

func TestReloadRobotConfigRejectsOpenFileRequirementOverflow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[online]\nmax_online_robots = 1000\nmax_online_per_command = 1000\n")
	before := m.loadRobotConfig()
	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	text := fmt.Sprintf("[online]\nmax_online_robots = %d\nmax_online_per_command = 1000\n", int(^uint(0)>>1))
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.reloadRobotConfigFile(path); err == nil {
		t.Fatal("overflowing open-file requirement unexpectedly loaded")
	}
	after := m.loadRobotConfig()
	if after.MaxOnlineRobots != before.MaxOnlineRobots {
		t.Fatalf("rejected max_online_robots replaced snapshot: before=%d after=%d", before.MaxOnlineRobots, after.MaxOnlineRobots)
	}
}

func TestUpdateRobotConfigPreflightFailureKeepsLastValidFile(t *testing.T) {
	requests := map[string]robotcap.ConfigUpdateRequest{
		"text": {
			Text: fmt.Sprintf("[online]\nmax_online_robots = %d\nmax_online_per_command = 1000\n", int(^uint(0)>>1)),
		},
		"updates": {
			Updates: map[string]interface{}{
				"online.max_online_robots":      int(^uint(0) >> 1),
				"online.max_online_per_command": 1000,
			},
		},
	}
	for name, req := range requests {
		t.Run(name, func(t *testing.T) {
			m := testRobotManagerWithConfig(t, "[online]\nmax_online_robots = 1000\nmax_online_per_command = 1000\n")
			beforeConfig := m.loadRobotConfig()
			path := layout.New(m.cfg.ConfigDir).RobotConfig()
			beforeFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := m.UpdateRobotConfig(req); err == nil {
				t.Fatal("overflowing open-file requirement unexpectedly saved")
			}
			afterFile, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(afterFile) != string(beforeFile) {
				t.Fatalf("rejected update changed config file:\nbefore=%q\nafter=%q", beforeFile, afterFile)
			}
			if got := m.loadRobotConfig().MaxOnlineRobots; got != beforeConfig.MaxOnlineRobots {
				t.Fatalf("rejected update changed snapshot: got=%d want=%d", got, beforeConfig.MaxOnlineRobots)
			}
		})
	}
}

func TestAdaptiveSchedulerLiveFeedbackIncreasesStoreWhenHealthy(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000, AutoShoutIntervalMinSec: 300, AutoShoutIntervalMaxSec: 600}
	applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       590,
		Connecting:    3,
		StoreRunning:  5,
		Actors:        600,
		Idle:          20,
		GamePortReady: true,
	})
	if rc.SchedulerStoreConcurrent != 55 {
		t.Fatalf("SchedulerStoreConcurrent got %d want 55", rc.SchedulerStoreConcurrent)
	}
	if rc.AutoStoreProbabilityPercent != 30 {
		t.Fatalf("AutoStoreProbabilityPercent got %d want 30", rc.AutoStoreProbabilityPercent)
	}
	if rc.AutoMoveIntervalMinSec != 9 || rc.AutoMoveIntervalMaxSec != 29 {
		t.Fatalf("AutoMoveInterval got %d..%d want 9..29", rc.AutoMoveIntervalMinSec, rc.AutoMoveIntervalMaxSec)
	}
	if rc.AutoShoutIntervalMinSec != 300 || rc.AutoShoutIntervalMaxSec != 600 {
		t.Fatalf("AutoShoutInterval got %d..%d want configured 300..600", rc.AutoShoutIntervalMinSec, rc.AutoShoutIntervalMaxSec)
	}
	if rc.AutoStoreIntervalMinSec != 60 || rc.AutoStoreIntervalMaxSec != 156 {
		t.Fatalf("AutoStoreInterval got %d..%d want 60..156", rc.AutoStoreIntervalMinSec, rc.AutoStoreIntervalMaxSec)
	}
}

func TestAdaptiveSchedulerLiveFeedbackUsesActiveStoreTarget(t *testing.T) {
	if got := adaptiveActiveStoreTarget(600); got != adaptiveActiveStoreCapacity {
		t.Fatalf("adaptiveActiveStoreTarget(600) got %d want %d", got, adaptiveActiveStoreCapacity)
	}

	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000}
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       600,
		StoreRunning:  84,
		Actors:        600,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyStore {
		t.Fatalf("84 active stores mode got %s want %s reason=%s", decision.Mode, schedulerPolicyStore, decision.Reason)
	}
	if rc.SchedulerStoreConcurrent != 45 {
		t.Fatalf("84 active stores concurrency got %d want 45", rc.SchedulerStoreConcurrent)
	}

	rc = robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000}
	decision = applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       600,
		StoreRunning:  adaptiveActiveStoreCapacity - 1,
		Actors:        600,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyStore || rc.SchedulerStoreConcurrent != 5 || rc.AutoStoreMaxPositionTries != 11 {
		t.Fatalf("near-target store expansion decision=%+v config=%+v", decision, rc)
	}

	rc = robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000}
	decision = applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       600,
		StoreRunning:  adaptiveActiveStoreCapacity,
		Actors:        600,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyStable {
		t.Fatalf("capacity active stores mode got %s want %s reason=%s", decision.Mode, schedulerPolicyStable, decision.Reason)
	}
	if rc.AutoStoreProbabilityPercent != 0 {
		t.Fatalf("capacity active stores probability got %d want 0", rc.AutoStoreProbabilityPercent)
	}
}

func TestAdaptiveSchedulerDoesNotExpandStoresWhileScalingDown(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 80, MaxOnlineRobots: 1000}
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       400,
		StoreRunning:  0,
		Actors:        400,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyStable {
		t.Fatalf("oversupplied mode got %s want %s reason=%s", decision.Mode, schedulerPolicyStable, decision.Reason)
	}
	if rc.AutoStoreProbabilityPercent != 0 {
		t.Fatalf("oversupplied store probability got %d want 0", rc.AutoStoreProbabilityPercent)
	}
}

func TestAdaptiveSchedulerLiveFeedbackReducesPressure(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000, AutoShoutIntervalMinSec: 300, AutoShoutIntervalMaxSec: 600}
	applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       420,
		Connecting:    120,
		StoreRunning:  28,
		Actors:        600,
		Idle:          0,
		GamePortReady: true,
	})
	if rc.SchedulerOnlineBatchSize != 30 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want 30", rc.SchedulerOnlineBatchSize)
	}
	if rc.SchedulerOnlineStartRate != 7 {
		t.Fatalf("SchedulerOnlineStartRate got %d want 7", rc.SchedulerOnlineStartRate)
	}
	if rc.SchedulerStoreConcurrent != 15 {
		t.Fatalf("SchedulerStoreConcurrent got %d want 15", rc.SchedulerStoreConcurrent)
	}
	if rc.AutoStoreProbabilityPercent != 6 {
		t.Fatalf("AutoStoreProbabilityPercent got %d want 6", rc.AutoStoreProbabilityPercent)
	}
	if rc.AutoMoveIntervalMinSec != 16 || rc.AutoMoveIntervalMaxSec != 49 {
		t.Fatalf("AutoMoveInterval got %d..%d want 16..49", rc.AutoMoveIntervalMinSec, rc.AutoMoveIntervalMaxSec)
	}
	if rc.AutoShoutIntervalMinSec != 300 || rc.AutoShoutIntervalMaxSec != 600 {
		t.Fatalf("AutoShoutInterval got %d..%d want configured 300..600", rc.AutoShoutIntervalMinSec, rc.AutoShoutIntervalMaxSec)
	}
	if rc.AutoStoreIntervalMinSec != 112 || rc.AutoStoreIntervalMaxSec != 292 {
		t.Fatalf("AutoStoreInterval got %d..%d want 112..292", rc.AutoStoreIntervalMinSec, rc.AutoStoreIntervalMaxSec)
	}
}

func TestAdaptiveSchedulerThrottlesRecentOnlineFailures(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 1500, MaxOnlineRobots: 2000}
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live: true, Running: 900, Actors: 1100, GamePortReady: true,
		OnlineSuccess: 20, OnlineFailed: 80,
	})
	if decision.Mode != schedulerPolicyPressure || !strings.Contains(decision.Reason, "online_failure_pressure") {
		t.Fatalf("decision = %+v", decision)
	}
	if rc.SchedulerOnlineStartRate != 7 || rc.SchedulerOnlineBatchSize != 15 {
		t.Fatalf("online throttle rate=%d batch=%d, want 7/15", rc.SchedulerOnlineStartRate, rc.SchedulerOnlineBatchSize)
	}
}

func TestAdaptiveSchedulerLiveFeedbackSpeedsFillWhenConnectionRoom(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000}
	applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       550,
		Connecting:    5,
		StoreRunning:  25,
		Actors:        570,
		Idle:          0,
		GamePortReady: true,
	})
	if rc.SchedulerOnlineStartRate != 21 {
		t.Fatalf("SchedulerOnlineStartRate got %d want 21", rc.SchedulerOnlineStartRate)
	}
}

func TestAdaptiveSchedulerMissingTargetFillsInsteadOfPressure(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 20, MaxOnlineRobots: 1000}
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       14,
		Connecting:    0,
		Actors:        14,
		Idle:          0,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyFill {
		t.Fatalf("mode got %s want %s reason=%s", decision.Mode, schedulerPolicyFill, decision.Reason)
	}
	if rc.SchedulerOnlineBatchSize <= 0 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want positive fill batch", rc.SchedulerOnlineBatchSize)
	}
}

func TestAdaptiveSchedulerFreezesScaleUpOnPendingBacklog(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 1000}
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live:          true,
		Running:       130,
		Connecting:    0,
		Actors:        420,
		Idle:          290,
		GamePortReady: true,
	})
	if decision.Mode != schedulerPolicyPressure {
		t.Fatalf("mode got %s want %s reason=%s", decision.Mode, schedulerPolicyPressure, decision.Reason)
	}
	if rc.SchedulerOnlineBatchSize != -1 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want frozen -1", rc.SchedulerOnlineBatchSize)
	}
	if got := robotconfig.ScaleUpBatch(rc); got != 0 {
		t.Fatalf("schedulerScaleUpBatch got %d want 0", got)
	}
}

func TestLoadRobotConfigPreservesAdaptiveScaleFreeze(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 600\n")
	m.autoMu.Lock()
	m.autoStats.UpdatedAt = time.Now()
	m.autoStats.Running = 130
	m.autoStats.Idle = 150
	m.autoStats.Actors = 700
	m.autoStats.GamePortReady = true
	m.autoMu.Unlock()
	rc := m.loadRobotConfig()
	if rc.SchedulerOnlineBatchSize != -1 {
		t.Fatalf("SchedulerOnlineBatchSize got %d want adaptive freeze -1", rc.SchedulerOnlineBatchSize)
	}
	if got := robotconfig.ScaleUpBatch(rc); got != 0 {
		t.Fatalf("schedulerScaleUpBatch got %d want 0", got)
	}
}
