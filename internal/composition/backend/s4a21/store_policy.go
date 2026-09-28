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

// ItemStoreSupported reports whether the adapter implements the private item
// stall. S4A21 exposes the disassembler machine only, so the scheduler must not
// alternate attempts into the unavailable item workflow.
func (StorePolicy) ItemStoreSupported() bool { return false }

// DisjointFailure maps a wire failure code to a stable reason string and
// whether a different coordinate may retry on the same session.
//
// The codes come from the A21 ExpertJobStoreRuntimeService and its placement
// validator:
//
//	0x0a (10): the server rejected the create body (adapter bug).
//	0x13 (19): invalid state - in party, in a dungeon, profession mismatch,
//	           cost above the PVF limit, or the machine has no endurance.
//	0x52 (82): the coordinate is inside an NPC commercial-restricted box
//	           (x +/- 80, y +/- 150), so another point may succeed.
//	0xbe (190): the point is outside the movable area, or this character
//	           already owns a store; another point may succeed.
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
	case 0x52, 0xbe:
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
	case "disjoint_err_0x52", "disjoint_err_0xbe":
		return true, true
	case "disjoint_failed":
		return false, true
	}
	if strings.HasPrefix(reason, "disjoint_err_") {
		return false, true
	}
	return false, false
}
