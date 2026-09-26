package actor

import (
	"fmt"
	"runtime/debug"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	foundationlog "robot/internal/foundation/log"
)

type request struct {
	cmd        Command
	uid        int
	generation uint64
	done       chan robotcap.ActionResult
}

type control struct {
	kind controlKind
	uid  int
	done chan controlResult
}

type controlResult struct {
	uid int
	ok  bool
}

type runtimeForceCloser interface {
	ForceClose(uid int) bool
}

const stopReleaseMaxAttempts = 8

func (a *Actor) start() {
	go a.loop()
}

func (a *Actor) assignAndWait(uid int, timeout time.Duration) bool {
	if uid <= 0 {
		return false
	}
	res := a.controlAndWait(control{kind: controlAssign, uid: uid}, timeout)
	return res.ok
}

func (a *Actor) releaseAndWait(timeout time.Duration) int {
	a.setReleaseRequested(true)
	res := a.controlAndWait(control{kind: controlRelease}, timeout)
	return res.uid
}

func (a *Actor) controlAndWait(ctrl control, timeout time.Duration) controlResult {
	ctrl.done = make(chan controlResult, 1)
	var timer *time.Timer
	var deadline <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	// The control queue is intentionally small. Waiting here (bounded by the
	// caller's timeout) replaces the old silent drop, which could lose a
	// release request and leave the actor holding a UID forever.
	select {
	case a.ctrls <- ctrl:
	case <-a.done:
		return controlResult{}
	case <-deadline:
		return controlResult{}
	}
	if timeout <= 0 {
		select {
		case res := <-ctrl.done:
			return res
		case <-a.done:
			return controlResult{}
		}
	}
	select {
	case res := <-ctrl.done:
		return res
	case <-a.done:
		return controlResult{}
	case <-timer.C:
		return controlResult{}
	}
}

func (a *Actor) stopAndWait(timeout time.Duration) {
	a.requestStop()
	if timeout <= 0 {
		<-a.done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-a.done:
	case <-timer.C:
		foundationlog.Robotf("[Actor] stop_timeout slot=%d uid=%d timeout=%s\n", a.slotIDValue(), a.uidValue(), timeout)
	}
}

func (a *Actor) requestStop() {
	a.setReleaseRequested(true)
	a.once.Do(func() { close(a.stop) })
}

func (a *Actor) enqueue(cmd Command, timeout time.Duration) (robotcap.ActionResult, bool) {
	uid, generation := a.leaseIdentity()
	req := request{cmd: cmd, uid: uid, generation: generation, done: make(chan robotcap.ActionResult, 1)}
	select {
	case a.cmds <- req:
	default:
		return robotcap.ActionResult{UID: a.uidValue(), OK: false, State: robotcap.ActionStateQueueFailed}, false
	}
	if timeout <= 0 {
		return robotcap.ActionResult{UID: a.uidValue(), OK: true, State: robotcap.ActionStateAccepted}, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-req.done:
		return res, true
	case <-timer.C:
		return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStatePending, Message: "actor command is still running"}, true
	}
}

func (a *Actor) loop() {
	defer func() {
		if rec := recover(); rec != nil {
			a.handleLoopPanic("loop", rec)
			a.tryReleaseAfterFatalPanic()
		}
		close(a.done)
	}()
	// Read the poll interval when each tick is scheduled. The runtime config is
	// hot-reloadable, and keeping a ticker created at actor startup would make
	// SystemActorPollMS changes silently ineffective for existing actors.
	timer := time.NewTimer(a.pollInterval())
	defer timer.Stop()
	for {
		select {
		case <-a.stop:
			a.releaseCurrentUIDUntilClosed()
			return
		case ctrl := <-a.ctrls:
			res := a.handleControlSafely(ctrl)
			select {
			case ctrl.done <- res:
			default:
			}
		case req := <-a.cmds:
			res := a.handleRequestSafely(req)
			select {
			case req.done <- res:
			default:
			}
		case now := <-timer.C:
			a.tickSafely(now)
			timer.Reset(a.pollInterval())
		}
	}
}

func (a *Actor) handleRequestSafely(req request) robotcap.ActionResult {
	uid, generation := a.leaseIdentity()
	if req.uid <= 0 || req.uid != uid || req.generation != generation {
		return robotcap.ActionResult{
			UID: req.uid, OK: false, State: robotcap.ActionStateFailed,
			Message: "actor lease changed before command execution",
		}
	}
	result := a.handleCommandSafely(req.cmd)
	if req.cmd == CommandLogout && a.modeValue() == ModeManual {
		a.requestStop()
	}
	return result
}

func (a *Actor) pollInterval() (interval time.Duration) {
	interval = time.Second
	defer func() {
		if rec := recover(); rec != nil {
			a.handleLoopPanic("config", rec)
		}
	}()
	return actorPollInterval(a.runtime.Config())
}

func (a *Actor) handleControlSafely(ctrl control) (res controlResult) {
	defer func() {
		if rec := recover(); rec != nil {
			a.handleLoopPanic(fmt.Sprintf("control_%d", ctrl.kind), rec)
			res = controlResult{uid: a.uidValue()}
		}
	}()
	return a.handleControl(ctrl)
}

