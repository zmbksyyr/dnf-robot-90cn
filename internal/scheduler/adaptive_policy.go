package scheduler

import (
	"fmt"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/process"
	"robot/internal/shared"
)

type schedulerPolicyMode string

const (
	schedulerPolicyBootstrap   schedulerPolicyMode = robotcap.SchedulerModeBootstrap
	schedulerPolicyFill        schedulerPolicyMode = robotcap.SchedulerModeFill
	schedulerPolicyStable      schedulerPolicyMode = robotcap.SchedulerModeStable
	schedulerPolicyStore       schedulerPolicyMode = robotcap.SchedulerModeStore
	schedulerPolicyPressure    schedulerPolicyMode = robotcap.SchedulerModePressure
	schedulerPolicyBreaker     schedulerPolicyMode = robotcap.SchedulerModeBreaker
	schedulerPolicyMaintenance schedulerPolicyMode = robotcap.SchedulerModeMaintenance
	schedulerPolicyManual      schedulerPolicyMode = robotcap.SchedulerModeManual
)

const adaptiveActiveStoreCapacity = 108

const (
	schedulerReasonAutoDisabled     = "auto_disabled"
	schedulerReasonGamePortUnstable = "game_port_unstable"
	schedulerReasonBreakerActive    = "breaker_active"
	schedulerReasonTargetZero       = "target_zero"
	schedulerReasonNoLiveSnapshot   = "no_live_snapshot"
	schedulerReasonKeyInvalid       = "key_invalid"
	schedulerReasonKeyInvalidPrefix = "key_invalid="
	schedulerReasonStructuralPrefix = "structural_op="
	schedulerReasonActorPrefix      = "actor_container="
	schedulerReasonPendingBacklog   = "pending_backlog"
)

type adaptiveSchedulerSignals struct {
	Live             bool
	Running          int
	Connecting       int
	StoreRunning     int
	StoreUnsupported bool
	Actors           int
	Idle             int
	ActorIdle        int
	ActorAssigned    int
	ActorOnline      int
	ActorRunning     int
	ActorBusy        int
	ActorReleasing   int
	GamePortReady    bool
	BreakerActive    bool
	CPUPercent       float64
	MemoryMB         int
	Goroutines       int
	OnlineSuccess    int
	OnlineFailed     int
}

type schedulerPolicyDecision struct {
	Mode   schedulerPolicyMode
	Reason string
}

func (m *RobotManager) SchedulerStatus() robotcap.SchedulerStatus {
	signals := m.adaptiveSchedulerSignals()
	rc, decision := m.refreshAdaptiveRobotConfig(signals)
	if !m.autoActionsEnabled(rc) {
		decision = schedulerPolicyDecision{Mode: schedulerPolicyManual, Reason: schedulerReasonAutoDisabled}
	}
	m.updateSchedulerStatus(rc, signals, decision)

	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.schedulerStatus
}

func (m *RobotManager) adaptiveSchedulerSignals() adaptiveSchedulerSignals {
	now := time.Now()
	m.autoMu.Lock()
	stats := m.autoStats
	live := !stats.UpdatedAt.IsZero()
	breaker := now.Before(m.autoBreakerUntil)
	m.autoMu.Unlock()

	cpu, mem, goroutines := process.ResourceSnapshot()
	return adaptiveSchedulerSignals{
		Live:             live,
		Running:          stats.Running,
		Connecting:       stats.Connecting,
		StoreRunning:     stats.StoreRunning,
		StoreUnsupported: m.requireBackendCapability(shared.CapabilityStore) != nil,
		Actors:           stats.Actors,
		Idle:             stats.Idle,
		ActorIdle:        stats.ActorIdle,
		ActorAssigned:    stats.ActorAssigned,
		ActorOnline:      stats.ActorOnline,
		ActorRunning:     stats.ActorRunning,
		ActorBusy:        stats.ActorBusy,
		ActorReleasing:   stats.ActorReleasing,
		GamePortReady:    !live || stats.GamePortReady,
		BreakerActive:    breaker,
		CPUPercent:       cpu,
		MemoryMB:         mem,
		Goroutines:       goroutines,
		OnlineSuccess:    m.schedulerRecentOnlineSuccess,
		OnlineFailed:     m.schedulerRecentOnlineFailed,
	}
}

