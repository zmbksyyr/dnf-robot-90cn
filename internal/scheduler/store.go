package scheduler

import (
	"sync"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/layout"
	"robot/internal/foundation/mathx"
)

func (m *RobotManager) storePoints() *storecap.PointCoordinator {
	var points *storecap.PointCoordinator
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStorePoints, "store_points", func() error {
		if m.storePointsCoord == nil {
			var paths layout.Paths
			if m.cfg != nil {
				paths = layout.New(m.cfg.ConfigDir)
			}
			m.storePointsCoord = storecap.NewPointCoordinator(paths.State, paths.PVFMaps(), robotLogf)
		}
		points = m.storePointsCoord
		return nil
	})
	return points
}

func (m *RobotManager) releaseStorePoint(uid int) {
	var points *storecap.PointCoordinator
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStorePoints, "release_store_point", func() error {
		points = m.storePointsCoord
		return nil
	})
	if points != nil {
		points.ReleaseUID(uid)
	}
}

func (m *RobotManager) flushStorePointCache() {
	var points *storecap.PointCoordinator
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStorePoints, "flush_store_points", func() error {
		points = m.storePointsCoord
		return nil
	})
	if points != nil {
		points.Flush()
	}
}

func (m *RobotManager) acquireAutoStoreSlot(rc robotconfig.RuntimeConfig) (func(), bool) {
	limit := normalizedStoreConcurrent(rc)
	acquired := false
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStoreSlots, "acquire_auto_store_slot", func() error {
		if m.autoStoreActive < limit {
			m.autoStoreActive++
			acquired = true
		}
		return nil
	})
	if !acquired {
		return nil, false
	}
	return sync.OnceFunc(m.releaseAutoStoreSlot), true
}

func normalizedStoreConcurrent(rc robotconfig.RuntimeConfig) int {
	limit := rc.SchedulerStoreConcurrent
	if limit <= 0 {
		limit = 30
	}
	return limit
}

func (m *RobotManager) acquireAutoItemStoreSlot(rc robotconfig.RuntimeConfig) (func(), bool) {
	itemLimit := m.effectiveAutoItemStoreLimit(rc)
	itemAcquired := false
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStoreSlots, "acquire_auto_item_store_slot", func() error {
		if m.autoItemStoreActive < itemLimit {
			m.autoItemStoreActive++
			itemAcquired = true
		}
		return nil
	})
	if !itemAcquired {
		return nil, false
	}
	releaseItem := sync.OnceFunc(m.releaseAutoItemStoreSlot)

	releaseShared, ok := m.acquireAutoStoreSlot(rc)
	if !ok {
		releaseItem()
		return nil, false
	}
	return func() {
		releaseShared()
		releaseItem()
	}, true
}

func (m *RobotManager) effectiveAutoItemStoreLimit(rc robotconfig.RuntimeConfig) int {
	configLimit := normalizedStoreConcurrent(rc)
	if configLimit <= 0 {
		return 1
	}
	success := 0
	if points := m.storePoints(); points != nil {
		success = points.SuccessCount()
	}
	limit := mathx.MinInt(configLimit, 8)
	if success >= 20 {
		limit = mathx.MinInt(configLimit, mathx.MaxInt(2, success/3))
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func (m *RobotManager) releaseAutoStoreSlot() {
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStoreSlots, "release_auto_store_slot", func() error {
		if m.autoStoreActive > 0 {
			m.autoStoreActive--
		}
		return nil
	})
}

func (m *RobotManager) releaseAutoItemStoreSlot() {
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerStoreSlots, "release_auto_item_store_slot", func() error {
		if m.autoItemStoreActive > 0 {
			m.autoItemStoreActive--
		}
		return nil
	})
}

func (m *RobotManager) restoreAutoNormalPosition(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) (robotcap.Info, error) {
	normal := info
	err := m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerNormalPosition, "restore_normal_position", func() error {
		var restoreErr error
		normal, restoreErr = m.storeMaintenance().RestoreAutoNormalPosition(info, rc, reason)
		return restoreErr
	})
	return normal, err
}

func (m *RobotManager) restoreAutoNormalOnline(info robotcap.Info, rc robotconfig.RuntimeConfig, reason string) (robotcap.Info, bool) {
	started := time.Now()
	if m.isCleanupPending(info.UID) {
		robotLogf("[AutoStore] uid=%d restore_normal_skipped reason=%s cleanup_pending=1\n", info.UID, reason)
		return info, true
	}
	normal, err := m.restoreAutoNormalPosition(info, rc, reason)
	if err != nil {
		robotLogf("[AutoStore] uid=%d restore_normal_failed reason=%s elapsed_ms=%d err=%v\n",
			normal.UID, reason, time.Since(started).Milliseconds(), err)
		return normal, false
	}
	if err := m.invalidateCharacterCache(normal.UID); err != nil {
		robotLogf("[AutoStore] uid=%d restore_normal_cache_invalidation_failed reason=%s elapsed_ms=%d err=%v\n",
			normal.UID, reason, time.Since(started).Milliseconds(), err)
		return normal, false
	}
	result, err := m.sessionService().Online(robotcap.CommandRequest{UIDs: []int{normal.UID}}, true, rc)
	recovered := err == nil && result.Confirmed == 1
	elapsedMS := time.Since(started).Milliseconds()
	if !recovered {
		robotLogf("[AutoStore] uid=%d restore_normal_online_failed reason=%s confirmed=%d failed=%d elapsed_ms=%d err=%v\n",
			normal.UID, reason, result.Confirmed, result.Failed, elapsedMS, err)
		return normal, false
	}
	robotLogf("[AutoStore] uid=%d restore_normal_online_ok reason=%s elapsed_ms=%d\n", normal.UID, reason, elapsedMS)
	return normal, true
}

func (m *RobotManager) finishStoreState(uid, cid int, reason string) {
	if m == nil || uid <= 0 {
		return
	}
	m.storeMaintenance().FinishStoreState(uid, cid, reason)
	m.releaseStorePoint(uid)
}
