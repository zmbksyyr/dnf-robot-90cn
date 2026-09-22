package s4a21

import "testing"

import protocol "robot/internal/protocol/s4a21"

func TestDungeonRunStateFollowsVerifiedSingleRoleWorkflow(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got.Phase != uint8(dungeonPhaseSelection) || got.DungeonID != 144 || got.Generation != 1 {
		t.Fatalf("selection snapshot = %+v", got)
	}
	if err := state.BeginEntry(); err != nil {
		t.Fatal(err)
	}
	if err := state.StartLoading(0, 3); err != nil {
		t.Fatal(err)
	}
	if err := state.FinishLoading(); err != nil {
		t.Fatal(err)
	}
	if err := state.StartLoading(1, 3); err != nil {
		t.Fatal(err)
	}
	if err := state.FinishLoading(); err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got.Phase != uint8(dungeonPhaseReady) || got.RoomX != 1 || got.RoomY != 3 {
		t.Fatalf("ready snapshot = %+v", got)
	}
}

func TestDungeonRunStateRejectsStaleWorkflowTransitions(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginEntry(); err == nil {
		t.Fatal("entry without selection unexpectedly succeeded")
	}
	if err := state.BeginSelection(0); err == nil {
		t.Fatal("zero dungeon selection unexpectedly succeeded")
	}
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if err := state.FinishLoading(); err == nil {
		t.Fatal("loading completion before START_MAP unexpectedly succeeded")
	}
	state.ReturnToTown()
	if got := state.Snapshot(); got.Phase != uint8(dungeonPhaseTown) || got.DungeonID != 0 || got.RoomX != 0 || got.RoomY != 0 {
		t.Fatalf("town snapshot = %+v", got)
	}
}

func TestDungeonRunStateGenerationSurvivesReturnAndRejectsOldPhase(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	first := state.Snapshot().Generation
	state.ReturnToTown()
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	second := state.Snapshot().Generation
	if second != first+1 {
		t.Fatalf("generation after new selection=%d, first=%d", second, first)
	}
	if err := state.BeginSelection(144); err == nil {
		t.Fatal("duplicate selection unexpectedly succeeded")
	}
}

func TestDungeonRunStateConsumesVerifiedPacketSequence(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginEntry(); err != nil {
		t.Fatal(err)
	}
	if err := state.AcceptPacket(protocol.Packet{Command: 0, Type: 0x001D, Body: []byte{0, 3}}); err != nil {
		t.Fatal(err)
	}
	if err := state.AcceptPacket(protocol.Packet{Command: 0, Type: 0x001E, Body: []byte{0, 0, 0, 0, 0}}); err != nil {
		t.Fatal(err)
	}
	got := state.Snapshot()
	if got.Phase != uint8(dungeonPhaseReady) || got.RoomX != 0 || got.RoomY != 3 {
		t.Fatalf("packet sequence snapshot = %+v", got)
	}
}

func TestDungeonRunStateRejectsMalformedOrWrongDirectionPackets(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if err := state.AcceptPacket(protocol.Packet{Command: 1, Type: 0x001D, Body: []byte{0, 3}}); err == nil {
		t.Fatal("response-shaped START_MAP packet unexpectedly accepted")
	}
	if err := state.AcceptPacket(protocol.Packet{Command: 0, Type: 0x001D, Body: []byte{0}}); err == nil {
		t.Fatal("truncated START_MAP unexpectedly accepted")
	}
}

func TestDungeonRunStateDoesNotCommitPendingRoomBeforeLoadingRelease(t *testing.T) {
	var state dungeonRunState
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginEntry(); err != nil {
		t.Fatal(err)
	}
	if err := state.StartLoading(0, 3); err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got.RoomX != 0 || got.RoomY != 0 || got.Phase != uint8(dungeonPhaseLoading) {
		t.Fatalf("pending room was committed early: %+v", got)
	}
	if err := state.FinishLoading(); err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot(); got.RoomX != 0 || got.RoomY != 3 || got.Phase != uint8(dungeonPhaseReady) {
		t.Fatalf("released room snapshot = %+v", got)
	}
}
