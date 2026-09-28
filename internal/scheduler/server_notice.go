package scheduler

import (
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// serverNoticeKind picks the trigger kind for one fleet-wide event. The
// per-robot stock writer provisions whatever kind this chooses.
func (m *RobotManager) serverNoticeKind(rc robotconfig.RuntimeConfig) shared.ServerNoticeKind {
	if rc.ServerNoticeLotteryPercent <= 0 {
		return shared.ServerNoticeUpgrade
	}
	if rc.ServerNoticeLotteryPercent >= 100 || m == nil {
		return shared.ServerNoticeLottery
	}
	if m.randIntn(100) < rc.ServerNoticeLotteryPercent {
		return shared.ServerNoticeLottery
	}
	return shared.ServerNoticeUpgrade
}

// claimServerNoticeSlot is the fleet-wide pacing gate. Every actor asks before
// running its due notice action; only one claim succeeds per gap, and the
// hourly cap bounds bursts even when many actors become due together.
func (m *RobotManager) claimServerNoticeSlot(rc robotconfig.RuntimeConfig) bool {
	if m == nil {
		return false
	}
	minGap := rc.ServerNoticeMinGapSec
	maxGap := rc.ServerNoticeMaxGapSec
	if minGap <= 0 {
		minGap = 600
	}
	gap := time.Duration(m.randBetween(minGap, maxGap)) * time.Second
	now := time.Now()
	m.serverNoticeMu.Lock()
	defer m.serverNoticeMu.Unlock()
	if !m.serverNoticeNextAt.IsZero() && now.Before(m.serverNoticeNextAt) {
		return false
	}
	if rc.ServerNoticeMaxPerHour > 0 {
		if m.serverNoticeWindowAt.IsZero() || now.Sub(m.serverNoticeWindowAt) >= time.Hour {
			m.serverNoticeWindowAt = now
			m.serverNoticeWindowCount = 0
		}
		if m.serverNoticeWindowCount >= rc.ServerNoticeMaxPerHour {
			return false
		}
		m.serverNoticeWindowCount++
	}
	m.serverNoticeNextAt = now.Add(gap)
	return true
}

func (m *RobotManager) serverNoticeRuntime() shared.ServerNoticer {
	if m == nil {
		return nil
	}
	return m.serverNoticeTrigger
}

func (m *RobotManager) serverNoticeStockWriter() shared.ServerNoticeStockWriter {
	if m == nil {
		return nil
	}
	return m.serverNoticeStock
}

// addServerNotice records one trigger outcome: sent/accepted without a
// broadcast, broadcast notices, and failures stay separate so operators can
// see the probabilistic lottery pool.
func (m *RobotManager) addServerNotice(sent, failed, broadcast int) {
	m.autoMu.Lock()
	m.autoStats.ServerNoticeSent += sent
	m.autoStats.ServerNoticeFailed += failed
	m.autoStats.ServerNoticeBroadcast += broadcast
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

// serverNoticeHistoryPort is implemented by adapters that keep the observed
// 0x0056 broadcast history.
type serverNoticeHistoryPort interface {
	RecentServerNotices() []shared.ServerNoticeEvent
}

// RecentServerNotices returns the adapter's deduplicated broadcast history.
func (m *RobotManager) RecentServerNotices() []shared.ServerNoticeEvent {
	if m == nil || m.serverNoticeTrigger == nil {
		return nil
	}
	if history, ok := m.serverNoticeTrigger.(serverNoticeHistoryPort); ok {
		return history.RecentServerNotices()
	}
	return nil
}
