package cn90

import (
	"fmt"
	"strings"
)

// StorePolicy exposes 90CN expert-job store semantics to the shared
// scheduler. The wire error codes below come from the server expert-job store
// dispatcher and its placement validator; they belong to this adapter, not to
// the scheduler.
type StorePolicy struct{}

const (
	cn90DisjointStoreCostGold = 500
	cn90EnchantStoreCostGold  = 500
)

// DisjointStoreCost is the gold cost of one disjoint-store attempt.
func (StorePolicy) DisjointStoreCost() uint32 { return cn90DisjointStoreCostGold }

// EnchantStoreCost is the gold cost of one enchanter-stall attempt.
func (StorePolicy) EnchantStoreCost() uint32 { return cn90EnchantStoreCostGold }

// ItemStoreSupported reports whether the adapter implements the private item
// stall. 90CN exposes the expert-job stalls only, so the scheduler must not
// alternate attempts into the unavailable item workflow.
func (StorePolicy) ItemStoreSupported() bool { return false }

// DisjointFailure maps a wire failure code to a stable reason string and
// whether a different coordinate may retry on the same session.
//
// The codes come from the 90CN ExpertJobStoreRuntimeService and its placement
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
	return fmt.Sprintf("disjoint_err_0x%02x", errCode), cn90ExpertStoreRetrySameSession(errCode)
}

// EnchantFailure maps an enchanter-stall wire failure code. The enchanter shop
// shares the server's create validation with the disassembler machine, so the
// codes have the same meaning.
func (StorePolicy) EnchantFailure(errCode byte) (string, bool) {
	if errCode == 0 {
		return "enchant_failed", false
	}
	return fmt.Sprintf("enchant_err_0x%02x", errCode), cn90ExpertStoreRetrySameSession(errCode)
}

func cn90ExpertStoreRetrySameSession(errCode byte) bool {
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

// EnchantReasonRetryable classifies adapter-owned enchanter failure reasons.
func (StorePolicy) EnchantReasonRetryable(reason string) (bool, bool) {
	switch reason {
	case "enchant_err_0x52", "enchant_err_0xbe":
		return true, true
	case "enchant_failed":
		return false, true
	}
	if strings.HasPrefix(reason, "enchant_err_") {
		return false, true
	}
	return false, false
}
