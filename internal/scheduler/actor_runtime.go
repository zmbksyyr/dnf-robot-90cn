package scheduler

import (
	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
	"runtime/debug"
	"strings"
	"time"
)

type RobotRuntime struct {
	manager  *RobotManager
	uidLocks *lockhub.RefHub
}

var _ actormodel.RobotRuntime = (*RobotRuntime)(nil)

func NewRobotRuntime(manager *RobotManager) *RobotRuntime {
	return &RobotRuntime{manager: manager, uidLocks: lockhub.NewRefHub()}
}

func (r *RobotRuntime) Config() robotconfig.RuntimeConfig {
	return r.manager.loadRobotConfig()
}

func (r *RobotRuntime) Status(uid int) (robotcap.RuntimeStatus, bool) {
	return r.manager.runtimeStatus(uid)
}

func (r *RobotRuntime) PartyActive(uid int) bool {
	if provider, ok := r.manager.actions.(interface{ PartyActive(int) bool }); ok {
		return provider.PartyActive(uid)
	}
	return false
}

func (r *RobotRuntime) IsActive(uid int) bool {
	st, ok := r.Status(uid)
	if !ok {
		return false
	}
	return robotcap.ActiveRuntimeStatus(st)
}

func (r *RobotRuntime) FinishStoreState(uid, cid int, reason string) {
	r.manager.finishStoreState(uid, cid, reason)
}

func (r *RobotRuntime) AddAutoOnline(success, failed int) {
	r.manager.addAutoOnline(success, failed)
}

func (r *RobotRuntime) AddOnlineAttempt(success bool) {
	r.manager.addAutoOnlineAttempt(success)
}

// TryAcquireOnlineAttempt and ReleaseOnlineAttempt expose the scheduler-owned
// online attempt gate to the actor model. The actor reaches the runtime through
// this type, so the methods must live here and not only on RobotManager.
func (r *RobotRuntime) TryAcquireOnlineAttempt() bool {
	return r.manager.TryAcquireOnlineAttempt()
}

func (r *RobotRuntime) ReleaseOnlineAttempt() {
	r.manager.ReleaseOnlineAttempt()
}

func (r *RobotRuntime) AutoActionsEnabled(rc robotconfig.RuntimeConfig) bool {
	return r.manager.autoActionsEnabled(rc)
}

func (r *RobotRuntime) RandomShoutMessage(randIntn func(int) int) string {
	tpl := r.manager.loadShoutTemplates()
	if len(tpl.Messages) == 0 {
		return ""
	}
	idx := 0
	if randIntn != nil {
		idx = randIntn(len(tpl.Messages))
	}
	return robottemplate.SafeShoutMessage(tpl.Messages[idx])
}

func (r *RobotRuntime) OnlineNoConfirm(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.sessionService().Online(robotcap.CommandRequest{UIDs: []int{uid}}, false, r.Config())
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Logout(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.sessionService().LogoutUID(uid)
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) ForceClose(uid int) bool {
	if r == nil || r.manager == nil || r.manager.sessions == nil {
		return false
	}
	return r.manager.sessions.ForceClose(uid)
}

func (r *RobotRuntime) Move(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityTownMove); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		res, err := r.manager.moveService().Move(robotcap.CommandRequest{UIDs: []int{uid}}, r.Config())
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Shout(uid int, world bool) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if world {
			if err := r.manager.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
			}
		} else if err := r.manager.requireBackendCapability(shared.CapabilityShout); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		res, err := r.manager.shoutService().ShoutOne(robotcap.CommandRequest{UIDs: []int{uid}}, world)
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) Store(uid int) robotcap.ActionResult {
	if !r.manager.itemStoreSupported() {
		// Disjoint-only adapters route the manual store command to the
		// disassembler machine instead of the unavailable private stall.
		return r.run(uid, func() robotcap.ActionResult {
			st, ok := r.Status(uid)
			if !ok || !robotcap.ActiveRuntimeStatus(st) || st.PartyActive || r.PartyActive(uid) {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
			}
			return r.autoDisjointStore(uid, st, nil)
		})
	}
	return r.run(uid, func() robotcap.ActionResult {
		res, err := r.manager.storeWorkflow().Store(robotcap.CommandRequest{UIDs: []int{uid}})
		return firstActionResult(uid, res, err)
	})
}