func (m *RobotManager) updateSchedulerStatus(rc robotconfig.RuntimeConfig, sig adaptiveSchedulerSignals, decision schedulerPolicyDecision) {
	target := robotconfig.NormalizedTarget(rc)
	op, opStarted, opActive := m.structuralOperation()
	if opActive {
		decision = schedulerPolicyDecision{Mode: schedulerPolicyMaintenance, Reason: schedulerReasonStructuralPrefix + op}
	}
	recent := m.RecentOperation()
	status := robotcap.SchedulerStatus{
		Mode:                    string(decision.Mode),
		Reason:                  decision.Reason,
		RecentOperation:         recent.Type,
		RecentOperationState:    recent.State,
		RecentOperationSummary:  recentOperationSummary(recent),
		TargetOnline:            target,
		Running:                 sig.Running,
		Connecting:              sig.Connecting,
		Actors:                  sig.Actors,
		Idle:                    sig.Idle,
		ActorIdle:               sig.ActorIdle,
		ActorAssigned:           sig.ActorAssigned,
		ActorOnline:             sig.ActorOnline,
		ActorRunning:            sig.ActorRunning,
		ActorBusy:               sig.ActorBusy,
		ActorReleasing:          sig.ActorReleasing,
		StoreRunning:            sig.StoreRunning,
		StoreTarget:             adaptiveStoreTarget(target, sig.StoreUnsupported),
		GamePortReady:           sig.GamePortReady,
		BreakerActive:           sig.BreakerActive,
		CPUPercent:              sig.CPUPercent,
		MemoryMB:                sig.MemoryMB,
		Goroutines:              sig.Goroutines,
		OnlineBatchSize:         rc.SchedulerOnlineBatchSize,
		OnlineStartRate:         rc.SchedulerOnlineStartRate,
		OnlineFillTimeoutSec:    rc.SchedulerOnlineFillTimeout,
		MoveIntervalMinSec:      rc.AutoMoveIntervalMinSec,
		MoveIntervalMaxSec:      rc.AutoMoveIntervalMaxSec,
		ShoutIntervalMinSec:     rc.AutoShoutIntervalMinSec,
		ShoutIntervalMaxSec:     rc.AutoShoutIntervalMaxSec,
		StoreConcurrent:         rc.SchedulerStoreConcurrent,
		StoreProbabilityPercent: rc.AutoStoreProbabilityPercent,
		StoreIntervalMinSec:     rc.AutoStoreIntervalMinSec,
		StoreIntervalMaxSec:     rc.AutoStoreIntervalMaxSec,
		StoreDurationSec:        rc.AutoStoreDurationSec,
		StoreTickSec:            rc.AutoStoreTickSec,
		StoreMaxPositionTries:   rc.AutoStoreMaxPositionTries,
		StoreFailCooldownSec:    rc.AutoStoreFailCooldownSec,
		ScaleUpBatch:            robotconfig.ScaleUpBatch(rc),
		ScaleDownBatch:          robotconfig.ScaleDownBatch(sig.Actors, target),
		BreakerReleaseBatch:     rc.SchedulerBreakerReleaseBatch,
		PortDownReleaseBatch:    rc.SchedulerPortDownReleaseBatch,
		OnlineInFlight:          m.OnlineAttemptInFlight(),
		OnlineWindowSuccess:     sig.OnlineSuccess,
		OnlineWindowFailed:      sig.OnlineFailed,
		OnlineRetryBaseMS:       rc.SchedulerOnlineRetryBaseMS,
		CreateBatchSize:         rc.SchedulerCreateBatchSize,
		OperationActive:         opActive,
		Operation:               op,
		OperationStartedAt:      opStarted,
		UpdatedAt:               time.Now(),
	}
	m.autoMu.Lock()
	m.schedulerStatus = status
	m.autoMu.Unlock()
}

