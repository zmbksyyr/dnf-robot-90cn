package robotconfig

import (
	"testing"
	"time"
)

func TestOnlineStartRate(t *testing.T) {
	tests := []struct {
		name string
		rate int
		want int
	}{
		{name: "default", rate: 0, want: 20},
		{name: "configured", rate: 8, want: 8},
		{name: "hard cap", rate: 99, want: 60},
		{name: "frozen", rate: -1, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OnlineStartRate(RuntimeConfig{SchedulerOnlineStartRate: tt.rate})
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestOnlineStartRateForNeed(t *testing.T) {
	tests := []struct {
		name    string
		need    int
		rate    int
		timeout int
		want    int
	}{
		{name: "fill 600 in 60 seconds", need: 600, rate: 20, timeout: 60, want: 20},
		{name: "small target keeps configured rate", need: 100, rate: 20, timeout: 60, want: 20},
		{name: "large target raises rate", need: 3000, rate: 20, timeout: 60, want: 50},
		{name: "hard cap", need: 6000, rate: 20, timeout: 60, want: 60},
		{name: "invalid timeout uses default", need: 600, rate: 1, timeout: 0, want: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := OnlineStartRateForNeed(tt.need, RuntimeConfig{
				SchedulerOnlineStartRate:   tt.rate,
				SchedulerOnlineFillTimeout: tt.timeout,
			})
			if got != tt.want {
				t.Fatalf("got %d want %d", got, tt.want)
			}
		})
	}
}

func TestScaleBatches(t *testing.T) {
	if got := ScaleUpBatch(RuntimeConfig{SchedulerOnlineBatchSize: 30}); got != 30 {
		t.Fatalf("scale up configured got %d want 30", got)
	}
	if got := ScaleUpBatch(RuntimeConfig{SchedulerOnlineBatchSize: 999}); got != 120 {
		t.Fatalf("scale up cap got %d want 120", got)
	}
	if got := ScaleUpBatch(RuntimeConfig{SchedulerOnlineBatchSize: -1}); got != 0 {
		t.Fatalf("scale up frozen got %d want 0", got)
	}
	if got := ScaleDownBatch(600, 20); got != 24 {
		t.Fatalf("scale down 600->20 got %d want 24", got)
	}
	if got := ScaleDownBatch(30, 20); got != 5 {
		t.Fatalf("scale down minimum got %d want 5", got)
	}
	if got := ScaleDownBatch(20, 600); got != 0 {
		t.Fatalf("scale down grow path got %d want 0", got)
	}
}

func TestCreateRoomRespectsTargetCapacity(t *testing.T) {
	rc := RuntimeConfig{AutoTargetOnlineCount: 200, MaxOnlineRobots: 600}
	if got := CreateRoom(rc, 189); got != 11 {
		t.Fatalf("create room got %d want 11", got)
	}
	if got := CreateRoom(rc, 200); got != 0 {
		t.Fatalf("create room at target got %d want 0", got)
	}
	if got := CreateRoom(rc, 301); got != 0 {
		t.Fatalf("create room above target got %d want 0", got)
	}
	if got := TargetCapacity(RuntimeConfig{AutoTargetOnlineCount: 600, MaxOnlineRobots: 200}); got != 200 {
		t.Fatalf("target capacity got %d want max cap 200", got)
	}
}

func TestNormalizeRobotUIDSegment(t *testing.T) {
	rc := RuntimeConfig{RobotUIDStart: 17000000}
	Normalize(&rc)
	if rc.RobotUIDStart != 17000000 || rc.RobotUIDEnd != 17000999 {
		t.Fatalf("uid segment got %d-%d want 17000000-17000999", rc.RobotUIDStart, rc.RobotUIDEnd)
	}
}

func TestOnlineRetryBackoffGrowsAndCaps(t *testing.T) {
	rc := RuntimeConfig{SchedulerOnlineRetryBaseMS: 5000, SchedulerOnlineRetryMaxMS: 300000, SchedulerOnlineRetryJitterPct: 0}
	want := []int{5000, 10000, 20000, 40000, 80000, 160000, 300000, 300000}
	for failures := 1; failures <= len(want); failures++ {
		got := OnlineRetryBackoff(rc, failures, nil)
		if got != time.Duration(want[failures-1])*time.Millisecond {
			t.Fatalf("failures=%d got %s want %dms", failures, got, want[failures-1])
		}
	}
}

func TestOnlineRetryBackoffJitterStaysBounded(t *testing.T) {
	rc := RuntimeConfig{SchedulerOnlineRetryBaseMS: 5000, SchedulerOnlineRetryMaxMS: 300000, SchedulerOnlineRetryJitterPct: 20}
	centerConfig := rc
	centerConfig.SchedulerOnlineRetryJitterPct = 0
	randIntn := func(n int) int { return n - 1 }
	for failures := 1; failures <= 6; failures++ {
		center := OnlineRetryBackoff(centerConfig, failures, nil)
		low := OnlineRetryBackoff(rc, failures, func(int) int { return 0 })
		high := OnlineRetryBackoff(rc, failures, randIntn)
		if low < center-center/5 || high > center+center/5 {
			t.Fatalf("failures=%d jitter got %s..%s center %s", failures, low, high, center)
		}
	}
}

func TestNormalizeClampsOnlinePacing(t *testing.T) {
	rc := RuntimeConfig{
		SchedulerOnlineRetryBaseMS:     100,
		SchedulerOnlineRetryMaxMS:      50,
		SchedulerOnlineRetryJitterPct:  99,
		SchedulerOnlineInFlight:        999,
		SchedulerRecycleCooldownSec:    -1,
		SchedulerOnlineBreakerPauseSec: 1,
		SchedulerCreateBatchSize:       999,
	}
	Normalize(&rc)
	if rc.SchedulerOnlineRetryBaseMS != 5000 {
		t.Fatalf("retry base got %d want 5000", rc.SchedulerOnlineRetryBaseMS)
	}
	if rc.SchedulerOnlineRetryMaxMS != 300000 {
		t.Fatalf("retry max got %d want 300000", rc.SchedulerOnlineRetryMaxMS)
	}
	if rc.SchedulerOnlineRetryJitterPct != 50 {
		t.Fatalf("retry jitter got %d want 50", rc.SchedulerOnlineRetryJitterPct)
	}
	if rc.SchedulerOnlineInFlight != 64 {
		t.Fatalf("online in flight got %d want 64", rc.SchedulerOnlineInFlight)
	}
	if rc.SchedulerRecycleCooldownSec != 600 {
		t.Fatalf("recycle cooldown got %d want 600", rc.SchedulerRecycleCooldownSec)
	}
	if rc.SchedulerOnlineBreakerPauseSec != 15 {
		t.Fatalf("online breaker pause got %d want 15", rc.SchedulerOnlineBreakerPauseSec)
	}
	if rc.SchedulerCreateBatchSize != 40 {
		t.Fatalf("create batch got %d want 40", rc.SchedulerCreateBatchSize)
	}
}
