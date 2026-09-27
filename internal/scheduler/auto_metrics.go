package scheduler

import (
	"fmt"
	"time"

	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/robotlog"
)

func (m *RobotManager) addAutoCreated(n int) {
	if n <= 0 {
		return
	}
	m.autoMu.Lock()
	m.autoStats.Created += n
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) addAutoOnline(success, failed int) {
	m.autoMu.Lock()
	m.autoStats.OnlineSuccess += success
	m.autoStats.OnlineFailed += failed
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) addAutoMove(success, failed int) {
	m.autoMu.Lock()
	m.autoStats.MoveSuccess += success
	m.autoStats.MoveFailed += failed
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) addAutoShoutChannel(world bool, success, failed int) {
	m.autoMu.Lock()
	if world {
		m.autoStats.ShoutWorldSuccess += success
		m.autoStats.ShoutWorldFailed += failed
	} else {
		m.autoStats.ShoutLocalSuccess += success
		m.autoStats.ShoutLocalFailed += failed
	}
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) addAutoStore(success, failed, expired int) {
	m.autoMu.Lock()
	m.autoStats.StoreSuccess += success
	m.autoStats.StoreFailed += failed
	m.autoStats.StoreExpired += expired
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) updateAutoSnapshot(rc robotconfig.RuntimeConfig, summary robotcap.RuntimeStatusSummary) {
	m.autoMu.Lock()
	m.autoStats.Enabled = m.autoEnabled && rc.AutoActions
	m.autoStats.TargetOnline = rc.AutoTargetOnlineCount
	m.autoStats.Running = summary.Running
	m.autoStats.Connecting = summary.Connecting
	m.autoStats.StoreProbability = rc.AutoStoreProbabilityPercent
	m.autoStats.StoreRunning = summary.Stores
	m.autoStats.StoreItemRunning = summary.ItemStores
	m.autoStats.StoreDisjointRunning = summary.DisjointStores
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) updateAutoActorSnapshot(counts actormodel.LedgerCounts) {
	m.autoMu.Lock()
	m.autoStats.Actors = counts.Auto
	m.autoStats.Leased = counts.Leased
	m.autoStats.Idle = counts.Idle
	m.autoStats.Recycling = counts.Releasing
	m.autoStats.BlockedUIDs = counts.Blocked
	m.autoStats.ActorIdle = counts.StateIdle
	m.autoStats.ActorAssigned = counts.StateAssigned
	m.autoStats.ActorOnline = counts.StateOnline
	m.autoStats.ActorRunning = counts.StateRunning
	m.autoStats.ActorBusy = counts.StateBusy
	m.autoStats.ActorReleasing = counts.StateReleasing
	m.autoStats.UpdatedAt = time.Now()
	m.autoMu.Unlock()
}

func (m *RobotManager) updateAutoBreaker(now time.Time, rc robotconfig.RuntimeConfig, counts actormodel.LedgerCounts, running, connecting int) {
	target := rc.AutoTargetOnlineCount
	if target <= 0 {
		return
	}
	if rc.MaxOnlineRobots > 0 && target > rc.MaxOnlineRobots {
		target = rc.MaxOnlineRobots
	}
	abnormalPct := rc.SchedulerBreakerAbnormalPct
	if abnormalPct <= 0 {
		abnormalPct = 30
	}
	if abnormalPct > 100 {
		abnormalPct = 100
	}
	threshold := (target*abnormalPct + 99) / 100
	readyForBreaker := counts.Auto >= (target*9+9)/10 || counts.Leased >= (target*9+9)/10

	m.autoMu.Lock()
	defer m.autoMu.Unlock()

	stats := m.autoStats
	reason := ""
	if readyForBreaker && connecting >= threshold {
		reason = fmt.Sprintf("connecting_over_%dpct target=%d connecting=%d", abnormalPct, target, connecting)
	}

	if m.autoBreakerLastCheck.IsZero() || now.Sub(m.autoBreakerLastCheck) >= time.Minute {
		failDelta := (stats.OnlineFailed - m.autoBreakerLastOnlineFailed) +
			(stats.MoveFailed - m.autoBreakerLastMoveFailed) +
			(stats.ShoutLocalFailed - m.autoBreakerLastShoutLocalFailed) +
			(stats.ShoutWorldFailed - m.autoBreakerLastShoutWorldFailed) +
			(stats.StoreFailed - m.autoBreakerLastStoreFailed)
		m.autoBreakerLastCheck = now
		m.autoBreakerLastOnlineFailed = stats.OnlineFailed
		m.autoBreakerLastMoveFailed = stats.MoveFailed
		m.autoBreakerLastShoutLocalFailed = stats.ShoutLocalFailed
		m.autoBreakerLastShoutWorldFailed = stats.ShoutWorldFailed
		m.autoBreakerLastStoreFailed = stats.StoreFailed
		if readyForBreaker && failDelta >= threshold {
			reason = fmt.Sprintf("failures_over_%dpct_per_min target=%d failed_delta=%d", abnormalPct, target, failDelta)
		}
	}

	if reason == "" {
		return
	}
	pauseSec := rc.SchedulerBreakerPauseSec
	if pauseSec <= 0 {
		pauseSec = 300
	}
	until := now.Add(time.Duration(pauseSec) * time.Second)
	wasActive := now.Before(m.autoBreakerUntil)
	if until.After(m.autoBreakerUntil) {
		m.autoBreakerUntil = until
	}
	if !wasActive || m.autoBreakerReason != reason {
		m.autoBreakerReason = reason
		robotLogf("[AutoBreaker] pause_until=%s reason=%s running=%d connecting=%d actors=%d leased=%d failed online=%d move=%d local=%d world=%d store=%d\n",
			m.autoBreakerUntil.Format(time.RFC3339), reason, running, connecting, counts.Auto, counts.Leased,
			stats.OnlineFailed, stats.MoveFailed, stats.ShoutLocalFailed, stats.ShoutWorldFailed, stats.StoreFailed)
	}
}

