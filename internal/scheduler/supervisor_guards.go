package scheduler

import (
	"time"

	"robot/internal/capability/keypair"
	robotconfig "robot/internal/capability/robotconfig"
)

func (s *RobotSupervisor) handleAutoGuards(now time.Time, rc robotconfig.RuntimeConfig, signals adaptiveSchedulerSignals) bool {
	s.manager.autoMu.Lock()
	enabled := s.manager.autoEnabled
	s.manager.autoMu.Unlock()
	if !enabled || !rc.AutoActions {
		end := s.manager.beginActorContainerOp("auto_disabled_converge")
		s.stopAutoActors()
		end()
		s.updateGuardStatus(rc, signals, schedulerPolicyManual, schedulerReasonAutoDisabled)
		s.updateMetrics(rc, signals)
		return true
	}
	if s.manager.NativeKeypairRequired() {
		if st := s.manager.KeypairStatus(); !st.GameValid {
			s.stopAutoActors()
			s.logKeyBlocked(now, rc, st)
			reason := st.Error
			if reason == "" {
				reason = st.KeyReason
			}
			if reason == "" {
				reason = schedulerReasonKeyInvalid
			}
			s.updateGuardStatus(rc, signals, schedulerPolicyMaintenance, schedulerReasonKeyInvalidPrefix+reason)
			s.updateMetrics(rc, signals)
			return true
		}
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

func (s *RobotSupervisor) logKeyBlocked(now time.Time, rc robotconfig.RuntimeConfig, st keypair.KeypairStatus) {
	if !s.nextKeyLog.IsZero() && now.Before(s.nextKeyLog) {
		return
	}
	interval := time.Duration(rc.SchedulerMetricsIntervalSec) * time.Second
	if interval <= 0 {
		interval = 10 * time.Second
	}
	s.nextKeyLog = now.Add(interval)
	reason := st.Error
	if reason == "" {
		reason = st.KeyReason
	}
	robotLogf("[RobotSupervisor] auto_blocked key_state=%s reason=%s\n", st.KeyState, reason)
}
