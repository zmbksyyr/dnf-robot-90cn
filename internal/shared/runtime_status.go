package shared

type RuntimeStatus struct {
	UID                  int
	CID                  int
	GuildID              int
	State                int
	StateName            string
	DisconnectReason     int
	Reconnects           int
	RunStartTime         int64
	UptimeSeconds        int
	RobotType            int
	StoreDisplaySent     bool
	StoreDisplayAck      bool
	StoreDisplayItems    int
	StoreDisplayRejected bool
	StoreCreateRejected  bool
	LastStoreError       byte
	StoreCreated         bool
	DisjointCreateSent   bool
	DisjointDirectAck    bool
	DisjointActive       bool
	LastDisjointError    byte
	EnchantCreateSent    bool
	EnchantDirectAck     bool
	EnchantActive        bool
	LastEnchantError     byte
	PartyActive          bool
	Village              int
	Area                 int
	X                    int
	Y                    int
}

const (
	RuntimeStateStop    = "stop"
	RuntimeStateInit    = "init"
	RuntimeStateLogin   = "login"
	RuntimeStateRunning = "running"
	RuntimeStateClean   = "clean"
	RuntimeStateWrong   = "wrong"
	RuntimeStateSelect  = "character_select"
	RuntimeStateUnknown = "unknown"
)

func StateName(state int) string {
	switch state {
	case 0:
		return RuntimeStateStop
	case 1:
		return RuntimeStateInit
	case 2:
		return RuntimeStateLogin
	case 3:
		return RuntimeStateRunning
	case 4:
		return RuntimeStateClean
	case 5:
		return RuntimeStateWrong
	case 6:
		return RuntimeStateSelect
	default:
		return RuntimeStateUnknown
	}
}

func ActiveRuntimeStatus(st RuntimeStatus) bool {
	return st.StateName == RuntimeStateRunning && st.DisconnectReason == 0
}

func CopyRuntimeStatusMap(in map[int]RuntimeStatus) map[int]RuntimeStatus {
	out := make(map[int]RuntimeStatus, len(in))
	for uid, st := range in {
		out[uid] = st
	}
	return out
}

type RuntimeStatusSummary struct {
	Running        int
	Connecting     int
	Stores         int
	ItemStores     int
	DisjointStores int
	EnchantStores  int
}

func SummarizeRuntimeStatusMap(status map[int]RuntimeStatus) RuntimeStatusSummary {
	var summary RuntimeStatusSummary
	for _, st := range status {
		summary.Add(st)
	}
	return summary
}

func (s *RuntimeStatusSummary) Add(st RuntimeStatus) {
	if st.DisconnectReason != 0 {
		return
	}
	switch st.StateName {
	case RuntimeStateRunning:
		s.Running++
		if st.RobotType == 2 && st.StoreDisplayAck {
			s.Stores++
			s.ItemStores++
		}
		if st.RobotType == 3 && st.DisjointActive {
			s.Stores++
			s.DisjointStores++
		}
		if st.RobotType == 3 && st.EnchantActive {
			s.Stores++
			s.EnchantStores++
		}
	case RuntimeStateInit, RuntimeStateLogin:
		s.Connecting++
	}
}
