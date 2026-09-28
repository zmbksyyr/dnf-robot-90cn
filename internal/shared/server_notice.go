package shared

import "time"

// ServerNoticeKind identifies one robot-side action that makes the game server
// broadcast a server-wide item notice. It is a robot-owned enum: the adapter
// maps it to its own protocol operation.
type ServerNoticeKind string

const (
	// ServerNoticeLottery opens one lottery box (legacy pot). The server rolls
	// the reward pool; rare loot broadcasts the item notice.
	ServerNoticeLottery ServerNoticeKind = "lottery"
	// ServerNoticeUpgrade attempts one normal reinforcement on equipment
	// already at or above the table notice level, so both success and failure
	// broadcast.
	ServerNoticeUpgrade ServerNoticeKind = "upgrade"
)

func (k ServerNoticeKind) Name() string {
	switch k {
	case ServerNoticeLottery:
		return "lottery"
	case ServerNoticeUpgrade:
		return "upgrade"
	default:
		return string(k)
	}
}

// ServerNoticeTriggerRequest selects one online robot for a notice action.
type ServerNoticeTriggerRequest struct {
	UID  int
	CID  int
	Kind ServerNoticeKind
}

// ServerNoticeTriggerResult is the adapter-observed outcome of one trigger.
// Accepted means the game server acknowledged the action; Broadcast means the
// robot observed the resulting 0x0056 server notice.
type ServerNoticeTriggerResult struct {
	Kind      ServerNoticeKind `json:"kind"`
	UID       int              `json:"uid"`
	CID       int              `json:"cid"`
	Sent      bool             `json:"sent"`
	Accepted  bool             `json:"accepted"`
	Broadcast bool             `json:"broadcast"`
	ItemID    int              `json:"item_id,omitempty"`
	Level     int              `json:"level,omitempty"`
	Reason    string           `json:"reason,omitempty"`
	At        time.Time        `json:"at"`
}

// ServerNoticer is the adapter-owned live-session surface for notice actions.
// Adapters without a verified trigger keep the capability disabled instead of
// sending a guessed packet.
type ServerNoticer interface {
	TriggerServerNotice(ServerNoticeTriggerRequest) (ServerNoticeTriggerResult, error)
}

// ServerNoticeStockWriter prepares the items a notice action consumes in the
// adapter's persistence boundary. Ensure calls run while the account is
// offline; Ready probes are read-only and may run at any time.
type ServerNoticeStockWriter interface {
	EnsureServerNoticeStock(cid int, kind ServerNoticeKind) error
	ServerNoticeStockReady(cid int, kind ServerNoticeKind) (bool, error)
}

// ServerNoticeEvent is one observed 0x0056 server notice. The Web/API status
// layer lists recent events for operators.
type ServerNoticeEvent struct {
	Kind     string    `json:"kind"`
	UID      int       `json:"uid"`
	CID      int       `json:"cid,omitempty"`
	ItemID   int       `json:"item_id,omitempty"`
	Level    int       `json:"level,omitempty"`
	Success  bool      `json:"success,omitempty"`
	Observed time.Time `json:"observed_at"`
}