func recentOperationSummary(op robotcap.OperationStatus) string {
	if op.ID <= 0 {
		return ""
	}
	if op.Error != "" {
		return op.Error
	}
	return op.Summary
}

func applyAdaptiveSchedulerConfig(rc *robotconfig.RuntimeConfig, sig adaptiveSchedulerSignals) schedulerPolicyDecision {
	target := robotconfig.NormalizedTarget(*rc)
	scale := robotconfig.TargetScale(target)

	rc.SchedulerOnlineBatchSize = robotconfig.Clamp(target/10, 10, 60)
	rc.SchedulerOnlineStartRate = robotconfig.Clamp(target/40, 4, 30)
	rc.SchedulerOnlineFillTimeout = robotconfig.Clamp(target/10, 45, 180)

	rc.AutoMoveIntervalMinSec = robotconfig.Clamp(5+scale, 6, 18)
	rc.AutoMoveIntervalMaxSec = rc.AutoMoveIntervalMinSec + robotconfig.Clamp(10+scale*2, 12, 36)

	rc.SchedulerStoreConcurrent = robotconfig.Clamp(target/20, 5, 50)
	rc.AutoStoreProbabilityPercent = robotconfig.Clamp(120/scale, 5, 35)
	rc.AutoStoreIntervalMinSec = robotconfig.Clamp(45+scale*5, 50, 120)
	rc.AutoStoreIntervalMaxSec = rc.AutoStoreIntervalMinSec + robotconfig.Clamp(60+scale*10, 90, 240)
	rc.AutoStoreDurationSec = robotconfig.Clamp(120+scale*15, 120, 300)
	rc.AutoStoreTickSec = robotconfig.Clamp(5+scale, 10, 30)
	rc.AutoStoreMaxPositionTries = robotconfig.Clamp(4+scale, 5, 20)
	rc.AutoStoreFailCooldownSec = robotconfig.Clamp(60+scale*15, 60, 240)
	if sig.StoreUnsupported {
		rc.SchedulerStoreConcurrent = 0
		rc.AutoStoreProbabilityPercent = 0
	}

	rc.SchedulerBreakerAbnormalPct = 30
	rc.SchedulerBreakerPauseSec = robotconfig.Clamp(120+scale*30, 180, 600)
	rc.SchedulerBreakerReleaseBatch = robotconfig.Clamp(target/30, 5, 40)
	rc.SchedulerBreakerFloorPct = 70
	rc.SchedulerPortDownReleaseBatch = robotconfig.Clamp(target/25, 5, 50)
	// Retry pacing stays on the scheduler side: the configured values are
	// re-based to defaults, clamped here and stretched by the pressure branch
	// below, so every actor retry follows the same adaptive budget as the
	// initial fill.
	if rc.SchedulerOnlineRetryBaseMS <= 0 {
		rc.SchedulerOnlineRetryBaseMS = 5000
	}
	rc.SchedulerOnlineRetryBaseMS = robotconfig.Clamp(rc.SchedulerOnlineRetryBaseMS, 5000, 60000)
	if rc.SchedulerOnlineRetryMaxMS <= 0 {
		rc.SchedulerOnlineRetryMaxMS = 300000
	}
	if rc.SchedulerOnlineRetryMaxMS < rc.SchedulerOnlineRetryBaseMS {
		rc.SchedulerOnlineRetryMaxMS = rc.SchedulerOnlineRetryBaseMS
	}
	rc.SchedulerOnlineRetryMaxMS = robotconfig.Clamp(rc.SchedulerOnlineRetryMaxMS, rc.SchedulerOnlineRetryBaseMS, 1800000)
	rc.SchedulerOnlineRetryJitterPct = robotconfig.Clamp(rc.SchedulerOnlineRetryJitterPct, 0, 50)
	if rc.SchedulerOnlineInFlight <= 0 {
		rc.SchedulerOnlineInFlight = 12
	}
	rc.SchedulerOnlineInFlight = robotconfig.Clamp(rc.SchedulerOnlineInFlight, 2, 24)
	if rc.SchedulerRecycleCooldownSec <= 0 {
		rc.SchedulerRecycleCooldownSec = 600
	}
	rc.SchedulerRecycleCooldownSec = robotconfig.Clamp(rc.SchedulerRecycleCooldownSec, 30, 3600)
	if rc.SchedulerOnlineBreakerPauseSec <= 0 {
		rc.SchedulerOnlineBreakerPauseSec = 60
	}
	rc.SchedulerOnlineBreakerPauseSec = robotconfig.Clamp(rc.SchedulerOnlineBreakerPauseSec, 15, 300)
	if rc.SchedulerCreateBatchSize <= 0 {
		rc.SchedulerCreateBatchSize = 10
	}
	rc.SchedulerCreateBatchSize = robotconfig.Clamp(rc.SchedulerCreateBatchSize, 1, 40)

	if !sig.Live {
		return schedulerPolicyDecision{Mode: schedulerPolicyBootstrap, Reason: fmt.Sprintf("target=%d %s", target, schedulerReasonNoLiveSnapshot)}
	}
	return applyLiveSchedulerFeedback(rc, target, sig)
}

