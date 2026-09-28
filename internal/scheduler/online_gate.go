package scheduler

import (
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

// Online attempt admission lives on the scheduler side. Every login attempt -
// the initial fill and each actor retry - passes through this gate, so the
// adaptive policy owns the attempt rate, the in-flight cap and the breaker
// pause instead of each actor retrying on its own fixed timer.
//
// The gate is intentionally lazy: tokens are refilled from the current
// adaptive runtime config on every acquisition, so a policy change takes
// effect on the next attempt without a separate refill loop.
func (m *RobotManager) TryAcquireOnlineAttempt() bool {
	if m == nil {
		return false
	}
	now := time.Now()
	if m.autoBreakerActive(now) {
		return false
	}
	rc := m.loadRobotConfig()
	rate := robotconfig.OnlineStartRate(rc)
	if rate <= 0 {
		return false
	}
	inFlightCap := rc.SchedulerOnlineInFlight
	if inFlightCap <= 0 {
		inFlightCap = 8
	}

	m.onlineGateMu.Lock()
	defer m.onlineGateMu.Unlock()
	if m.onlineInFlight >= inFlightCap {
		return false
	}
	if m.onlineTokenAt.IsZero() {
		m.onlineTokenAt = now
		m.onlineTokens = 1
	}
	if elapsed := now.Sub(m.onlineTokenAt).Seconds(); elapsed > 0 {
		m.onlineTokens += elapsed * float64(rate)
		m.onlineTokenAt = now
	}
	burst := float64(rate)
	if burst < 1 {
		burst = 1
	}
	if m.onlineTokens > burst {
		m.onlineTokens = burst
	}
	if m.onlineTokens < 1 {
		return false
	}
	m.onlineTokens--
	m.onlineInFlight++
	return true
}

// ReleaseOnlineAttempt frees the in-flight slot taken by TryAcquireOnlineAttempt.
func (m *RobotManager) ReleaseOnlineAttempt() {
	if m == nil {
		return
	}
	m.onlineGateMu.Lock()
	if m.onlineInFlight > 0 {
		m.onlineInFlight--
	}
	m.onlineGateMu.Unlock()
}

func (m *RobotManager) OnlineAttemptInFlight() int {
	if m == nil {
		return 0
	}
	m.onlineGateMu.Lock()
	defer m.onlineGateMu.Unlock()
	return m.onlineInFlight
}