func (m *RobotManager) autoBreakerActive(now time.Time) bool {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return now.Before(m.autoBreakerUntil)
}

func (s *RobotSupervisor) updateMetrics(rc robotconfig.RuntimeConfig, signals adaptiveSchedulerSignals) {
	now := time.Now()
	if !s.nextMetrics.IsZero() && now.Before(s.nextMetrics) {
		return
	}
	s.nextMetrics = now.Add(time.Duration(rc.SchedulerMetricsIntervalSec) * time.Second)
	status := s.manager.runtimeStatusMap()
	uids := make([]int, 0, len(status))
	for uid := range status {
		uids = append(uids, uid)
	}
	alive, err := s.manager.aliveRobotUIDs(uids)
	if err != nil {
		robotLogf("[RobotSupervisor] runtime_alive_filter_failed err=%v\n", err)
		alive = nil
	}
	blocked := s.ledger.BlockedUIDSet()
	var summary robotcap.RuntimeStatusSummary
	for uid, st := range status {
		if _, skip := blocked[uid]; skip {
			continue
		}
		if alive != nil && !alive[uid] {
			continue
		}
		summary.Add(st)
	}
	running, connecting, stores := summary.Running, summary.Connecting, summary.Stores
	s.manager.updateAutoSnapshot(rc, summary)
	counts := s.ledger.Counts(now, rc)
	s.manager.updateAutoActorSnapshot(counts)
	s.manager.updateAutoBreaker(now, rc, counts, running, connecting)
	s.manager.autoMu.Lock()
	stats := s.manager.autoStats
	s.manager.schedulerRecentOnlineSuccess = stats.OnlineSuccess - s.manager.schedulerLastOnlineSuccess
	s.manager.schedulerRecentOnlineFailed = stats.OnlineFailed - s.manager.schedulerLastOnlineFailed
	s.manager.schedulerLastOnlineSuccess = stats.OnlineSuccess
	s.manager.schedulerLastOnlineFailed = stats.OnlineFailed
	policy := s.manager.schedulerStatus
	s.manager.autoMu.Unlock()
	dbStatus := s.manager.DatabaseStatus()
	line := fmt.Sprintf("[RobotMetrics] policy=%s target=%d actors=%d leased=%d idle=%d state idle=%d assigned=%d online=%d running=%d busy=%d releasing=%d runtime running=%d store=%d connecting=%d recycling=%d blocked=%d cpu=%.1f mem_mb=%d goroutines=%d online=%d/%d move=%d/%d shout_local=%d/%d shout_world=%d/%d store=%d/%d expired=%d db_ms=%d db_ok=%t log_mb=%.1f\n",
		policy.Mode,
		rc.AutoTargetOnlineCount, counts.Auto, counts.Leased, counts.Idle,
		counts.StateIdle, counts.StateAssigned, counts.StateOnline, counts.StateRunning, counts.StateBusy, counts.StateReleasing,
		running, stores, connecting, counts.Releasing, counts.Blocked,
		signals.CPUPercent, signals.MemoryMB, signals.Goroutines,
		stats.OnlineSuccess, stats.OnlineFailed,
		stats.MoveSuccess, stats.MoveFailed,
		stats.ShoutLocalSuccess, stats.ShoutLocalFailed,
		stats.ShoutWorldSuccess, stats.ShoutWorldFailed,
		stats.StoreSuccess, stats.StoreFailed, stats.StoreExpired,
		dbStatus.LatencyMS, dbStatus.OK, float64(robotlog.LogSizeBytes())/(1024*1024))
	robotLogf("%s", line)
}

func (s *RobotSupervisor) actorCounts(now time.Time, rc robotconfig.RuntimeConfig) actormodel.LedgerCounts {
	return s.ledger.Counts(now, rc)
}