func applyLiveSchedulerFeedback(rc *robotconfig.RuntimeConfig, target int, sig adaptiveSchedulerSignals) schedulerPolicyDecision {
	if target <= 0 {
		return schedulerPolicyDecision{Mode: schedulerPolicyStable, Reason: schedulerReasonTargetZero}
	}
	connectingLimit := robotconfig.Clamp(target/20, 2, 30)
	runningUpperBound := target + connectingLimit
	healthyOnline := sig.Running >= target*95/100 && sig.Running <= runningUpperBound && sig.Connecting <= connectingLimit && sig.GamePortReady && !sig.BreakerActive
	storeTarget := adaptiveStoreTarget(target, sig.StoreUnsupported)
	storeRoom := sig.StoreRunning < storeTarget
	idleRoom := sig.Idle >= robotconfig.Clamp(target/50, 2, 20)
	resourcePressure := sig.CPUPercent >= 85 || sig.MemoryMB >= 4096 || sig.Goroutines >= 20000
	pendingPressure := sig.Idle >= robotconfig.PendingActorLimit(target, *rc)
	connectionPressure := sig.Connecting > robotconfig.Clamp(target/10, 3, 60)
	onlineAttempts := sig.OnlineSuccess + sig.OnlineFailed
	onlineFailurePressure := onlineAttempts >= 10 && sig.OnlineFailed*100 >= onlineAttempts*20
	pressure := sig.BreakerActive || !sig.GamePortReady || resourcePressure || connectionPressure || pendingPressure || onlineFailurePressure

	if pressure {
		if onlineFailurePressure && sig.GamePortReady && !sig.BreakerActive && !resourcePressure {
			divisor := 2
			if sig.OnlineFailed*2 >= onlineAttempts {
				divisor = 4
			}
			rc.SchedulerOnlineBatchSize = robotconfig.Clamp(rc.SchedulerOnlineBatchSize/divisor, 5, 30)
			rc.SchedulerOnlineStartRate = robotconfig.Clamp(rc.SchedulerOnlineStartRate/divisor, 2, 20)
			rc.SchedulerOnlineFillTimeout = robotconfig.Clamp(rc.SchedulerOnlineFillTimeout*2, 60, 300)
			rc.SchedulerOnlineRetryBaseMS = robotconfig.Clamp(rc.SchedulerOnlineRetryBaseMS*3, 5000, 60000)
			rc.SchedulerOnlineRetryMaxMS = robotconfig.Clamp(rc.SchedulerOnlineRetryMaxMS*3, 60000, 1800000)
			rc.SchedulerOnlineRetryJitterPct = robotconfig.Clamp(rc.SchedulerOnlineRetryJitterPct+10, 20, 50)
			rc.SchedulerOnlineInFlight = robotconfig.Clamp(rc.SchedulerOnlineInFlight/2, 2, 24)
			return schedulerPolicyDecision{Mode: schedulerPolicyPressure, Reason: fmt.Sprintf("online_failure_pressure success=%d failed=%d running=%d target=%d", sig.OnlineSuccess, sig.OnlineFailed, sig.Running, target)}
		}
		if pendingPressure && sig.GamePortReady && !sig.BreakerActive && !resourcePressure && !connectionPressure {
			rc.SchedulerOnlineBatchSize = -1
			rc.SchedulerOnlineStartRate = robotconfig.Clamp(rc.SchedulerOnlineStartRate/2, 1, 10)
			rc.SchedulerStoreConcurrent = robotconfig.Clamp(rc.SchedulerStoreConcurrent/2, 2, 25)
			rc.AutoStoreProbabilityPercent = robotconfig.Clamp(rc.AutoStoreProbabilityPercent/3, 1, 15)
			return schedulerPolicyDecision{Mode: schedulerPolicyPressure, Reason: fmt.Sprintf("%s idle=%d actors=%d running=%d target=%d", schedulerReasonPendingBacklog, sig.Idle, sig.Actors, sig.Running, target)}
		}
		rc.SchedulerOnlineBatchSize = robotconfig.Clamp(rc.SchedulerOnlineBatchSize/2, 5, 60)
		rc.SchedulerOnlineStartRate = robotconfig.Clamp(rc.SchedulerOnlineStartRate/2, 4, 30)
		rc.SchedulerOnlineFillTimeout = robotconfig.Clamp(rc.SchedulerOnlineFillTimeout*2, 60, 300)
		rc.SchedulerOnlineRetryBaseMS = robotconfig.Clamp(rc.SchedulerOnlineRetryBaseMS*2, 5000, 60000)
		rc.SchedulerOnlineRetryMaxMS = robotconfig.Clamp(rc.SchedulerOnlineRetryMaxMS*2, 60000, 1800000)
		rc.SchedulerOnlineInFlight = robotconfig.Clamp(rc.SchedulerOnlineInFlight/2, 2, 24)
		rc.AutoMoveIntervalMinSec = robotconfig.Clamp(rc.AutoMoveIntervalMinSec*3/2, 10, 45)
		rc.AutoMoveIntervalMaxSec = robotconfig.Clamp(rc.AutoMoveIntervalMaxSec*3/2, rc.AutoMoveIntervalMinSec+12, 90)
		rc.SchedulerStoreConcurrent = robotconfig.Clamp(rc.SchedulerStoreConcurrent/2, 2, 25)
		rc.AutoStoreProbabilityPercent = robotconfig.Clamp(rc.AutoStoreProbabilityPercent/3, 1, 15)
		rc.AutoStoreIntervalMinSec = robotconfig.Clamp(rc.AutoStoreIntervalMinSec*3/2, 60, 240)
		rc.AutoStoreIntervalMaxSec = robotconfig.Clamp(rc.AutoStoreIntervalMaxSec*3/2, rc.AutoStoreIntervalMinSec+60, 480)
		rc.AutoStoreFailCooldownSec = robotconfig.Clamp(rc.AutoStoreFailCooldownSec*2, 120, 600)
		rc.SchedulerBreakerReleaseBatch = robotconfig.Clamp(rc.SchedulerBreakerReleaseBatch*3/2, 5, 60)
		rc.SchedulerPortDownReleaseBatch = robotconfig.Clamp(rc.SchedulerPortDownReleaseBatch*3/2, 5, 80)
		mode := schedulerPolicyPressure
		if sig.BreakerActive {
			mode = schedulerPolicyBreaker
		}
		return schedulerPolicyDecision{Mode: mode, Reason: fmt.Sprintf("running=%d connecting=%d store=%d cpu=%.1f mem=%d goroutines=%d port=%v", sig.Running, sig.Connecting, sig.StoreRunning, sig.CPUPercent, sig.MemoryMB, sig.Goroutines, sig.GamePortReady)}
	}
	if sig.Running > runningUpperBound {
		rc.AutoStoreProbabilityPercent = 0
		return schedulerPolicyDecision{Mode: schedulerPolicyStable, Reason: fmt.Sprintf("running=%d target=%d scaling_down", sig.Running, target)}
	}

	if healthyOnline && storeRoom {
		storeGap := storeTarget - sig.StoreRunning
		storeBoost := robotconfig.Clamp(target/40, 2, 25)
		if idleRoom {
			storeBoost += robotconfig.Clamp(sig.Idle/2, 1, 20)
		}
		expandedConcurrent := robotconfig.Clamp(rc.SchedulerStoreConcurrent+storeBoost, 5, 90)
		gapConcurrent := robotconfig.Clamp(storeGap*2, 5, 90)
		if expandedConcurrent > gapConcurrent {
			expandedConcurrent = gapConcurrent
		}
		rc.SchedulerStoreConcurrent = expandedConcurrent
		rc.AutoStoreProbabilityPercent = robotconfig.Clamp(rc.AutoStoreProbabilityPercent+robotconfig.Clamp(storeGap, 1, 10), 5, 60)
		rc.AutoMoveIntervalMinSec = robotconfig.Clamp(rc.AutoMoveIntervalMinSec*9/10, 5, 18)
		rc.AutoMoveIntervalMaxSec = robotconfig.Clamp(rc.AutoMoveIntervalMaxSec*9/10, rc.AutoMoveIntervalMinSec+10, 54)
		rc.AutoStoreIntervalMinSec = robotconfig.Clamp(rc.AutoStoreIntervalMinSec*4/5, 30, 120)
		rc.AutoStoreIntervalMaxSec = robotconfig.Clamp(rc.AutoStoreIntervalMaxSec*4/5, rc.AutoStoreIntervalMinSec+60, 300)
		positionTryBoost := robotconfig.Clamp((storeGap+9)/10, 1, 3)
		rc.AutoStoreMaxPositionTries = robotconfig.Clamp(rc.AutoStoreMaxPositionTries+positionTryBoost, 5, 30)
		return schedulerPolicyDecision{Mode: schedulerPolicyStore, Reason: fmt.Sprintf("running=%d idle=%d store=%d/%d create_limit=%d", sig.Running, sig.Idle, sig.StoreRunning, storeTarget, rc.SchedulerStoreConcurrent)}
	}
	if healthyOnline && !storeRoom {
		rc.AutoStoreProbabilityPercent = 0
		return schedulerPolicyDecision{Mode: schedulerPolicyStable, Reason: fmt.Sprintf("running=%d connecting=%d store=%d/%d", sig.Running, sig.Connecting, sig.StoreRunning, storeTarget)}
	}

	if sig.Running+sig.Connecting < target && sig.Connecting <= connectingLimit {
		rc.SchedulerOnlineBatchSize = robotconfig.Clamp(rc.SchedulerOnlineBatchSize+target/20, 20, 120)
		rc.SchedulerOnlineStartRate = robotconfig.Clamp(rc.SchedulerOnlineStartRate+target/100, 8, 60)
		return schedulerPolicyDecision{Mode: schedulerPolicyFill, Reason: fmt.Sprintf("running=%d connecting=%d target=%d", sig.Running, sig.Connecting, target)}
	}
	return schedulerPolicyDecision{Mode: schedulerPolicyStable, Reason: fmt.Sprintf("running=%d connecting=%d store=%d/%d", sig.Running, sig.Connecting, sig.StoreRunning, storeTarget)}
}

func adaptiveActiveStoreTarget(target int) int {
	if target <= 0 {
		return 0
	}
	return robotconfig.Clamp((target+3)/4, 1, adaptiveActiveStoreCapacity)
}

func adaptiveStoreTarget(target int, unsupported bool) int {
	if unsupported {
		return 0
	}
	return adaptiveActiveStoreTarget(target)
}
