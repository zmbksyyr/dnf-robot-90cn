package scheduler

import (
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

type blockingActorStopRuntime struct {
	actorTestRuntime
	started chan int
	release <-chan struct{}
}

func (r *blockingActorStopRuntime) Logout(uid int) robotcap.ActionResult {
	r.started <- uid
	<-r.release
	return robotcap.ActionResult{UID: uid, OK: true}
}

func TestSupervisorPressureReleaseAllowsOnlyOneBatchAndStopWaits(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
	supervisorStarted := false
	runtime := &blockingActorStopRuntime{
		started: make(chan int, 2),
		release: release,
	}
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	defer func() {
		releaseAll()
		if supervisorStarted {
			supervisor.Stop()
		}
	}()
	actors := ensureSupervisorActors(t, supervisor, 2)
	for index, actor := range actors {
		uid := 101 + index
		if !actor.AssignAndWait(uid, time.Second) {
			t.Fatalf("assign actor uid=%d", uid)
		}
		if !supervisor.ledger.TryLeaseUID(uid, actor) {
			t.Fatalf("lease actor uid=%d", uid)
		}
	}

	supervisor.stopSomeAutoActors(1, 0)
	var drainingUID int
	select {
	case drainingUID = <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("first pressure release did not start")
	}
	if !supervisor.ledger.HasUID(drainingUID) {
		t.Fatalf("draining uid %d lost its lease before actor exit", drainingUID)
	}
	if supervisor.ledger.TryLeaseUID(drainingUID, actors[0]) {
		t.Fatalf("draining uid %d was leased a second time", drainingUID)
	}
	supervisor.stopSomeAutoActors(1, 0)
	counts := supervisor.ledger.Counts(time.Now(), robotconfig.RuntimeConfig{})
	if counts.Auto != 2 || counts.Draining != 1 {
		t.Fatalf("pressure release must retain draining capacity: actors=%d draining=%d", counts.Auto, counts.Draining)
	}

	supervisor.Start()
	supervisorStarted = true
	stopped := make(chan struct{})
	go func() {
		supervisor.Stop()
		close(stopped)
	}()
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("supervisor stop did not start remaining actor release")
	}
	select {
	case <-stopped:
		t.Fatal("supervisor stop returned while pressure release was in flight")
	case <-time.After(20 * time.Millisecond):
	}
	releaseAll()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor stop did not wait for actor releases")
	}

	supervisor.pressureMu.Lock()
	running := supervisor.pressureRunning
	supervisor.pressureMu.Unlock()
	if running {
		t.Fatal("pressure release remained marked in flight after stop")
	}
}

func TestSupervisorBreakerPreservesHealthyActorCapacity(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	manager := testRobotManagerWithConfig(t, "")
	manager.cfg.DFGameR = filepath.Join(t.TempDir(), "df_game_r")
	manager.cfg.RobotConnectIP = "127.0.0.1"
	manager.cfg.RobotGamePort = listener.Addr().(*net.TCPAddr).Port
	now := time.Now()
	manager.autoMu.Lock()
	manager.autoEnabled = true
	manager.autoPortSince = now.Add(-time.Minute)
	manager.autoBreakerUntil = now.Add(time.Minute)
	manager.autoMu.Unlock()

	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	actors := ensureSupervisorActors(t, supervisor, 3)
	for index, actor := range actors {
		uid := 101 + index
		if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
			t.Fatalf("assign actor uid=%d", uid)
		}
	}
	rc := robotconfig.RuntimeConfig{
		AutoActions:                  true,
		AutoTargetOnlineCount:        3,
		AutoGamePortCheckTimeoutMS:   100,
		AutoGamePortStableSec:        1,
		SchedulerBreakerReleaseBatch: 2,
		SchedulerBadRecoverSec:       60,
		SchedulerBadFailures:         3,
		SchedulerMetricsIntervalSec:  60,
	}

	if !supervisor.handleAutoGuards(now, rc, adaptiveSchedulerSignals{}) {
		t.Fatal("active breaker did not pause the supervisor loop")
	}
	counts := supervisor.ledger.Counts(time.Now(), rc)
	if counts.Auto != 3 || counts.Leased != 3 || counts.Draining != 0 {
		t.Fatalf("breaker changed healthy actor capacity: %+v", counts)
	}
}