func (r *RobotRuntime) AutoMove(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityTownMove); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		rc := r.Config()
		maps := r.manager.loadMapCatalog()
		target, hasTarget := r.manager.currentFollowTarget(rc, maps)
		info := robotcap.Info{UID: st.UID, CID: st.CID, GuildID: st.GuildID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y}
		var err error
		if hasTarget {
			err = r.manager.moveService().AutoMove(info, rc, maps, &target)
		} else {
			err = r.manager.moveService().AutoMove(info, rc, maps, nil)
		}
		if err != nil {
			r.manager.addAutoMove(0, 1)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		r.manager.addAutoMove(1, 0)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateMoved}
	})
}

func (r *RobotRuntime) AutoShout(uid int, world bool, msg string) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if world {
			if err := r.manager.requireBackendCapability(shared.CapabilityWorldShout); err != nil {
				world = false
			}
		}
		if !world {
			if err := r.manager.requireBackendCapability(shared.CapabilityShout); err != nil {
				return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
			}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			r.manager.addAutoShoutChannel(world, 0, 1)
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		tpl := r.manager.loadShoutTemplates()
		if msg == "" && len(tpl.Messages) > 0 {
			msg = robottemplate.SafeShoutMessage(tpl.Messages[0])
		}
		if err := r.manager.shoutService().AutoShout(uid, msg, world); err != nil {
			r.manager.addAutoShoutChannel(world, 0, 1)
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
		}
		r.manager.addAutoShoutChannel(world, 1, 0)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: true, State: robotcap.ActionStateSent}
	})
}

func (r *RobotRuntime) AutoStore(uid int, shouldStop func() bool) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		if err := r.manager.requireBackendCapability(shared.CapabilityStore); err != nil {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateCancelled, Message: err.Error()}
		}
		st, ok := r.Status(uid)
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 || st.PartyActive || r.PartyActive(uid) {
			return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateOffline}
		}
		if shouldStop != nil && shouldStop() {
			return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		disjoint, releaseStoreType := r.manager.beginAdaptiveStoreType()
		defer releaseStoreType()
		if disjoint {
			return r.autoDisjointStore(uid, st, shouldStop)
		}
		return r.autoItemStore(st, shouldStop)
	})
}

func (r *RobotRuntime) autoItemStore(st robotcap.RuntimeStatus, shouldStop func() bool) robotcap.ActionResult {
	switch r.manager.storeWorkflow().AutoUntilSuccess(st, r.Config(), shouldStop) {
	case storecap.AutoAttemptSuccess:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: true, State: robotcap.ActionStateStore}
	case storecap.AutoAttemptBusy:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	case storecap.AutoAttemptCancelled:
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
	}
	if shouldStop != nil && shouldStop() {
		return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateCancelled}
	}
	return robotcap.ActionResult{UID: st.UID, CID: st.CID, OK: false, State: robotcap.ActionStateStoreFailed}
}

// claimStorePoint prefers a point in the robot's current area so store
// attempts avoid a server-side area transition under load, then falls back to
// the globally balanced pool.
func (r *RobotRuntime) claimStorePoint(points *storecap.PointCoordinator, uid int, rc robotconfig.RuntimeConfig, info robotcap.Info, allowed func(storecap.Position) bool) (storecap.Position, bool) {
	preferVillage, preferArea := info.Village, info.Area
	if status, ok := r.manager.runtimeStatus(uid); ok {
		preferVillage, preferArea = status.Village, status.Area
	}
	if pos, ok := points.ClaimForStoreInAreaWhere(uid, rc.AutoStoreDurationSec, preferVillage, preferArea, allowed); ok {
		return pos, true
	}
	return points.ClaimForStoreWhere(uid, rc.AutoStoreDurationSec, allowed)
}

