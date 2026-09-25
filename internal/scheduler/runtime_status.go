package scheduler

import (
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/lockhub"
)

const runtimeStatusCacheTTL = 2000 * time.Millisecond

type runtimeStatusMapProvider interface {
	RuntimeStatusMap() map[int]robotcap.RuntimeStatus
}

// runtimeStateTable is the scheduler's only cached runtime-state source.
// Adapter transports publish raw protocol state; every scheduler, Web and API
// projection reads an immutable snapshot from this table.
type runtimeStateTable struct {
	mu        lockhub.RWLocker
	snapshot  map[int]robotcap.RuntimeStatus
	updatedAt time.Time
	summary   robotcap.RuntimeStatusSummary
	summaryAt time.Time
	refresh   chan struct{}
}

// runtimeStatusMap returns an immutable snapshot. Callers that need to delete
// or replace entries must use runtimeStatusMapCopy.
func (m *RobotManager) runtimeStatusMap() map[int]robotcap.RuntimeStatus {
	for {
		now := time.Now()
		m.runtimeState.mu.RLock()
		snapshot := m.runtimeState.snapshot
		cacheAt := m.runtimeState.updatedAt
		refreshDone := m.runtimeState.refresh
		m.runtimeState.mu.RUnlock()
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
		m.runtimeState.mu.Lock()
		now = time.Now()
		if m.runtimeState.snapshot != nil && !m.runtimeState.updatedAt.IsZero() && now.Sub(m.runtimeState.updatedAt) <= runtimeStatusCacheTTL {
			snapshot = m.runtimeState.snapshot
		} else if m.runtimeState.refresh != nil {
			refreshDone = m.runtimeState.refresh
		} else {
			refreshDone = make(chan struct{})
			m.runtimeState.refresh = refreshDone
			refresh = true
		}
		m.runtimeState.mu.Unlock()
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
		m.runtimeState.mu.Lock()
		if complete {
			cacheAt := time.Now()
			m.runtimeState.snapshot = status
			m.runtimeState.updatedAt = cacheAt
			m.runtimeState.summary = summary
			m.runtimeState.summaryAt = cacheAt
		}
		if m.runtimeState.refresh == refreshDone {
			m.runtimeState.refresh = nil
			close(refreshDone)
		}
		m.runtimeState.mu.Unlock()
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
	m.runtimeState.mu.RLock()
	if m.runtimeState.summaryAt.Equal(m.runtimeState.updatedAt) {
		summary := m.runtimeState.summary
		m.runtimeState.mu.RUnlock()
		return summary
	}
	cacheAt := m.runtimeState.updatedAt
	m.runtimeState.mu.RUnlock()

	summary := robotcap.SummarizeRuntimeStatusMap(status)
	m.runtimeState.mu.Lock()
	if m.runtimeState.updatedAt.Equal(cacheAt) {
		m.runtimeState.summary = summary
		m.runtimeState.summaryAt = cacheAt
	}
	m.runtimeState.mu.Unlock()
	return summary
}

func (m *RobotManager) runtimeStatusMapCopy() map[int]robotcap.RuntimeStatus {
	return robotcap.CopyRuntimeStatusMap(m.runtimeStatusMap())
}

func (m *RobotManager) runtimeStatusMapFresh() map[int]robotcap.RuntimeStatus {
	return m.loadRuntimeStatusMap()
}

func (m *RobotManager) loadRuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	if provider, ok := m.actions.(runtimeStatusMapProvider); ok {
		status := provider.RuntimeStatusMap()
		if status == nil {
			return make(map[int]robotcap.RuntimeStatus)
		}
		return robotcap.CopyRuntimeStatusMap(status)
	}
	return make(map[int]robotcap.RuntimeStatus)
}

func (m *RobotManager) invalidateRuntimeStatusCache() {
	m.runtimeState.mu.Lock()
	m.runtimeState.updatedAt = time.Time{}
	m.runtimeState.summaryAt = time.Time{}
	m.runtimeState.mu.Unlock()
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