func TestSupervisorBreakerStillConvergesDownToTarget(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	manager := testRobotManagerWithConfig(t, "")
	manager.cfg.DFGameR = filepath.Join(t.TempDir(), "df_game_r")
	manager.cfg.RobotConnectIP = "127.0.0.1"
	manager.cfg.RobotGamePort = listener.Addr().(*net.TCPAddr).Port
	now := time.Now()
	manager.autoMu.Lock()
	manager.autoEnabled = true
	manager.autoPortSince = now.Add(-time.Minute)
	manager.autoBreakerUntil = now.Add(time.Minute)
	manager.autoMu.Unlock()

	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	actors := ensureSupervisorActors(t, supervisor, 3)
	for index, actor := range actors {
		uid := 101 + index
		if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
			t.Fatalf("assign actor uid=%d", uid)
		}
	}
	rc := robotconfig.RuntimeConfig{
		AutoActions:                  true,
		AutoTargetOnlineCount:        1,
		AutoGamePortCheckTimeoutMS:   100,
		AutoGamePortStableSec:        1,
		SchedulerBreakerReleaseBatch: 2,
		SchedulerBadRecoverSec:       60,
		SchedulerBadFailures:         3,
		SchedulerMetricsIntervalSec:  60,
	}

	if !supervisor.handleAutoGuards(now, rc, adaptiveSchedulerSignals{Actors: 3}) {
		t.Fatal("active breaker did not pause the supervisor loop")
	}
	counts := supervisor.ledger.Counts(time.Now(), rc)
	if counts.Auto != 1 || counts.Leased != 1 || counts.Draining != 0 {
		t.Fatalf("breaker did not converge down to target: %+v", counts)
	}
}

type forceClosingActorRuntime struct {
	blockingActorStopRuntime
	forced chan int
}

func (r *forceClosingActorRuntime) ForceClose(uid int) bool {
	r.forced <- uid
	return true
}

func TestSupervisorShutdownIsBoundedAndKeepsStuckUIDLeased(t *testing.T) {
	release := make(chan struct{})
	runtime := &forceClosingActorRuntime{
		blockingActorStopRuntime: blockingActorStopRuntime{
			started: make(chan int, 1),
			release: release,
		},
		forced: make(chan int, 1),
	}
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	supervisor.shutdownTimeout = 100 * time.Millisecond
	supervisor.shutdownForceGrace = 40 * time.Millisecond
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("assign shutdown test actor")
	}
	supervisor.Start()

	startedAt := time.Now()
	err := supervisor.StopWithError()
	if err == nil {
		t.Fatal("bounded shutdown should report the stuck actor")
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("shutdown exceeded its bound: %s", elapsed)
	}
	select {
	case got := <-runtime.forced:
		if got != uid {
			t.Fatalf("force close uid got %d want %d", got, uid)
		}
	default:
		t.Fatal("shutdown did not attempt runtime force close")
	}
	if !supervisor.ledger.HasUID(uid) || !supervisor.ledger.IsDraining(actor) {
		t.Fatal("stuck actor must retain its uid lease in draining state")
	}
	if supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("stuck uid was reusable before actor exit")
	}

	close(release)
	select {
	case <-actor.Done():
	case <-time.After(time.Second):
		t.Fatal("actor did not exit after releasing blocked logout")
	}
	if !supervisor.ledger.ReapActor(actor) || supervisor.ledger.HasUID(uid) {
		t.Fatal("completed draining actor was not reaped")
	}
}

func TestSupervisorShutdownBroadcastsBeforeWaiting(t *testing.T) {
	const actorCount = 61
	release := make(chan struct{})
	runtime := &blockingActorStopRuntime{
		started: make(chan int, actorCount),
		release: release,
	}
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	actors := ensureSupervisorActors(t, supervisor, actorCount)
	for index, actor := range actors {
		uid := 1001 + index
		if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
			t.Fatalf("assign actor uid=%d", uid)
		}
	}
	supervisor.Start()
	stopped := make(chan error, 1)
	go func() { stopped <- supervisor.StopWithError() }()

	seen := make(map[int]struct{}, actorCount)
	deadline := time.After(2 * time.Second)
	for len(seen) < actorCount {
		select {
		case uid := <-runtime.started:
			seen[uid] = struct{}{}
		case <-deadline:
			t.Fatalf("only %d/%d actors received stop before any logout completed", len(seen), actorCount)
		}
	}
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned before blocked actors exited: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown after broadcast: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not finish after releasing all actors")
	}
}

func TestManagerRetainsSupervisorAfterIncompleteStop(t *testing.T) {
	release := make(chan struct{})
	runtime := &blockingActorStopRuntime{
		started: make(chan int, 1),
		release: release,
	}
	manager := testRobotManagerWithConfig(t, "")
	supervisor := NewRobotSupervisor(manager, runtime)
	supervisor.shutdownTimeout = 50 * time.Millisecond
	supervisor.shutdownForceGrace = 20 * time.Millisecond
	actor := ensureSupervisorActors(t, supervisor, 1)[0]
	const uid = 101
	if !actor.AssignAndWait(uid, time.Second) || !supervisor.ledger.TryLeaseUID(uid, actor) {
		t.Fatal("assign stop test actor")
	}
	manager.supervisor = supervisor
	supervisor.Start()

	if err := manager.stopAutoActions(); err == nil {
		t.Fatal("incomplete stop should return an error")
	}
	if manager.supervisor != supervisor {
		t.Fatal("incomplete stop cleared the supervisor")
	}
	manager.StartAutoActions()
	if manager.supervisor != supervisor {
		t.Fatal("start replaced an incompletely stopped supervisor")
	}

	close(release)
	select {
	case <-actor.Done():
	case <-time.After(time.Second):
		t.Fatal("actor did not finish after release")
	}
}
