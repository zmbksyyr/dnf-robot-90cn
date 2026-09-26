package scheduler

import (
	robotcap "robot/internal/capability/robot"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingStatusRuntime struct {
	noopRuntime
	statuses []robotcap.RuntimeStatus
	calls    atomic.Int32
	started  chan struct{}
	release  chan struct{}
}

type mapStatusRuntime struct {
	noopRuntime
	statuses   map[int]robotcap.RuntimeStatus
	mapCalls   atomic.Int32
	sliceCalls atomic.Int32
}

func (r *mapStatusRuntime) RuntimeStatus() []robotcap.RuntimeStatus {
	r.sliceCalls.Add(1)
	return nil
}

func (r *mapStatusRuntime) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	r.mapCalls.Add(1)
	return r.statuses
}

func TestRuntimeStateTableOwnsAdapterSnapshot(t *testing.T) {
	runtime := &mapStatusRuntime{statuses: map[int]robotcap.RuntimeStatus{
		17000001: {UID: 17000001, StateName: robotcap.RuntimeStateRunning},
	}}
	manager := NewRobotManager(nil, nil, runtime)
	snapshot := manager.runtimeStatusMap()
	runtime.statuses[17000001] = robotcap.RuntimeStatus{UID: 17000001, StateName: robotcap.RuntimeStateStop}
	delete(runtime.statuses, 17000001)
	if status, ok := snapshot[17000001]; !ok || status.StateName != robotcap.RuntimeStateRunning {
		t.Fatalf("adapter mutation changed runtime state table: status=%+v ok=%t", status, ok)
	}
}

func (r *countingStatusRuntime) RuntimeStatus() []robotcap.RuntimeStatus {
	r.calls.Add(1)
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		<-r.release
	}
	return append([]robotcap.RuntimeStatus(nil), r.statuses...)
}

