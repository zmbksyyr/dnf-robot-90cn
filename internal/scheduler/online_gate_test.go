package scheduler

import (
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotconfig "robot/internal/capability/robotconfig"
)

func TestOnlineGateRespectsAdaptiveRateAndInFlight(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 1000\n")
	rc := m.loadRobotConfig()
	rate := robotconfig.OnlineStartRate(rc)
	inFlightCap := rc.SchedulerOnlineInFlight
	if rate <= 0 || inFlightCap <= 0 {
		t.Fatalf("adaptive pacing missing rate=%d in_flight=%d", rate, inFlightCap)
	}

	// The lazy bucket starts with one token and refills at the adaptive rate.
	if !m.TryAcquireOnlineAttempt() {
		t.Fatal("first attempt should be admitted")
	}
	if m.OnlineAttemptInFlight() != 1 {
		t.Fatalf("in flight got %d want 1", m.OnlineAttemptInFlight())
	}
	// No refill window elapsed and one slot is already in flight; the gate must
	// not hand out another attempt immediately.
	if m.TryAcquireOnlineAttempt() {
		t.Fatal("attempt admitted without refill tokens")
	}

	deadline := time.Now().Add(2 * time.Second)
	for m.OnlineAttemptInFlight() < inFlightCap && time.Now().Before(deadline) {
		if m.TryAcquireOnlineAttempt() {
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := m.OnlineAttemptInFlight(); got > inFlightCap {
		t.Fatalf("in flight %d above cap %d", got, inFlightCap)
	}
	if got := m.OnlineAttemptInFlight(); got < 2 {
		t.Fatalf("gate did not refill attempts: in flight %d", got)
	}
	for i := 0; i < inFlightCap+1; i++ {
		m.ReleaseOnlineAttempt()
	}
	if m.OnlineAttemptInFlight() != 0 {
		t.Fatalf("in flight %d after release", m.OnlineAttemptInFlight())
	}
}

func TestOnlineGateStopsDuringBreaker(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 1000\n")
	m.autoMu.Lock()
	m.autoBreakerUntil = time.Now().Add(time.Minute)
	m.autoMu.Unlock()
	if m.TryAcquireOnlineAttempt() {
		t.Fatal("breaker admitted an attempt")
	}
	// Even after the bucket would have refilled, the breaker keeps the gate shut.
	time.Sleep(120 * time.Millisecond)
	if m.TryAcquireOnlineAttempt() {
		t.Fatal("breaker admitted a refilled attempt")
	}
}

func TestAdaptivePolicyStretchesRetryPacingUnderFailurePressure(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 1500, MaxOnlineRobots: 2000}
	robotconfig.Normalize(&rc)
	decision := applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live: true, Running: 900, Actors: 1100, GamePortReady: true,
		OnlineSuccess: 20, OnlineFailed: 80,
	})
	if decision.Mode != schedulerPolicyPressure {
		t.Fatalf("mode got %s want pressure", decision.Mode)
	}
	if rc.SchedulerOnlineRetryBaseMS < 15000 {
		t.Fatalf("retry base got %d want >= 15000 under failure pressure", rc.SchedulerOnlineRetryBaseMS)
	}
	if rc.SchedulerOnlineRetryMaxMS != 900000 {
		t.Fatalf("retry max got %d want 900000", rc.SchedulerOnlineRetryMaxMS)
	}
	if rc.SchedulerOnlineInFlight > 12 {
		t.Fatalf("in flight got %d want reduced cap", rc.SchedulerOnlineInFlight)
	}
}

func TestAdaptivePolicyKeepsBaseRetryPacingWhenHealthy(t *testing.T) {
	rc := robotconfig.RuntimeConfig{AutoTargetOnlineCount: 1500, MaxOnlineRobots: 2000}
	robotconfig.Normalize(&rc)
	applyAdaptiveSchedulerConfig(&rc, adaptiveSchedulerSignals{
		Live: true, Running: 1500, Actors: 1500, GamePortReady: true,
	})
	if rc.SchedulerOnlineRetryBaseMS != 5000 {
		t.Fatalf("healthy retry base got %d want 5000", rc.SchedulerOnlineRetryBaseMS)
	}
	if rc.SchedulerOnlineRetryMaxMS != 300000 {
		t.Fatalf("healthy retry max got %d want 300000", rc.SchedulerOnlineRetryMaxMS)
	}
}

func TestOnlineBreakerPauseEscalatesOnRepeatedFailures(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_target_online_count = 1500\n[scheduler]\nonline_breaker_pause_sec = 30\n")
	rc := m.loadRobotConfig()
	counts := actormodel.LedgerCounts{Auto: 1500, Leased: 1500}
	now := time.Now()

	m.updateAutoBreaker(now, rc, counts, 1400, 0, 0, 30)
	first := time.Until(m.autoBreakerUntil)
	if first < 25*time.Second || first > 35*time.Second {
		t.Fatalf("first pause got %s want ~30s", first)
	}

	m.autoMu.Lock()
	m.autoBreakerUntil = now.Add(-time.Second)
	m.autoMu.Unlock()
	m.updateAutoBreaker(now, rc, counts, 1400, 0, 0, 30)
	second := time.Until(m.autoBreakerUntil)
	if second < 55*time.Second {
		t.Fatalf("second pause got %s want escalated ~60s", second)
	}

	m.autoMu.Lock()
	m.autoBreakerUntil = now.Add(-time.Second)
	m.autoMu.Unlock()
	m.updateAutoBreaker(now, rc, counts, 1400, 0, 30, 0)
	m.autoMu.Lock()
	streak := m.onlineBreakerStreak
	m.autoMu.Unlock()
	if streak != 0 {
		t.Fatalf("healthy window left streak %d want 0", streak)
	}
}
