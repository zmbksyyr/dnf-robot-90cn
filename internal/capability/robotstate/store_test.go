package robotstate

import (
	"context"
	"errors"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func TestMemoryStoreUpdatesDirectoryAndLocations(t *testing.T) {
	store := NewMemoryStore([]robotcap.Info{{UID: 7, CID: 70, Village: 1, Area: 2, X: 30, Y: 40}})
	if err := store.UpdateRobotPositions(context.Background(), []robotcap.PositionUpdate{{UID: 7, Village: 3, Area: 4, X: 50, Y: 60}}); err != nil {
		t.Fatal(err)
	}
	robots, err := store.SelectRobots(context.Background(), robotcap.CommandRequest{UIDs: []int{7}})
	if err != nil || len(robots) != 1 || robots[0].Village != 3 || robots[0].X != 50 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	locations, err := store.RobotLocations(context.Background())
	if err != nil || len(locations) != 1 || locations[0].Area != 4 {
		t.Fatalf("locations=%+v err=%v", locations, err)
	}
}

func TestMemoryStoreRejectsUnknownPosition(t *testing.T) {
	store := NewMemoryStore(nil)
	if !errors.Is(store.UpdateRobotPositions(context.Background(), []robotcap.PositionUpdate{{UID: 99}}), ErrNotFound) {
		t.Fatal("unknown robot position unexpectedly succeeded")
	}
}

func TestMemoryStoreHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewMemoryStore(nil).SelectRobots(ctx, robotcap.CommandRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestMemoryStoreRegistersBackendNeutralIdentity(t *testing.T) {
	store := NewMemoryStore(nil)
	slot := uint16(4)
	identity := Identity{Backend: shared.BackendS4A21, Account: "acct", CharacterName: "robot", Slot: &slot}
	if err := store.RegisterIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterIdentity(context.Background(), identity); !errors.Is(err, ErrDuplicateIdentity) {
		t.Fatalf("duplicate error = %v", err)
	}
	identities, err := store.Identities(context.Background(), shared.BackendS4A21)
	if err != nil || len(identities) != 1 || identities[0].Slot == nil || *identities[0].Slot != slot {
		t.Fatalf("identities = %+v, err=%v", identities, err)
	}
}