func (a *Actor) handleCommandSafely(cmd Command) (res robotcap.ActionResult) {
	defer func() {
		if rec := recover(); rec != nil {
			a.handleLoopPanic(fmt.Sprintf("command_%d", cmd), rec)
			res = robotcap.ActionResult{
				UID:     a.uidValue(),
				OK:      false,
				State:   robotcap.ActionStateFailed,
				Message: fmt.Sprintf("actor command panic: %v", rec),
			}
		}
	}()
	return a.handleCommand(cmd)
}

func (a *Actor) tickSafely(now time.Time) {
	defer func() {
		if rec := recover(); rec != nil {
			a.handleLoopPanic("tick", rec)
		}
	}()
	if a.stateValue() == StateReleasing {
		a.releaseCurrentUID()
		return
	}
	if a.releaseRequestedValue() {
		// A release control message can be lost when the control queue is
		// full; recover in the tick loop instead of idling forever.
		a.releaseCurrentUID()
		return
	}
	a.tick(now)
}

func (a *Actor) handleLoopPanic(operation string, rec interface{}) {
	a.stateMu.Lock()
	uid := a.uid
	a.busy = false
	a.busyKind = ""
	a.onlineDesired = false
	a.releaseRequested = uid > 0
	if uid > 0 {
		a.state = StateReleasing
	} else {
		a.state = StateIdle
	}
	slotID := a.slotID
	a.stateMu.Unlock()
	foundationlog.Robotf("[Actor] panic slot=%d uid=%d operation=%s err=%v\n%s", slotID, uid, operation, rec, debug.Stack())
}

func (a *Actor) tryReleaseAfterFatalPanic() {
	defer func() {
		if rec := recover(); rec != nil {
			foundationlog.Robotf("[Actor] panic_cleanup_failed slot=%d uid=%d err=%v\n", a.slotIDValue(), a.uidValue(), rec)
		}
	}()
	a.releaseCurrentUID()
}

func actorPollInterval(rc robotconfig.RuntimeConfig) time.Duration {
	intervalMS := rc.SystemActorPollMS
	if intervalMS <= 0 {
		intervalMS = 1000
	}
	if intervalMS < 100 {
		intervalMS = 100
	}
	return time.Duration(intervalMS) * time.Millisecond
}

func (a *Actor) handleControl(ctrl control) controlResult {
	switch ctrl.kind {
	case controlAssign:
		if ctrl.uid <= 0 {
			return controlResult{}
		}
		if old := a.uidValue(); old > 0 && old != ctrl.uid {
			if released := a.releaseCurrentUID(); released != old {
				return controlResult{uid: old}
			}
		}
		a.resetForUID(ctrl.uid)
		a.setReleaseRequested(false)
		return controlResult{uid: ctrl.uid, ok: true}
	case controlRelease:
		old := a.releaseCurrentUID()
		return controlResult{uid: old, ok: true}
	}
	return controlResult{}
}

func (a *Actor) releaseCurrentUID() int {
	uid := a.uidValue()
	if uid <= 0 {
		a.setState(StateIdle)
		return 0
	}
	a.setState(StateReleasing)
	cid := 0
	st, statusOK := a.runtime.Status(uid)
	if statusOK {
		cid = st.CID
	}
	a.finishStoreStateIfNeeded(uid, cid, st, statusOK, "release")
	if !a.releaseRuntimeUID(uid) {
		foundationlog.Robotf("[Actor] release_pending slot=%d uid=%d\n", a.slotIDValue(), uid)
		return 0
	}
	a.resetForUID(0)
	return uid
}

func (a *Actor) releaseCurrentUIDUntilClosed() {
	uid := a.uidValue()
	if uid <= 0 {
		a.setState(StateIdle)
		return
	}
	a.setState(StateReleasing)
	cid := 0
	st, statusOK := a.runtime.Status(uid)
	if statusOK {
		cid = st.CID
	}
	a.finishStoreStateIfNeeded(uid, cid, st, statusOK, "release")
	for attempt := 1; attempt <= stopReleaseMaxAttempts; attempt++ {
		if a.releaseRuntimeUID(uid) {
			a.resetForUID(0)
			return
		}
		foundationlog.Robotf("[Actor] stop_release_retry slot=%d uid=%d\n", a.slotIDValue(), uid)
		if attempt < stopReleaseMaxAttempts {
			time.Sleep(250 * time.Millisecond)
		}
	}
	a.quarantineCurrentUID()
	foundationlog.Robotf("[Actor] stop_release_quarantined slot=%d uid=%d attempts=%d\n", a.slotIDValue(), uid, stopReleaseMaxAttempts)
}

func (a *Actor) releaseRuntimeUID(uid int) bool {
	if result := a.runtime.Logout(uid); result.OK {
		return true
	}
	if closer, ok := a.runtime.(runtimeForceCloser); ok {
		return closer.ForceClose(uid)
	}
	return !a.runtime.IsActive(uid)
}

func (a *Actor) AssignAndWait(uid int, timeout time.Duration) bool {
	return a.assignAndWait(uid, timeout)
}

func (a *Actor) ReleaseAndWait(timeout time.Duration) int {
	return a.releaseAndWait(timeout)
}

func (a *Actor) StopAndWait(timeout time.Duration) {
	a.stopAndWait(timeout)
}

func (a *Actor) RequestStop() {
	a.requestStop()
}

func (a *Actor) Done() <-chan struct{} {
	return a.done
}

func (a *Actor) Enqueue(cmd Command, timeout time.Duration) (robotcap.ActionResult, bool) {
	return a.enqueue(cmd, timeout)
}

func (a *Actor) Start() {
	a.start()
}
