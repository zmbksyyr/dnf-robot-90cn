package store

import (
	"fmt"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
	"time"
)

type Workflow struct {
	Env WorkflowEnv
}

type WorkflowEnv interface {
	AddAutoStore(success, failed, expired int)
	AcquireAutoStoreSlot(rc robotconfig.RuntimeConfig) (func(), bool)
	BeginStoreBusy(uid int) bool
	Config() robotconfig.RuntimeConfig
	EndStoreBusy(uid int)
	EnsureStoreInventoryAndStall(info robotcap.Info, rc robotconfig.RuntimeConfig) error
	FinishStoreState(uid, cid int, reason string)
	InvalidateCharacterCache(uid int) error
	Logf(format string, args ...interface{})
	Logout(req robotcap.CommandRequest) (robotcap.CommandResult, error)
	MarkStoreStarted(uid int) error
	Online(req robotcap.CommandRequest, confirm bool) (robotcap.CommandResult, error)
	OfflineCharacterForWrite(uid int, shouldStop func() bool) (bool, error)
	PrepareStorePosition(info robotcap.Info) error
	RefreshCharacterForWrite(uid int, shouldStop func() bool) (bool, error)
	ResumeCharacterAfterWrite(uid int, shouldStop func() bool) (bool, error)
	RestoreAutoNormalOnline(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) (robotcap.Info, bool)
	RobotGamePort() int
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
	SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool
	StartPrivateStore(uid int, title string) bool
	StoreTitle(uid int, rc robotconfig.RuntimeConfig) string
	StorePoints() *PointCoordinator
	SyncRobotCharacterVillage(cid int, village int) error
	WaitAccountOffline(uid int, shouldStop func() bool) (bool, error)
	WaitCharacterRunning(uid int, shouldStop func() bool) (bool, error)
}

type AutoAttemptState int

const (
	AutoAttemptFailed AutoAttemptState = iota
	AutoAttemptSuccess
	AutoAttemptBusy
	AutoAttemptCancelled
)