func (r *RobotRuntime) autoDisjointStore(uid int, st robotcap.RuntimeStatus, shouldStop func() bool) robotcap.ActionResult {
	rc := r.Config()
	info := robotcap.Info{UID: uid, CID: st.CID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y, Port: r.manager.cfg.RobotGamePort}
	if robots, err := r.manager.selectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
		info = robots[0]
		info.Port = r.manager.cfg.RobotGamePort
	}
	if !r.manager.beginStoreBusy(uid) {
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	}
	// The proven baseline shared the normal store concurrency window. A later
	// independent cap of four made disjoint preparation visibly starve.
	releaseSlot, ok := r.manager.acquireAutoStoreSlot(rc)
	if !ok {
		r.manager.endStoreBusy(uid)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: false, State: robotcap.ActionStateStoreBusy}
	}
	defer func() {
		releaseSlot()
		r.manager.endStoreBusy(uid)
	}()

	points := r.manager.storePoints()
	tries := rc.AutoStoreMaxPositionTries
	if tries <= 0 {
		tries = 10
	}
	var failureState storecap.AttemptFailureState
	reuseSession := false
	setAreaStalls := 0
	allowedPosition := func(pos storecap.Position) bool { return shared.GenericAreaAllowed(info.GuildID, pos.Village) }
	for try := 1; try <= tries; try++ {
		if shouldStop != nil && shouldStop() {
			points.DiscardAttemptFailure(uid, &failureState)
			points.Flush()
			r.cleanupStoreSession(info, rc, "cancelled")
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		pos, ok := r.claimStorePoint(points, uid, rc, info, allowedPosition)
		if !ok {
			break
		}
		info.Village, info.Area, info.X, info.Y = pos.Village, pos.Area, pos.X, pos.Y
		var reason string
		if reuseSession {
			ok, reason = r.tryDisjointPositionInCurrentSession(info, shouldStop)
		} else {
			ok, reason = r.tryDisjointPosition(info, rc, shouldStop)
		}
		if ok {
			points.CommitAttemptFailure(uid, &failureState)
			points.Report(uid, pos, true, storecap.StoreReasonDisjointAck)
			r.manager.addAutoStore(1, 0, 0)
			robotLogf("[DISJOINT_SUCCESS_POINT] uid=%d point=%s village=%d area=%d x=%d y=%d try=%d\n", uid, pos.PointID, pos.Village, pos.Area, pos.X, pos.Y, try)
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: true, State: robotcap.ActionStateStore}
		}
		if reason == "cancelled" {
			points.DiscardAttemptFailure(uid, &failureState)
			points.Discard(uid, pos)
			points.Flush()
			r.cleanupStoreSession(info, rc, reason)
			return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateCancelled}
		}
		if reason == "" {
			reason = "disjoint_failed"
		}
		robotLogf("[DISJOINT_TRY_FAILED] uid=%d cid=%d try=%d/%d point=%s reason=%s\n",
			uid, info.CID, try, tries, pos.PointID, reason)
		if points.ReportAttemptFailure(uid, &failureState, pos, reason) {
			robotLogf("[DISJOINT_SESSION_POINT_FAILURE] uid=%d cid=%d try=%d/%d point=%s reason=%s\n",
				uid, info.CID, try, tries, pos.PointID, reason)
			break
		}
		if reason == "set_area_failed" {
			setAreaStalls++
			if setAreaStalls >= disjointSetAreaStallLimit {
				robotLogf("[DISJOINT_SET_AREA_STALLED] uid=%d cid=%d try=%d/%d\n", uid, info.CID, try, tries)
				break
			}
		}
		reuseSession = r.manager.disjointReasonRetryable(reason)
		if !reuseSession {
			break
		}
	}
	points.CommitAttemptFailure(uid, &failureState)
	points.Flush()
	r.cleanupStoreSession(info, rc, "disjoint_failed")
	r.manager.addAutoStore(0, 1, 0)
	return robotcap.ActionResult{UID: uid, CID: info.CID, OK: false, State: robotcap.ActionStateStoreFailed}
}

