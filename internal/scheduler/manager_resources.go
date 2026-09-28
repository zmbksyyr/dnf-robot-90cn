package scheduler

import (
	"math/rand"
	"net"
	"robot/internal/capability/catalog"
	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
	"strconv"
	"strings"
	"time"
)

func (m *RobotManager) randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	var out int
	_ = m.withRand(func(r *rand.Rand) {
		out = r.Intn(n)
	})
	return out
}

func (m *RobotManager) randBetween(min, max int) int {
	if max < min {
		min, max = max, min
	}
	return min + m.randIntn(max-min+1)
}

func (m *RobotManager) RandIntn(n int) int {
	return m.randIntn(n)
}

func (m *RobotManager) RandBetween(min, max int) int {
	return m.randBetween(min, max)
}

func (m *RobotManager) withRand(fn func(*rand.Rand)) error {
	return m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerRandom, "random_source", func() error {
		fn(m.rand)
		return nil
	})
}

func (m *RobotManager) beginStoreBusy(uid int) bool {
	if uid <= 0 {
		return false
	}
	m.autoMu.Lock()
	if m.autoStoreBusy == nil {
		m.autoStoreBusy = make(map[int]bool)
	}
	if m.autoStoreBusy[uid] {
		m.autoMu.Unlock()
		return false
	}
	m.autoStoreBusy[uid] = true
	m.autoMu.Unlock()
	if batcher := m.positionWrites.Load(); batcher != nil {
		batcher.Discard(uid)
	}
	return true
}

func (m *RobotManager) endStoreBusy(uid int) {
	if uid <= 0 {
		return
	}
	m.autoMu.Lock()
	delete(m.autoStoreBusy, uid)
	m.autoMu.Unlock()
}

// beginAdaptiveStoreType is restored from the first proven disjoint/item
// split. It balances active plus in-flight stalls, so failed or slower branches
// do not permanently skew all subsequent choices toward one type.
func (m *RobotManager) beginAdaptiveStoreType() (disjoint bool, done func()) {
	itemRunning, disjointRunning := m.autoStoreTypeCounts()
	m.autoMu.Lock()
	itemPlanned := itemRunning + m.autoStoreItemPending
	disjointPlanned := disjointRunning + m.autoStoreDisjointPending
	disjoint = disjointPlanned < itemPlanned
	if disjoint {
		m.autoStoreDisjointPending++
	} else {
		m.autoStoreItemPending++
	}
	m.autoMu.Unlock()

	return disjoint, func() {
		m.autoMu.Lock()
		if disjoint {
			if m.autoStoreDisjointPending > 0 {
				m.autoStoreDisjointPending--
			}
		} else if m.autoStoreItemPending > 0 {
			m.autoStoreItemPending--
		}
		m.autoMu.Unlock()
	}
}

func (m *RobotManager) autoStoreTypeCounts() (item, disjoint int) {
	for _, st := range m.runtimeStatusMap() {
		if !robotcap.ActiveRuntimeStatus(st) {
			continue
		}
		switch {
		case st.RobotType == 2 && st.StoreDisplayAck:
			item++
		case st.RobotType == 3 && st.DisjointActive:
			disjoint++
		}
	}
	return item, disjoint
}

func (m *RobotManager) markCleanupPending(uids []int) {
	if len(uids) == 0 {
		return
	}
	now := time.Now()
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerCleanup, "mark_cleanup_pending", func() error {
		if m.cleanupPendingUIDs == nil {
			m.cleanupPendingUIDs = make(map[int]time.Time)
		}
		for _, uid := range uids {
			if uid > 0 {
				m.cleanupPendingUIDs[uid] = now
			}
		}
		return nil
	})
}

func (m *RobotManager) clearCleanupPending(uids []int) {
	if len(uids) == 0 {
		return
	}
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerCleanup, "clear_cleanup_pending", func() error {
		for _, uid := range uids {
			delete(m.cleanupPendingUIDs, uid)
		}
		return nil
	})
}

func (m *RobotManager) cleanupPendingSet() map[int]bool {
	out := map[int]bool{}
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerCleanup, "cleanup_pending_set", func() error {
		out = make(map[int]bool, len(m.cleanupPendingUIDs))
		for uid := range m.cleanupPendingUIDs {
			out[uid] = true
		}
		return nil
	})
	return out
}

func (m *RobotManager) isCleanupPending(uid int) bool {
	if m == nil || uid <= 0 {
		return false
	}
	pending := false
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerCleanup, "is_cleanup_pending", func() error {
		_, pending = m.cleanupPendingUIDs[uid]
		return nil
	})
	return pending
}

// waitCleanupQuiescence closes the gap between requesting actor drains and
// deleting their database rows. A store workflow can finish its cancellation
// path after StopUIDs returns; deleting while it is still restoring normal
// position/cache data recreates orphan rows immediately after cleanup.
func (m *RobotManager) waitCleanupQuiescence(uids []int, timeout time.Duration) {
	if len(uids) == 0 {
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active := make(map[int]bool, len(uids))
		for _, uid := range uids {
			if uid > 0 {
				active[uid] = true
			}
		}
		busy := false
		for _, snap := range m.actorStatusMap() {
			if active[snap.UID] {
				busy = true
				break
			}
		}
		if !busy {
			m.autoMu.Lock()
			for uid := range active {
				if m.autoStoreBusy[uid] {
					busy = true
					break
				}
			}
			m.autoMu.Unlock()
		}
		if !busy {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	robotLogf("[RobotLifecycle] cleanup_quiescence_timeout uids=%d timeout_ms=%d\n", len(uids), timeout.Milliseconds())
}

func (m *RobotManager) loadMapCatalog() []shared.MapCatalogItem {
	if m != nil {
		m.townMapCatalogMu.RLock()
		maps := cloneTownMapCatalog(m.townMapCatalog)
		m.townMapCatalogMu.RUnlock()
		if len(maps) > 0 {
			return maps
		}
	}
	if m.cfg == nil {
		return nil
	}
	return catalog.ViewMaps(layout.New(m.cfg.ConfigDir).PVF)
}

func cloneTownMapCatalog(maps []shared.MapCatalogItem) []shared.MapCatalogItem {
	if len(maps) == 0 {
		return nil
	}
	out := make([]shared.MapCatalogItem, len(maps))
	copy(out, maps)
	for i := range out {
		out[i].Rectangles = append([]shared.MapRectangle(nil), maps[i].Rectangles...)
	}
	return out
}

func (m *RobotManager) robotConnectIP() string {
	if m.cfg != nil && strings.TrimSpace(m.cfg.RobotConnectIP) != "" {
		return strings.TrimSpace(m.cfg.RobotConnectIP)
	}
	if m.cfg != nil {
		return strings.TrimSpace(m.cfg.RobotInnerIP)
	}
	return ""
}

func (m *RobotManager) robotGamePortAddress() string {
	port := 10011
	if m.cfg != nil && m.cfg.RobotGamePort > 0 {
		port = m.cfg.RobotGamePort
	}
	host := strings.TrimSpace(m.robotConnectIP())
	if host == "" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}