func (w Workflow) Store(req robotcap.CommandRequest) (robotcap.CommandResult, error) {
	env := w.Env
	robots, err := env.SelectRobots(req)
	if err != nil {
		return robotcap.CommandResult{}, err
	}
	rc := env.Config()
	status := env.RuntimeStatusMap()
	result := robotcap.NewCommandResult(len(robots))
	var offline []robotcap.Info
	for _, r := range robots {
		if !env.BeginStoreBusy(r.UID) {
			result.Failed++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateStoreBusy, Message: "store already running for uid"})
			continue
		}
		if st, ok := status[r.UID]; ok && robotcap.ActiveRuntimeStatus(st) {
			logout, err := env.Logout(robotcap.CommandRequest{UIDs: []int{r.UID}})
			if err != nil || logout.Accepted != 1 {
				msg := fmt.Sprintf("logout before store failed: err=%v accepted=%d confirmed=%d", err, logout.Accepted, logout.Confirmed)
				env.Logf("[Store] uid=%d %s\n", r.UID, msg)
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateLogoutFailed, Message: msg})
				env.EndStoreBusy(r.UID)
				continue
			}
		}
		cancelled, err := env.WaitAccountOffline(r.UID, nil)
		if cancelled || err != nil {
			msg := fmt.Sprintf("wait offline before store failed: cancelled=%t err=%v", cancelled, err)
			env.Logf("[Store] uid=%d %s\n", r.UID, msg)
			result.Failed++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateLogoutFailed, Message: msg})
			env.EndStoreBusy(r.UID)
			continue
		}
		// The game server's final character snapshot can overwrite inventory
		// rows. Prepare them only after the account is confirmed offline.
		if err := env.EnsureStoreInventoryAndStall(r, rc); err != nil {
			result.Failed++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateStorePrepareFailed, Message: err.Error()})
			env.EndStoreBusy(r.UID)
			continue
		}
		if err := env.InvalidateCharacterCache(r.UID); err != nil {
			msg := fmt.Sprintf("character cache invalidation: %v", err)
			env.Logf("[Store] uid=%d prepare_cache_invalidation_failed err=%v\n", r.UID, err)
			result.Failed++
			result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateStorePrepareFailed, Message: msg})
			env.EndStoreBusy(r.UID)
			continue
		}
		offline = append(offline, r)
	}
	if len(offline) > 0 {
		online, err := env.Online(robotcap.CommandRequest{UIDs: robotcap.UIDs(offline)}, true)
		if err != nil {
			for _, r := range offline {
				env.EndStoreBusy(r.UID)
			}
			return result, err
		}
		onlineOK := make(map[int]robotcap.ActionResult)
		for _, robot := range online.Robots {
			if robot.OK && robot.State == robotcap.ActionStateRunning {
				onlineOK[robot.UID] = robot
			}
		}
		for _, r := range offline {
			if _, ok := onlineOK[r.UID]; !ok {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateNotOnline, Message: "online before store failed"})
				env.FinishStoreState(r.UID, r.CID, StoreReasonOnlineFailed)
				env.EndStoreBusy(r.UID)
				continue
			}
			title := env.StoreTitle(r.UID, rc)
			if env.StartPrivateStore(r.UID, title) {
				if err := env.MarkStoreStarted(r.UID); err != nil {
					env.Logf("[Store] uid=%d persist_start_failed err=%v\n", r.UID, err)
					_, _ = env.Logout(robotcap.CommandRequest{UIDs: []int{r.UID}})
					env.FinishStoreState(r.UID, r.CID, StoreReasonStartFailed)
					env.EndStoreBusy(r.UID)
					result.Failed++
					result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateStoreStartFailed, Message: fmt.Sprintf("persist store start: %v", err)})
					continue
				}
				result.Accepted++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateAccepted})
			} else {
				result.Failed++
				result.Robots = append(result.Robots, robotcap.ActionResult{UID: r.UID, CID: r.CID, OK: false, State: robotcap.ActionStateStoreStartFailed, Message: "StartPrivateStore failed after online"})
				env.FinishStoreState(r.UID, r.CID, StoreReasonStartFailed)
				env.EndStoreBusy(r.UID)
			}
		}
	}
	deadline := time.Now().Add(time.Duration(rc.StoreConfirmTimeoutSec) * time.Second)
	for time.Now().Before(deadline) {
		allDone := true
		status = env.RuntimeStatusMap()
		for i := range result.Robots {
			if result.Robots[i].OK || result.Robots[i].State != robotcap.ActionStateAccepted {
				continue
			}
			st, ok := status[result.Robots[i].UID]
			if !ok || !st.StoreDisplayAck {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	status = env.RuntimeStatusMap()
	for i := range result.Robots {
		if result.Robots[i].OK || result.Robots[i].State != robotcap.ActionStateAccepted {
			continue
		}
		if st, ok := status[result.Robots[i].UID]; ok && robotcap.ActiveRuntimeStatus(st) && st.StoreDisplayAck {
			result.Robots[i].OK = true
			result.Robots[i].State = robotcap.ActionStateStore
			result.Confirmed++
		} else {
			result.Robots[i].State = robotcap.ActionStateNotConfirmed
			result.Robots[i].Message = "store state not confirmed"
			result.Failed++
			env.FinishStoreState(result.Robots[i].UID, result.Robots[i].CID, StoreReasonNotConfirmed)
		}
		env.EndStoreBusy(result.Robots[i].UID)
	}
	return result, nil
}

func (w Workflow) AutoUntilSuccess(st robotcap.RuntimeStatus, rc robotconfig.RuntimeConfig, shouldStop func() bool) AutoAttemptState {
	env := w.Env
	tries := rc.AutoStoreMaxPositionTries
	if tries <= 0 {
		tries = 10
	}
	info := robotcap.Info{UID: st.UID, CID: st.CID, Village: st.Village, Area: st.Area, X: st.X, Y: st.Y, Port: env.RobotGamePort()}
	if robots, err := env.SelectRobots(robotcap.CommandRequest{UIDs: []int{st.UID}}); err == nil && len(robots) > 0 {
		info.CID = robots[0].CID
		info.Level = robots[0].Level
		info.Job = robots[0].Job
		info.Grow = robots[0].Grow
		info.GuildID = robots[0].GuildID
	}
	if !env.BeginStoreBusy(info.UID) {
		return AutoAttemptBusy
	}
	releaseSlot, ok := env.AcquireAutoStoreSlot(rc)
	if !ok {
		env.EndStoreBusy(info.UID)
		return AutoAttemptBusy
	}
	defer func() {
		releaseSlot()
		env.EndStoreBusy(info.UID)
	}()
	points := env.StorePoints()
	finalReason := StoreReasonFailed
	attempts := 0
	allowedPosition := func(pos Position) bool { return shared.GenericAreaAllowed(info.GuildID, pos.Village) }
	firstPos, ok := points.ClaimForItemStoreWhere(info.UID, rc.AutoStoreDurationSec, allowedPosition)
	if !ok {
		points.Flush()
		return AutoAttemptBusy
	}
	info.Village, info.Area, info.X, info.Y = firstPos.Village, firstPos.Area, firstPos.X, firstPos.Y
	if reason := w.prepareAutoSession(info, rc, shouldStop); reason != "" {
		if reason == StoreReasonCancelled {
			points.Discard(info.UID, firstPos)
			points.Flush()
			return AutoAttemptCancelled
		}
		points.Report(info.UID, firstPos, false, reason)
		points.Flush()
		env.AddAutoStore(0, 1, 0)
		return AutoAttemptFailed
	}
	pos := firstPos
	var failureState AttemptFailureState
	for try := 1; try <= tries; try++ {
		attempts = try
		if shouldStop != nil && shouldStop() {
			env.Logf("[AutoStore] uid=%d cancelled_before_try=%d\n", info.UID, try)
			points.DiscardAttemptFailure(info.UID, &failureState)
			points.Discard(info.UID, pos)
			finalReason = StoreReasonCancelled
			break
		}
		if try > 1 {
			pos, ok = points.ClaimForItemStoreWhere(info.UID, rc.AutoStoreDurationSec, allowedPosition)
			if !ok {
				env.Logf("[AutoStore] uid=%d no_store_point try=%d/%d\n", info.UID, try, tries)
				break
			}
		}
		info.Village, info.Area, info.X, info.Y = pos.Village, pos.Area, pos.X, pos.Y
		if ok, reason := w.tryPosition(info, rc, try, pos.Source == PointSourceProbe, shouldStop); ok {
			points.CommitAttemptFailure(info.UID, &failureState)
			points.Report(info.UID, pos, true, StoreReasonAck)
			env.Logf("[StoreSuccessPoint] uid=%d point=%s village=%d area=%d x=%d y=%d try=%d source=%s\n", info.UID, pos.PointID, info.Village, info.Area, info.X, info.Y, try, pos.Source)
			env.AddAutoStore(1, 0, 0)
			return AutoAttemptSuccess
		} else if reason != "" {
			if reason == StoreReasonCancelled {
				points.DiscardAttemptFailure(info.UID, &failureState)
				points.Discard(info.UID, pos)
				points.Flush()
				finalReason = reason
				break
			}
			finalReason = reason
			if points.ReportAttemptFailure(info.UID, &failureState, pos, reason) {
				env.Logf("[AutoStore] uid=%d session_point_failure reason=%s try=%d/%d point=%s\n", info.UID, reason, try, tries, pos.PointID)
				break
			}
			if !RetryStoreReasonWithNewPoint(reason) {
				finalReason = reason
				env.Logf("[AutoStore] uid=%d hard_fail reason=%s try=%d/%d\n", info.UID, reason, try, tries)
				break
			}
			continue
		}
		points.ReportAttemptFailure(info.UID, &failureState, pos, StoreReasonFailed)
	}
	points.CommitAttemptFailure(info.UID, &failureState)
	points.Flush()
	env.Logf("[AutoStore] uid=%d failed_after=%d reason=%s\n", info.UID, attempts, finalReason)
	if finalReason != StoreReasonCancelled {
		env.AddAutoStore(0, 1, 0)
	}
	if _, err := env.OfflineCharacterForWrite(info.UID, nil); err != nil {
		env.Logf("[AutoStore] uid=%d cleanup_offline_failed reason=%s err=%v\n", info.UID, finalReason, err)
		return AutoAttemptFailed
	}
	w.finishAndRestoreAutoNormal(info, rc, finalReason)
	if finalReason == StoreReasonCancelled {
		return AutoAttemptCancelled
	}
	return AutoAttemptFailed
}

func (w Workflow) prepareAutoSession(info robotcap.Info, rc robotconfig.RuntimeConfig, shouldStop func() bool) string {
	env := w.Env
	if shouldStop != nil && shouldStop() {
		return StoreReasonCancelled
	}
	cancelled, err := env.OfflineCharacterForWrite(info.UID, shouldStop)
	if err != nil {
		env.Logf("[AutoStore] uid=%d offline_for_write_failed err=%v\n", info.UID, err)
		return StoreReasonPrepareFailed
	}
	var prepareErr error
	if !cancelled {
		prepareErr = w.prepareAutoStoreWrite(info, rc)
	}
	if prepareErr != nil {
		env.Logf("[AutoStore] uid=%d prepare_failed err=%v\n", info.UID, prepareErr)
		w.finishAndRestoreAutoNormal(info, rc, StoreReasonPrepareFailed)
		return StoreReasonPrepareFailed
	}
	if cancelled || (shouldStop != nil && shouldStop()) {
		w.finishAndRestoreAutoNormal(info, rc, StoreReasonCancelled)
		return StoreReasonCancelled
	}
	online, err := env.Online(robotcap.CommandRequest{UIDs: []int{info.UID}}, true)
	if err != nil || online.Confirmed != 1 {
		env.Logf("[AutoStore] uid=%d full_resume_failed confirmed=%d err=%v\n", info.UID, online.Confirmed, err)
		if _, offlineErr := env.OfflineCharacterForWrite(info.UID, nil); offlineErr != nil {
			env.Logf("[AutoStore] uid=%d full_resume_cleanup_offline_failed err=%v\n", info.UID, offlineErr)
			return StoreReasonOnlineAttemptFailed
		}
		w.finishAndRestoreAutoNormal(info, rc, StoreReasonOnlineFailed)
		return StoreReasonOnlineAttemptFailed
	}
	cancelled, err = env.WaitCharacterRunning(info.UID, shouldStop)
	if err != nil {
		env.Logf("[AutoStore] uid=%d full_resume_running_failed err=%v\n", info.UID, err)
		if _, offlineErr := env.OfflineCharacterForWrite(info.UID, nil); offlineErr != nil {
			env.Logf("[AutoStore] uid=%d full_resume_running_cleanup_offline_failed err=%v\n", info.UID, offlineErr)
			return StoreReasonOnlineAttemptFailed
		}
		w.finishAndRestoreAutoNormal(info, rc, StoreReasonOnlineFailed)
		return StoreReasonOnlineAttemptFailed
	}
	if cancelled {
		if _, err := env.OfflineCharacterForWrite(info.UID, nil); err != nil {
			env.Logf("[AutoStore] uid=%d cancel_cleanup_offline_failed err=%v\n", info.UID, err)
			return StoreReasonOnlineAttemptFailed
		}
		w.finishAndRestoreAutoNormal(info, rc, StoreReasonCancelled)
		return StoreReasonCancelled
	}
	return ""
}

func (w Workflow) finishAndRestoreAutoNormal(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) bool {
	env := w.Env
	env.FinishStoreState(info.UID, info.CID, reason)
	_, recovered := env.RestoreAutoNormalOnline(info, rc, reason)
	if !recovered {
		env.Logf("[AutoStore] uid=%d restore_normal_online_failed reason=%s\n", info.UID, reason)
	}
	return recovered
}

func (w Workflow) tryPosition(info robotcap.Info, rc robotconfig.RuntimeConfig, try int, forceCoordinate bool, shouldStop func() bool) (bool, string) {
	env := w.Env
	if shouldStop != nil && shouldStop() {
		return false, StoreReasonCancelled
	}
	stAfterOnline, stOK := env.RuntimeStatusMap()[info.UID]
	if stOK && !storePositionRequiresAreaSet(stAfterOnline, info, forceCoordinate) {
		return w.startAndWaitDisplay(info, rc, try, shouldStop)
	}
	if stOK {
		if points := env.StorePoints(); points == nil || !points.HasArea(info.Village, info.Area) {
			env.Logf("[AutoStore] uid=%d set_area_skipped_unsafe try=%d from=%d/%d to=%d/%d\n", info.UID, try, stAfterOnline.Village, stAfterOnline.Area, info.Village, info.Area)
			return false, StoreReasonSetAreaFailed
		}
		if forceCoordinate && stAfterOnline.Village == info.Village && stAfterOnline.Area == info.Area {
			env.Logf("[StoreProbe] relocate uid=%d village=%d area=%d from=%d/%d to=%d/%d\n", info.UID, info.Village, info.Area, stAfterOnline.X, stAfterOnline.Y, info.X, info.Y)
		}
		areaSet := env.SetAreaFrom(info.UID, info.Village, info.Area, info.X, info.Y, stAfterOnline.Village, stAfterOnline.Area)
		if !areaSet {
			return false, StoreReasonSetAreaFailed
		}
		if SleepWithStop(1800*time.Millisecond, shouldStop) {
			return false, StoreReasonCancelled
		}
	}
	return w.startAndWaitDisplay(info, rc, try, shouldStop)
}

func storePositionRequiresAreaSet(current robotcap.RuntimeStatus, target robotcap.Info, forceCoordinate bool) bool {
	if current.Village != target.Village || current.Area != target.Area {
		return true
	}
	return forceCoordinate && (current.X != target.X || current.Y != target.Y)
}

func (w Workflow) startAndWaitDisplay(info robotcap.Info, rc robotconfig.RuntimeConfig, try int, shouldStop func() bool) (bool, string) {
	env := w.Env
	title := env.StoreTitle(info.UID, rc)
	for inventoryAttempt := 0; inventoryAttempt < 2; inventoryAttempt++ {
		if !env.StartPrivateStore(info.UID, title) {
			env.Logf("[AutoStore] uid=%d store_start_failed try=%d inventory_attempt=%d\n", info.UID, try, inventoryAttempt+1)
			return false, StoreReasonStartFailed
		}
		if ok, reason := w.waitDisplay(info.UID, rc, shouldStop); ok {
			return true, ""
		} else if reason != StoreReasonInventoryNotReady || inventoryAttempt > 0 {
			return false, reason
		}

		// CMD 88 already succeeded, so account-level store permission is loaded.
		// If CMD 13 still exposes an old character snapshot, refresh only the
		// character through the character-select screen. Another hard disconnect can
		// re-enter df_game's reconnect cache and reproduce the same stale inventory.
		env.Logf("[AutoStore] uid=%d inventory_refresh mode=character_select try=%d attempt=%d\n", info.UID, try, inventoryAttempt+1)
		if reason := w.refreshAutoInventorySession(info, rc, shouldStop); reason != "" {
			return false, reason
		}
	}
	return false, StoreReasonInventoryNotReady
}

func (w Workflow) refreshAutoInventorySession(info robotcap.Info, rc robotconfig.RuntimeConfig, shouldStop func() bool) string {
	env := w.Env
	cancelled, err := env.RefreshCharacterForWrite(info.UID, shouldStop)
	if err != nil {
		env.Logf("[AutoStore] uid=%d inventory_select_failed err=%v\n", info.UID, err)
		return StoreReasonPrepareFailed
	}
	refreshCancelled := cancelled || (shouldStop != nil && shouldStop())
	var prepareErr error
	if !refreshCancelled {
		prepareErr = w.prepareAutoStoreWrite(info, rc)
	}
	resumeCancelled, resumeErr := env.ResumeCharacterAfterWrite(info.UID, shouldStop)
	if resumeErr != nil {
		env.Logf("[AutoStore] uid=%d inventory_reselect_failed err=%v\n", info.UID, resumeErr)
		return StoreReasonOnlineAttemptFailed
	}
	if prepareErr != nil {
		env.Logf("[AutoStore] uid=%d inventory_refresh_prepare_failed err=%v\n", info.UID, prepareErr)
		return StoreReasonPrepareFailed
	}
	if refreshCancelled || resumeCancelled {
		return StoreReasonCancelled
	}
	return ""
}

func (w Workflow) prepareAutoStoreWrite(info robotcap.Info, rc robotconfig.RuntimeConfig) error {
	env := w.Env
	if err := env.PrepareStorePosition(info); err != nil {
		return fmt.Errorf("dummy update: %w", err)
	}
	if err := env.SyncRobotCharacterVillage(info.CID, info.Village); err != nil {
		return fmt.Errorf("character village sync: %w", err)
	}
	if err := env.EnsureStoreInventoryAndStall(info, rc); err != nil {
		return fmt.Errorf("inventory and stall: %w", err)
	}
	if err := env.InvalidateCharacterCache(info.UID); err != nil {
		return fmt.Errorf("character cache invalidation: %w", err)
	}
	return nil
}

func (w Workflow) waitDisplay(uid int, rc robotconfig.RuntimeConfig, shouldStop func() bool) (bool, string) {
	env := w.Env
	timeout := time.Duration(rc.StoreConfirmTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			return false, StoreReasonCancelled
		}
		st, ok := env.RuntimeStatusMap()[uid]
		if !ok || st.StateName != robotcap.RuntimeStateRunning || st.DisconnectReason != 0 {
			return false, StoreReasonRuntimeStopped
		}
		if st.StoreDisplayAck {
			return true, ""
		}
		if st.StoreDisplayRejected {
			if !st.StoreDisplaySent && st.LastStoreError == 0 {
				return false, StoreReasonInventoryNotReady
			}
			return false, StoreErrReason(st.LastStoreError)
		}
		if st.StoreCreateRejected && !st.StoreCreated {
			return false, StoreErrReason(st.LastStoreError)
		}
		if SleepWithStop(200*time.Millisecond, shouldStop) {
			return false, StoreReasonCancelled
		}
	}
	return false, StoreReasonDisplayWaitFailed
}

func StoreErrReason(err byte) string {
	// CreatePrivateStore error classification, verified from
	// server-side branches rather than inferred from robot success rates:
	//   0x38: village object registration failed. Usually a point collision or an
	//         invalid object position, so changing coordinates is appropriate.
	//   0x3e: generic "store creation is not allowed here/now". The server reuses
	//         it for busy state, forbidden village/channel, gate/entrance area and
	//         village/area mismatch; only some branches are position-related.
	//   0x3f: IsPermissionPrivateStore() disagrees with the requested doll slot.
	//         Robot uses 0xffff (no doll), which requires the account-level store
	//         entitlement to have been loaded by a full account login.
	//   0x52: coordinates are inside a configured restrictive commercial zone;
	//         this is the definite position error (often NPC/entrance space).
	//   0x72: account/trading security protection rejected the operation; it is
	//         not a map-position error and changing coordinates cannot fix it.
	//   0x11: store item/inventory verification failed after creation; it is not
	//         a map-position error.
	if err == 0 {
		return StoreReasonFailed
	}
	if err == 0x11 {
		return StoreReasonErr011
	}
	return fmt.Sprintf("store_err_0x%02x", err)
}

func RetryStoreReasonWithNewPoint(reason string) bool {
	// 0x38 and 0x52 are position failures. 0x3e remains retryable because the
	// server also uses it for gate/entrance/area restrictions, although it is not
	// exclusively positional. 0x11 and 0x72 must not be treated as "try a point".
	switch reason {
	case "store_err_0x38", "store_err_0x3e", StoreReasonErr052:
		return true
	default:
		return false
	}
}

func SleepWithStop(d time.Duration, shouldStop func() bool) bool {
	if d <= 0 {
		return shouldStop != nil && shouldStop()
	}
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			return true
		}
		remaining := time.Until(deadline)
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		time.Sleep(remaining)
	}
	return shouldStop != nil && shouldStop()
}
