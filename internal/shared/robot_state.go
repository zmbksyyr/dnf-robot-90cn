package shared

import "time"

type DesiredState string

const (
	DesiredUnknown DesiredState = ""
	DesiredOnline  DesiredState = "online"
	DesiredOffline DesiredState = "offline"
	DesiredMove    DesiredState = "move"
	DesiredShout   DesiredState = "shout"
	DesiredStore   DesiredState = "store"
	DesiredCleanup DesiredState = "cleanup"
)

type ActualState string

const (
	ActualUnknown      ActualState = ""
	ActualStopped      ActualState = "stopped"
	ActualConnecting   ActualState = "connecting"
	ActualLogin        ActualState = "login"
	ActualRunning      ActualState = "running"
	ActualDisconnected ActualState = "disconnected"
	ActualError        ActualState = "error"
)

type Phase string

const (
	PhaseAssigned  Phase = "assigned"
	PhaseExecuting Phase = "executing"
	PhaseConfirmed Phase = "confirmed"
	PhaseFailed    Phase = "failed"
)

type RobotState struct {
	UID          int          `json:"uid"`
	CID          int          `json:"cid,omitempty"`
	ActorID      string       `json:"actor_id,omitempty"`
	DesiredState DesiredState `json:"desired_state,omitempty"`
	ActualState  ActualState  `json:"actual_state,omitempty"`
	Phase        Phase        `json:"phase,omitempty"`
	LockVersion  int64        `json:"lock_version"`
	LastError    string       `json:"last_error,omitempty"`
	UpdatedAt    time.Time    `json:"updated_at"`
}

func RuntimeActualState(stateName string, disconnectReason int, missingCore bool) ActualState {
	if missingCore {
		return ActualError
	}
	if disconnectReason != 0 {
		return ActualDisconnected
	}
	switch stateName {
	case "running":
		return ActualRunning
	case "init":
		return ActualConnecting
	case "login":
		return ActualLogin
	case "stop", "clean", "offline":
		return ActualStopped
	case "wrong", "broken":
		return ActualError
	default:
		return ActualUnknown
	}
}

func DesiredFromOperation(operation string, onlineDesired bool) DesiredState {
	switch operation {
	case "cleanup", "deleting":
		return DesiredCleanup
	case "store":
		return DesiredStore
	case "move":
		return DesiredMove
	case "shout":
		return DesiredShout
	}
	if onlineDesired {
		return DesiredOnline
	}
	return DesiredOffline
}
