package s4a21

import (
	"fmt"
	"strings"
)

// StorePolicy exposes S4A21 private-store semantics to the shared scheduler.
// The wire error codes below come from the server disjoint-store dispatcher and
// CDisjointer::OnCreateDisjointStore; they belong to this adapter, not to the
// scheduler.
type StorePolicy struct{}

const s4a21DisjointStoreCostGold = 500

// DisjointStoreCost is the gold cost of one disjoint-store attempt.
func (StorePolicy) DisjointStoreCost() uint32 { return s4a21DisjointStoreCostGold }

// DisjointFailure maps a wire failure code to a stable reason string and
// whether a different coordinate may retry on the same session.
//
//	0x13: not in town-run state, already has a disjoint object, or in party.
//	0x14: CVillageObjectMgr::register_object rejected the machine.
//	0x16: disjoint-machine endurance <= 0; coordinates cannot repair it.
//	0x3e: the current area does not permit commercial transactions.
//	0x52: the coordinate is inside a restrictive transaction zone.
//	0xbe: private store busy, village 7, or is_available_point rejected it.
//	0x0a: invalid disjoint cost; 0x15: expert-job object pool exhausted.
//	-1/-2 (wire 0xff/0xfe): invalid user state or profession mismatch.
//
// Only coordinate-dependent failures may retry in the same session; structural
// and profession-data failures must stop instead of causing retry storms.
func (StorePolicy) DisjointFailure(errCode byte) (string, bool) {
	if errCode == 0 {
		return "disjoint_failed", false
	}
	return fmt.Sprintf("disjoint_err_0x%02x", errCode), s4a21DisjointRetrySameSession(errCode)
}

func s4a21DisjointRetrySameSession(errCode byte) bool {
	switch errCode {
	case 0x14, 0x3e, 0x52, 0xbe:
		return true
	default:
		return false
	}
}

// DisjointReasonRetryable classifies adapter-owned disjoint failure reasons.
// Every disjoint_err_* reason is claimed by this adapter; scheduler-owned
// reasons (set_area_failed, ack_timeout, ...) stay unclaimed.
func (StorePolicy) DisjointReasonRetryable(reason string) (bool, bool) {
	switch reason {
	case "disjoint_err_0x14", "disjoint_err_0x3e", "disjoint_err_0x52", "disjoint_err_0xbe":
		return true, true
	case "disjoint_failed":
		return false, true
	}
	if strings.HasPrefix(reason, "disjoint_err_") {
		return false, true
	}
	return false, false
}
