package scheduler

import (
	"testing"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/shared"
)

type slowLogoutRuntime struct {
	noopRuntime
	delay         time.Duration
	logoutEntered chan struct{}
	logoutRelease chan struct{}
	statuses      map[int]robotcap.RuntimeStatus
	statusBlock   chan struct{}
	statusEntered chan struct{}
}

func (r *slowLogoutRuntime) Logout(uid int) error {
	if r.logoutEntered != nil {
		select {
		case r.logoutEntered <- struct{}{}:
		default:
		}
	}
	if r.logoutRelease != nil {
		<-r.logoutRelease
	}
	time.Sleep(r.delay)
	return nil
}

func (r *slowLogoutRuntime) Online([]shared.RuntimeOnlineUser) error { return nil }

func (r *slowLogoutRuntime) ForceClose(int) bool { return true }

func (r *slowLogoutRuntime) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	if r.statusBlock != nil {
		if r.statusEntered != nil {
			select {
			case r.statusEntered <- struct{}{}:
			default:
			}
		}
		<-r.statusBlock
	}
	return r.statuses
}

func TestLogoutManagedRunsBatchConcurrently(t *testing.T) {
	robots := []robotcap.Info{
		{UID: 101, CID: 1}, {UID: 102, CID: 2}, {UID: 103, CID: 3}, {UID: 104, CID: 4},
	}
	runtime := &slowLogoutRuntime{
		delay:         100 * time.Millisecond,
		logoutEntered: make(chan struct{}, 1),
		logoutRelease: make(chan struct{}),
		statuses:      make(map[int]robotcap.RuntimeStatus, len(robots)),
	}
	released := false
	defer func() {
		if !released {
			close(runtime.logoutRelease)
		}
	}()
	for _, robot := range robots {
		runtime.statuses[robot.UID] = robotcap.RuntimeStatus{UID: robot.UID, StateName: robotcap.RuntimeStateRunning}
	}

	// A short actor poll interval lets the assigned actors settle into the
	// running state before the command guard accepts work.
	manager := testRobotManagerWithConfig(t, "[system]\nactor_poll_ms = 20\n")
	manager.doll = runtime
	manager.robotState = robotstate.NewMemoryStore(robots)
	manager.autoEnabled = false
	manager.characterCacheInvalidate = func(int) error { return nil }

	supervisor := NewRobotSupervisor(manager, NewRobotRuntime(manager))
	manager.supervisor = supervisor
	actors := ensureSupervisorActors(t, supervisor, len(robots))
	for index, actor := range actors {
		if !actor.AssignAndWait(robots[index].UID, time.Second) {
			t.Fatalf("assign actor uid=%d", robots[index].UID)
		}
		if !supervisor.ledger.TryLeaseUID(robots[index].UID, actor) {
			t.Fatalf("lease uid=%d", robots[index].UID)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		settled := true
		for _, actor := range actors {
			if actor.Snapshot().State != actormodel.StateRunning {
				settled = false
				break
			}
		}
		if settled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("actors did not settle into running: %+v", actors[0].Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Logout confirmation needs the runtime to report the sessions as gone.
	runtime.statuses = map[int]robotcap.RuntimeStatus{}
	manager.invalidateRuntimeStatusCache()

	start := time.Now()
	type logoutOutcome struct {
		result robotcap.CommandResult
		err    error
	}
	completed := make(chan logoutOutcome, 1)
	go func() {
		result, err := manager.LogoutManaged(robotcap.CommandRequest{UIDs: []int{101, 102, 103, 104}})
		completed <- logoutOutcome{result: result, err: err}
	}()
	select {
	case <-runtime.logoutEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("logout did not reach the runtime")
	}
	lockAcquired := make(chan struct{})
	go func() {
		manager.mutationMu.Lock()
		manager.mutationMu.Unlock()
		close(lockAcquired)
	}()
	select {
	case <-lockAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("structural lock stayed held during logout confirmation")
	}
	close(runtime.logoutRelease)
	released = true
	outcome := <-completed
	result, err := outcome.result, outcome.err
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Robots) != len(robots) {
		t.Fatalf("logout result = %+v, want %d robots", result, len(robots))
	}
	if result.Accepted != len(robots) {
		t.Fatalf("logout result = %+v, want all robots accepted", result)
	}
	t.Logf("batch logout elapsed=%s result=%+v", elapsed, result)
	// Serial dispatch costs 4 x (100ms runtime + 500ms safety sleep) = ~2.4s.
	// Concurrent dispatch must finish well below that.
	if elapsed > 1500*time.Millisecond {
		t.Fatalf("batch logout took %s, want concurrent dispatch", elapsed)
	}
}
