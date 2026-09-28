package scheduler

import (
	"time"

	robotconfig "robot/internal/capability/robotconfig"
)

func (s *RobotSupervisor) handleAutoGuards(now time.Time, rc robotconfig.RuntimeConfig, signals adaptiveSchedulerSignals) bool {
	s.manager.autoMu.Lock()
	enabled := s.manager.autoEnabled
	s.manager.autoMu.Unlock()
	if !enabled || !rc.AutoActions {
		end := s.manager.beginActorContainerOp("auto_disabled_converge")
		s.stopAutoActors(rc)
		end()
		s.updateGuardStatus(rc, signals, schedulerPolicyManual, schedulerReasonAutoDisabled)
		s.updateMetrics(rc, signals)
		return true
	}
	if err := s.manager.CheckGameCommand(); err != nil {
		s.stopAutoActors(rc)
		s.logGameGateBlocked(now, rc, err)
		s.updateGuardStatus(rc, signals, schedulerPolicyMaintenance, schedulerReasonKeyInvalidPrefix+err.Error())
		s.updateMetrics(rc, signals)
		return true
	}
	if op, started, active := s.manager.structuralOperation(); active {
		s.manager.updateSchedulerStatus(rc, signals, schedulerPolicyDecision{Mode: schedulerPolicyMaintenance, Reason: schedulerReasonStructuralPrefix + op})
		s.updateMetrics(rc, signals)
		robotLogf("[RobotSupervisor] paused structural_op=%s started=%s\n", op, started.Format(time.RFC3339))
		return true
	}
	if !s.manager.autoGamePortStable(now, rc) {
		s.stopSomeAutoActors(rc.SchedulerPortDownReleaseBatch, 0)
		s.updateGuardStatus(rc, signals, schedulerPolicyPressure, schedulerReasonGamePortUnstable)
		s.updateMetrics(rc, signals)
		return true
	}
	if s.manager.autoBreakerActive(now) {
		// A breaker pauses new work, but it must not pin capacity above a newly
		// reduced target. Downward convergence removes pressure and cannot create
		// the login/store storm that the breaker is intended to stop.
		target := robotconfig.TargetCapacity(rc)
		if s.ledger.Counts(now, rc).Auto > target {
			s.ensureAutoActorSlots(rc, target)
		}
		s.recycleUnhealthyActors(now, rc)
		s.updateGuardStatus(rc, signals, schedulerPolicyBreaker, schedulerReasonBreakerActive)
		s.updateMetrics(rc, signals)
		return true
	}
	return false
}

func (s *RobotSupervisor) updateGuardStatus(rc robotconfig.RuntimeConfig, signals adaptiveSchedulerSignals, mode schedulerPolicyMode, reason string) {
	s.manager.updateSchedulerStatus(rc, signals, schedulerPolicyDecision{Mode: mode, Reason: reason})
}

func (s *RobotSupervisor) logGameGateBlocked(now time.Time, rc robotconfig.RuntimeConfig, err error) {
	if !s.nextGameGateLog.IsZero() && now.Before(s.nextGameGateLog) {
		return
	}
	interval := time.Duration(rc.SchedulerMetricsIntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	s.nextGameGateLog = now.Add(interval)
	robotLogf("[RobotSupervisor] auto_blocked runtime_gate=%v\n", err)
}
