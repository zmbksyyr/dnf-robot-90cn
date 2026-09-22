package scheduler

import (
	"time"

	robotcap "robot/internal/capability/robot"
)

const runtimeStatusCacheTTL = 2000 * time.Millisecond

type runtimeStatusMapProvider interface {
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
}

// runtimeStatusMap returns an immutable snapshot. Callers that need to delete
// or replace entries must use runtimeStatusMapCopy.
func (m *RobotManager) runtimeStatusMap() map[int]robotcap.RuntimeStatus {
	for {
		now := time.Now()
		m.runtimeStatusMu.RLock()
		snapshot := m.runtimeStatusCache
		cacheAt := m.runtimeStatusCacheAt
		refreshDone := m.runtimeStatusRefresh
		m.runtimeStatusMu.RUnlock()
		if snapshot != nil && !cacheAt.IsZero() && now.Sub(cacheAt) <= runtimeStatusCacheTTL {
			return snapshot
		}
		if refreshDone != nil {
			<-refreshDone
			continue
		}

		snapshot = nil
		refreshDone = nil
		refresh := false
		m.runtimeStatusMu.Lock()
		now = time.Now()
		if m.runtimeStatusCache != nil && !m.runtimeStatusCacheAt.IsZero() && now.Sub(m.runtimeStatusCacheAt) <= runtimeStatusCacheTTL {
			snapshot = m.runtimeStatusCache
		} else if m.runtimeStatusRefresh != nil {
			refreshDone = m.runtimeStatusRefresh
		} else {
			refreshDone = make(chan struct{})
			m.runtimeStatusRefresh = refreshDone
			refresh = true
		}
		m.runtimeStatusMu.Unlock()
		if snapshot != nil {
			return snapshot
		}
		if !refresh {
			<-refreshDone
			continue
		}
		return m.refreshRuntimeStatusMap(refreshDone)
	}
}

func (m *RobotManager) refreshRuntimeStatusMap(refreshDone chan struct{}) (status map[int]robotcap.RuntimeStatus) {
	complete := false
	summary := robotcap.RuntimeStatusSummary{}
	defer func() {
		m.runtimeStatusMu.Lock()
		if complete {
			cacheAt := time.Now()
			m.runtimeStatusCache = status
			m.runtimeStatusCacheAt = cacheAt
			m.runtimeStatusSummary = summary
			m.runtimeStatusSummaryAt = cacheAt
		}
		if m.runtimeStatusRefresh == refreshDone {
			m.runtimeStatusRefresh = nil
			close(refreshDone)
		}
		m.runtimeStatusMu.Unlock()
	}()

	status = m.loadRuntimeStatusMap()
	for _, st := range status {
		summary.Add(st)
	}
	complete = true
	return status
}

func (m *RobotManager) runtimeStatusSummarySnapshot() robotcap.RuntimeStatusSummary {
	status := m.runtimeStatusMap()
	m.runtimeStatusMu.RLock()
	if m.runtimeStatusSummaryAt.Equal(m.runtimeStatusCacheAt) {
		summary := m.runtimeStatusSummary
		m.runtimeStatusMu.RUnlock()
		return summary
	}
	cacheAt := m.runtimeStatusCacheAt
	m.runtimeStatusMu.RUnlock()

	summary := robotcap.SummarizeRuntimeStatusMap(status)
	m.runtimeStatusMu.Lock()
	if m.runtimeStatusCacheAt.Equal(cacheAt) {
		m.runtimeStatusSummary = summary
		m.runtimeStatusSummaryAt = cacheAt
	}
	m.runtimeStatusMu.Unlock()
	return summary
}

func (m *RobotManager) runtimeStatusMapCopy() map[int]robotcap.RuntimeStatus {
	return robotcap.CopyRuntimeStatusMap(m.runtimeStatusMap())
}

func (m *RobotManager) runtimeStatusMapFresh() map[int]robotcap.RuntimeStatus {
	return m.loadRuntimeStatusMap()
}

func (m *RobotManager) loadRuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	if provider, ok := m.backendActions.(runtimeStatusMapProvider); ok {
		status := provider.RuntimeStatusMap()
		if status == nil {
			return make(map[int]robotcap.RuntimeStatus)
		}
		return status
	}
	if provider, ok := m.doll.(runtimeStatusMapProvider); ok {
		status := provider.RuntimeStatusMap()
		if status == nil {
			return make(map[int]robotcap.RuntimeStatus)
		}
		return status
	}
	status := make(map[int]robotcap.RuntimeStatus)
	for _, st := range m.doll.RuntimeStatus() {
		status[st.UID] = st
	}
	return status
}

func (m *RobotManager) invalidateRuntimeStatusCache() {
	m.runtimeStatusMu.Lock()
	m.runtimeStatusCacheAt = time.Time{}
	m.runtimeStatusSummaryAt = time.Time{}
	m.runtimeStatusMu.Unlock()
}

func (m *RobotManager) runtimeStatus(uid int) (robotcap.RuntimeStatus, bool) {
	if uid <= 0 {
		return robotcap.RuntimeStatus{}, false
	}
	status, ok := m.runtimeStatusMap()[uid]
	return status, ok
}

func (m *RobotManager) countRuntimeRunning() int {
	n := 0
	for _, st := range m.runtimeStatusMap() {
		if robotcap.ActiveRuntimeStatus(st) {
			n++
		}
	}
	return n
}
