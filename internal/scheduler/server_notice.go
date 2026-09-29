package scheduler

import (
	"fmt"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// serverNoticeAreaWindow paces one village/area independently. Every area with
// robots gets its own announcement stream instead of sharing one global slot.
type serverNoticeAreaWindow struct {
	nextAt      time.Time
	windowAt    time.Time
	windowCount int
}

// serverNoticeKind picks the trigger kind for one notice event. The per-robot
// stock writer provisions whatever kind this chooses.
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

// claimServerNoticeSlot is the per-area pacing gate. Each village/area keeps
// its own minimum gap and hourly cap, so a fleet spread over many towns
// produces one stream per area instead of one stream for the whole server.
// When the optional village/area filter is configured the caller already
// matched it; the window still keys on the robot's own area so the pacing
// stays predictable.
func (m *RobotManager) claimServerNoticeSlot(rc robotconfig.RuntimeConfig, village, area int) bool {
	if m == nil {
		return false
	}
	minGap := rc.ServerNoticeMinGapSec
	maxGap := rc.ServerNoticeMaxGapSec
	if minGap <= 0 {
		minGap = 600
	}
	gap := time.Duration(m.randBetween(minGap, maxGap)) * time.Second
	// The first claim of a fresh area waits a random slice of the gap so a
	// fleet restart does not fire every area in the same second.
	initialDelay := time.Duration(m.randBetween(0, maxGap)) * time.Second
	now := time.Now()
	key := fmt.Sprintf("%d/%d", village, area)
	m.serverNoticeMu.Lock()
	defer m.serverNoticeMu.Unlock()
	if m.serverNoticeAreas == nil {
		m.serverNoticeAreas = make(map[string]*serverNoticeAreaWindow)
	}
	window := m.serverNoticeAreas[key]
	if window == nil {
		window = &serverNoticeAreaWindow{nextAt: now.Add(initialDelay)}
		m.serverNoticeAreas[key] = window
	}
	if !window.nextAt.IsZero() && now.Before(window.nextAt) {
		return false
	}
	if rc.ServerNoticeMaxPerHour > 0 {
		if window.windowAt.IsZero() || now.Sub(window.windowAt) >= time.Hour {
			window.windowAt = now
			window.windowCount = 0
		}
		if window.windowCount >= rc.ServerNoticeMaxPerHour {
			return false
		}
		window.windowCount++
	}
	window.nextAt = now.Add(gap)
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