func TestRuntimeStatusRefreshIsSingleflight(t *testing.T) {
	runtime := &countingStatusRuntime{
		statuses: []robotcap.RuntimeStatus{{UID: 17000001, StateName: robotcap.RuntimeStateRunning}},
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
	manager := NewRobotManager(nil, nil, runtime)

	const readers = 64
	start := make(chan struct{})
	errs := make(chan string, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, ok := manager.runtimeStatus(17000001)
			if !ok || status.UID != 17000001 {
				errs <- "runtime status missing"
			}
		}()
	}
	close(start)
	<-runtime.started
	close(runtime.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if got := runtime.calls.Load(); got != 1 {
		t.Fatalf("RuntimeStatus calls got %d want 1", got)
	}
}

func TestRuntimeStatusRefreshUsesMapProviderWithoutSliceRoundTrip(t *testing.T) {
	runtime := &mapStatusRuntime{statuses: map[int]robotcap.RuntimeStatus{
		17000001: {UID: 17000001, StateName: robotcap.RuntimeStateRunning},
	}}
	manager := NewRobotManager(nil, nil, runtime)
	status, ok := manager.runtimeStatus(17000001)
	if !ok || status.UID != 17000001 {
		t.Fatalf("runtime status = %+v, ok=%t", status, ok)
	}
	if runtime.mapCalls.Load() != 1 || runtime.sliceCalls.Load() != 0 {
		t.Fatalf("runtime calls map=%d slice=%d", runtime.mapCalls.Load(), runtime.sliceCalls.Load())
	}
}

func TestRuntimeStatusMapCopyDoesNotMutateSnapshot(t *testing.T) {
	runtime := &countingStatusRuntime{statuses: []robotcap.RuntimeStatus{
		{UID: 17000001, StateName: robotcap.RuntimeStateRunning},
		{UID: 17000002, StateName: robotcap.RuntimeStateLogin},
	}}
	manager := NewRobotManager(nil, nil, runtime)

	mutable := manager.runtimeStatusMapCopy()
	delete(mutable, 17000001)
	mutable[17000003] = robotcap.RuntimeStatus{UID: 17000003}

	snapshot := manager.runtimeStatusMap()
	if _, ok := snapshot[17000001]; !ok {
		t.Fatal("deleting from mutable copy changed cached snapshot")
	}
	if _, ok := snapshot[17000003]; ok {
		t.Fatal("adding to mutable copy changed cached snapshot")
	}
	if got := runtime.calls.Load(); got != 1 {
		t.Fatalf("RuntimeStatus calls got %d want 1", got)
	}
}

func TestRuntimeStatusRejectsInvalidUIDWithoutRefresh(t *testing.T) {
	runtime := &countingStatusRuntime{}
	manager := NewRobotManager(nil, nil, runtime)
	if _, ok := manager.runtimeStatus(0); ok {
		t.Fatal("zero UID unexpectedly resolved")
	}
	if got := runtime.calls.Load(); got != 0 {
		t.Fatalf("RuntimeStatus calls got %d want 0", got)
	}
}

func TestCountRuntimeRunningUsesCachedSnapshot(t *testing.T) {
	runtime := &countingStatusRuntime{statuses: []robotcap.RuntimeStatus{
		{UID: 17000001, StateName: robotcap.RuntimeStateRunning},
		{UID: 17000002, StateName: robotcap.RuntimeStateLogin},
	}}
	manager := NewRobotManager(nil, nil, runtime)
	if got := manager.countRuntimeRunning(); got != 1 {
		t.Fatalf("running count = %d", got)
	}
	if got := manager.countRuntimeRunning(); got != 1 {
		t.Fatalf("cached running count = %d", got)
	}
	if got := runtime.calls.Load(); got != 1 {
		t.Fatalf("RuntimeStatus calls got %d want 1", got)
	}
}

func TestAutoAndSystemStatusShareRuntimeSummary(t *testing.T) {
	runtime := &countingStatusRuntime{statuses: []robotcap.RuntimeStatus{
		{UID: 17000001, StateName: robotcap.RuntimeStateRunning},
		{UID: 17000002, StateName: robotcap.RuntimeStateRunning, RobotType: 2, StoreDisplayAck: true},
		{UID: 17000003, StateName: robotcap.RuntimeStateRunning, RobotType: 3, DisjointActive: true},
		{UID: 17000004, StateName: robotcap.RuntimeStateLogin},
	}}
	manager := testRobotManagerWithConfig(t, "")
	manager.doll = runtime

	auto := manager.AutoStatus()
	system := manager.SystemStatus()
	if auto.Running != 3 || auto.StoreRunning != 2 || auto.StoreItemRunning != 1 || auto.StoreDisjointRunning != 1 || system.Running != 3 || system.Store != 2 {
		t.Fatalf("auto=%+v system=%+v", auto, system)
	}
	if got := runtime.calls.Load(); got != 1 {
		t.Fatalf("RuntimeStatus calls got %d want 1", got)
	}
}

func TestRuntimeStatusRefreshServesStaleAfterTimeout(t *testing.T) {
	previousTimeout := runtimeStatusRefreshTimeout
	runtimeStatusRefreshTimeout = 40 * time.Millisecond
	defer func() { runtimeStatusRefreshTimeout = previousTimeout }()

	runtime := &countingStatusRuntime{statuses: []robotcap.RuntimeStatus{
		{UID: 17000001, StateName: robotcap.RuntimeStateRunning},
	}}
	manager := NewRobotManager(nil, nil, runtime)
	if status, ok := manager.runtimeStatus(17000001); !ok || status.UID != 17000001 {
		t.Fatalf("initial status = %+v ok=%t", status, ok)
	}

	release := make(chan struct{})
	runtime.release = release
	manager.invalidateRuntimeStatusCache()

	start := time.Now()
	status, ok := manager.runtimeStatus(17000001)
	elapsed := time.Since(start)
	close(release)
	if !ok || status.UID != 17000001 {
		t.Fatalf("stale status = %+v ok=%t", status, ok)
	}
	if elapsed > time.Second {
		t.Fatalf("stale lookup blocked for %v", elapsed)
	}

	// The stale snapshot is served from cache until the TTL expires.
	start = time.Now()
	if _, ok := manager.runtimeStatus(17000001); !ok {
		t.Fatal("cached stale status missing")
	}
	if elapsed := time.Since(start); elapsed > runtimeStatusRefreshTimeout {
		t.Fatalf("cached stale lookup blocked for %v", elapsed)
	}
}

func BenchmarkRuntimeStatusLookup550(b *testing.B) {
	manager := benchmarkRuntimeStatusManager(b)
	if _, ok := manager.runtimeStatus(17000275); !ok {
		b.Fatal("benchmark status missing")
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = manager.runtimeStatus(17000275)
	}
}

func BenchmarkRuntimeStatusMutableCopy550(b *testing.B) {
	manager := benchmarkRuntimeStatusManager(b)
	_ = manager.runtimeStatusMap()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = manager.runtimeStatusMapCopy()
	}
}

func BenchmarkRuntimeStatusRefresh550(b *testing.B) {
	statuses := make([]robotcap.RuntimeStatus, 550)
	statusMap := make(map[int]robotcap.RuntimeStatus, len(statuses))
	for i := range statuses {
		statuses[i] = robotcap.RuntimeStatus{UID: 17000000 + i, StateName: robotcap.RuntimeStateRunning}
		statusMap[statuses[i].UID] = statuses[i]
	}
	b.Run("slice", func(b *testing.B) {
		manager := NewRobotManager(nil, nil, &countingStatusRuntime{statuses: statuses})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			manager.invalidateRuntimeStatusCache()
			_ = manager.runtimeStatusMap()
		}
	})
	b.Run("map", func(b *testing.B) {
		manager := NewRobotManager(nil, nil, &mapStatusRuntime{statuses: statusMap})
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			manager.invalidateRuntimeStatusCache()
			_ = manager.runtimeStatusMap()
		}
	})
}

func benchmarkRuntimeStatusManager(b *testing.B) *RobotManager {
	b.Helper()
	statuses := make([]robotcap.RuntimeStatus, 550)
	for i := range statuses {
		statuses[i] = robotcap.RuntimeStatus{UID: 17000000 + i, StateName: robotcap.RuntimeStateRunning}
	}
	return NewRobotManager(nil, nil, &countingStatusRuntime{statuses: statuses})
}