func (r *RobotRuntime) tryDisjointPosition(info robotcap.Info, rc robotconfig.RuntimeConfig, shouldStop func() bool) (bool, string) {
	// A prepared disjointer with a durable machine opens the next store on the
	// live session. The offline profession cycle below only runs when the probe
	// fails, the machine needs its endurance refreshed, or the live area cannot
	// host a point: the next login lets the spawn repair relocate the robot
	// instead of forcing a cross-area transition under load.
	if points := r.manager.storePoints(); points != nil && points.HasArea(info.Village, info.Area) {
		if writer := r.manager.disjointWriter(); writer != nil {
			if ready, err := writer.DisjointProfessionReady(info.CID); err == nil && ready {
				return r.tryDisjointPositionInCurrentSession(info, shouldStop)
			}
		}
	}
	// The first coordinate establishes the account session. After the
	// transactional profession and position writes, NoCache is the reload
	// boundary before CMD 238. Coordinate-only retries stay on this session.
	closed := false
	closeDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(closeDeadline) {
		if r.ForceClose(info.UID) {
			closed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !closed {
		robotLogf("[DISJOINT_LOGOUT_ERROR] uid=%d cid=%d reason=direct_close_timeout\n", info.UID, info.CID)
		return false, "logout_failed"
	}
	r.manager.markSessionLogout(info.UID, time.Now())
	if err := r.manager.invalidateClosedCharacterCache(info.UID); err != nil {
		robotLogf("[DISJOINT_CACHE_INVALIDATION_ERROR] uid=%d cid=%d phase=logout err=%v\n", info.UID, info.CID, err)
		return false, "cache_invalidation_failed"
	}
	cancelled, err := r.manager.waitAccountOffline(info.UID, shouldStop)
	if err != nil {
		robotLogf("[DISJOINT_OFFLINE_ERROR] uid=%d cid=%d err=%v\n", info.UID, info.CID, err)
		return false, "offline_failed"
	}
	if cancelled {
		return false, "cancelled"
	}
	// Disjoint-store persistence is adapter-owned: the profession rows load at
	// character select, so the write must happen between logout and the next
	// login. A backend without this port keeps the capability disabled.
	writer := r.manager.disjointWriter()
	if writer == nil {
		robotLogf("[DISJOINT_PERSISTENCE_UNAVAILABLE] uid=%d cid=%d\n", info.UID, info.CID)
		return false, "profession_failed"
	}
	if err := writer.EnsureDisjointProfession(info.CID); err != nil {
		robotLogf("[DISJOINT_PROFESSION_ERROR] uid=%d cid=%d err=%v\n", info.UID, info.CID, err)
		return false, "profession_failed"
	}
	// Share the scheduler's adaptive login budget: store reconnects must not
	// bypass the breaker or the in-flight cap that protects the server.
	if !r.manager.acquireOnlineAttemptWait(storeOnlineGateTimeout, shouldStop) {
		robotLogf("[DISJOINT_ONLINE_GATE_BUSY] uid=%d cid=%d\n", info.UID, info.CID)
		return false, "online_gate_busy"
	}
	online, err := r.manager.sessionService().Online(robotcap.CommandRequest{UIDs: []int{info.UID}}, true, rc)
	r.manager.ReleaseOnlineAttempt()
	if err != nil || online.Confirmed != 1 {
		robotLogf("[DISJOINT_ONLINE_ERROR] uid=%d confirmed=%d failed=%d err=%v\n", info.UID, online.Confirmed, online.Failed, err)
		return false, "online_failed"
	}
	r.manager.invalidateRuntimeStatusCache()
	return r.tryDisjointPositionInCurrentSession(info, shouldStop)
}

func (r *RobotRuntime) tryDisjointPositionInCurrentSession(info robotcap.Info, shouldStop func() bool) (bool, string) {
	st, ok := r.manager.runtimeStatus(info.UID)
	if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
		return false, "runtime_stopped"
	}
	if points := r.manager.storePoints(); points == nil || !points.HasArea(info.Village, info.Area) {
		return false, "set_area_failed"
	}
	runtime := r.manager.storeSessionRuntime()
	if runtime == nil {
		return false, "set_area_failed"
	}
	if !runtime.SetAreaFrom(info.UID, info.Village, info.Area, info.X, info.Y, st.Village, st.Area) {
		return false, "set_area_failed"
	}
	if storecap.SleepWithStop(1800*time.Millisecond, shouldStop) {
		return false, "cancelled"
	}
	if !runtime.StartDisjointStore(info.UID, r.manager.disjointStoreCost()) {
		return false, "start_failed"
	}
	r.manager.invalidateRuntimeStatusCache()
	robotLogf("[DISJOINT_RETRY_SENT] uid=%d cid=%d from=%d/%d to=%d/%d/%d/%d\n",
		info.UID, info.CID, st.Village, st.Area, info.Village, info.Area, info.X, info.Y)
	return r.waitDisjointPositionResult(info, shouldStop, false)
}

func (r *RobotRuntime) waitDisjointPositionResult(info robotcap.Info, shouldStop func() bool, allowCompatibilitySend bool) (bool, string) {
	deadline := time.Now().Add(20 * time.Second)
	sawRunning := false
	runningSince := time.Time{}
	compatibilitySendTried := !allowCompatibilitySend
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			return false, "cancelled"
		}
		if st, ok := r.manager.runtimeStatus(info.UID); ok {
			if st.DisconnectReason != 0 {
				robotLogf("[DISJOINT_RUNTIME_STOPPED] uid=%d state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d\n",
					info.UID, st.StateName, st.DisconnectReason, st.RobotType, st.DisjointCreateSent, st.DisjointDirectAck, st.DisjointActive, st.LastDisjointError)
				return false, "runtime_stopped"
			}
			if st.StateName != robotcap.RuntimeStateRunning {
				// OnlineDisjoint is intentionally asynchronous, matching the original
				// workflow. init/login before the first StateRun is normal startup,
				// not a failed disjoint attempt.
				if sawRunning || st.DisjointCreateSent || st.RobotType == 3 {
					robotLogf("[DISJOINT_RUNTIME_STOPPED] uid=%d state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d\n",
						info.UID, st.StateName, st.DisconnectReason, st.RobotType, st.DisjointCreateSent, st.DisjointDirectAck, st.DisjointActive, st.LastDisjointError)
					return false, "runtime_stopped"
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			sawRunning = true
			if runningSince.IsZero() {
				runningSince = time.Now()
			}
			if st.RobotType == 3 && st.DisjointActive {
				return true, ""
			}
			if st.LastDisjointError != 0 {
				robotLogf("[DISJOINT_ACK_ERROR] uid=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d pos=%d/%d/%d/%d\n",
					info.UID, st.RobotType, st.DisjointCreateSent, st.DisjointDirectAck, st.DisjointActive, st.LastDisjointError, st.Village, st.Area, st.X, st.Y)
				reason, _ := r.manager.disjointFailure(st.LastDisjointError)
				return false, reason
			}
			if !compatibilitySendTried && !st.DisjointCreateSent && st.RobotType != 3 && time.Since(runningSince) >= 500*time.Millisecond {
				compatibilitySendTried = true
				runtime := r.manager.storeSessionRuntime()
				if runtime != nil && runtime.StartDisjointStore(info.UID, r.manager.disjointStoreCost()) {
					robotLogf("[DISJOINT_LOGIN_FALLBACK_SENT] uid=%d cid=%d pos=%d/%d/%d/%d\n",
						info.UID, info.CID, st.Village, st.Area, st.X, st.Y)
				} else {
					robotLogf("[DISJOINT_LOGIN_FALLBACK_FAILED] uid=%d cid=%d state=%s type=%d sent=%t\n",
						info.UID, info.CID, st.StateName, st.RobotType, st.DisjointCreateSent)
					return false, "start_failed"
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if st, ok := r.manager.runtimeStatus(info.UID); ok {
		robotLogf("[DISJOINT_ACK_TIMEOUT] uid=%d state=%s disconnect=%d type=%d sent=%t direct_ack=%t active=%t last_error=%d pos=%d/%d/%d/%d target=%d/%d/%d/%d\n",
			info.UID, st.StateName, st.DisconnectReason, st.RobotType, st.DisjointCreateSent, st.DisjointDirectAck, st.DisjointActive, st.LastDisjointError,
			st.Village, st.Area, st.X, st.Y, info.Village, info.Area, info.X, info.Y)
	} else {
		robotLogf("[DISJOINT_ACK_TIMEOUT] uid=%d runtime_status=missing target=%d/%d/%d/%d\n", info.UID, info.Village, info.Area, info.X, info.Y)
	}
	return false, "ack_timeout"
}

func (r *RobotRuntime) ExpireStore(uid int) robotcap.ActionResult {
	return r.run(uid, func() robotcap.ActionResult {
		st, ok := r.Status(uid)
		if !ok {
			return robotcap.ActionResult{UID: uid, OK: true, State: robotcap.ActionStateOffline}
		}
		rc := r.Config()
		info := robotcap.Info{UID: uid, CID: st.CID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y, Port: r.manager.cfg.RobotGamePort}
		if robots, err := r.manager.selectRobots(robotcap.CommandRequest{UIDs: []int{uid}}); err == nil && len(robots) > 0 {
			info = robots[0]
			info.Port = r.manager.cfg.RobotGamePort
		}
		recovered := r.cleanupStoreSession(info, rc, "store_expired")
		r.manager.addAutoStore(0, 0, 1)
		return robotcap.ActionResult{UID: uid, CID: st.CID, OK: recovered, State: robotcap.ActionStateStoreExpired}
	})
}

// cleanupStoreSession releases both character and account caches before
// removing temporary stall rows/permissions, then returns the role as a normal
// online robot. This prevents the final server snapshot from restoring stale
// inventory or the private-store entitlement (the visible pack-animal state).
func (r *RobotRuntime) cleanupStoreSession(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) bool {
	if err := r.manager.offlineStoreSession(info.UID); err != nil {
		robotLogf("[STORE_CLEANUP_OFFLINE_ERROR] uid=%d cid=%d reason=%s err=%v\n", info.UID, info.CID, reason, err)
		return false
	}
	r.manager.finishStoreState(info.UID, info.CID, reason)
	_, recovered := r.manager.restoreAutoNormalOnline(info, rc, reason)
	return recovered
}

func (r *RobotRuntime) run(uid int, fn func() robotcap.ActionResult) (result robotcap.ActionResult) {
	lock := r.uidLocks.Acquire(uid)
	defer r.uidLocks.Release(uid, lock)
	defer func() {
		if rec := recover(); rec != nil {
			robotLogf("[RobotRuntime] panic uid=%d err=%v stack=%s\n", uid, rec, debug.Stack())
			result = robotcap.ActionResult{
				UID:     uid,
				OK:      false,
				State:   robotcap.ActionStateFailed,
				Message: "runtime panic",
			}
		}
	}()
	return fn()
}

func (m *RobotManager) currentFollowTarget(rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) (robotaction.FollowTarget, bool) {
	account := strings.TrimSpace(rc.FollowAccount)
	if account == "" || rc.SpawnFixed {
		return robotaction.FollowTarget{}, false
	}

	lookup, ok := m.loadFollowAccount(account)
	if !ok {
		return robotaction.FollowTarget{}, false
	}
	if !lookup.villageOK {
		return robotaction.FollowTarget{}, false
	}
	info := robotcap.Info{Village: lookup.village, Area: rc.SpawnArea, X: m.randBetween(rc.SpawnXMin, rc.SpawnXMax), Y: m.randBetween(rc.SpawnYMin, rc.SpawnYMax), Level: rc.LevelMax}
	robotspawn.ApplyVillageLocation(spawnEnv{manager: m}, &info, info.Village, rc, maps)
	return robotaction.FollowTarget{Village: info.Village, Area: info.Area, X: info.X, Y: info.Y}, true
}

func firstActionResult(uid int, res robotcap.CommandResult, err error) robotcap.ActionResult {
	if err != nil {
		return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateFailed, Message: err.Error()}
	}
	for _, robot := range res.Robots {
		if robot.UID == uid {
			return robot
		}
	}
	return robotcap.ActionResult{UID: uid, OK: false, State: robotcap.ActionStateMissing, Message: "no action result"}
}
