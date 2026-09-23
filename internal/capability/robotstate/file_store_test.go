package robotstate

import (
	"context"
	"path/filepath"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func TestFileStorePersistsRobotStateWithoutGameDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "robot_state.json")
	store, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.MemoryStore = NewMemoryStore([]robotcap.Info{{UID: 7, Name: "robot"}})
	if err := store.UpdateRobotPositions(context.Background(), []robotcap.PositionUpdate{{UID: 7, Village: 1, Area: 2, X: 30, Y: 40}}); err != nil {
		t.Fatal(err)
	}
	slot := uint16(3)
	if err := store.RegisterIdentity(context.Background(), Identity{Backend: shared.BackendS4A21, Account: "acct", CharacterName: "robot", Slot: &slot}); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginCreateBatch(context.Background(), CreateBatch{ID: "batch", Backend: shared.BackendS4A21}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteCreateBatch(context.Background(), "batch"); err != nil {
		t.Fatal(err)
	}

	reloaded, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	robots, err := reloaded.SelectRobots(context.Background(), robotcap.CommandRequest{UIDs: []int{7}})
	if err != nil || len(robots) != 1 || robots[0].X != 30 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	identities, err := reloaded.Identities(context.Background(), shared.BackendS4A21)
	if err != nil || len(identities) != 1 || identities[0].Slot == nil || *identities[0].Slot != slot {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
	if err := reloaded.RemoveRobots(context.Background(), []int{7}); err != nil {
		t.Fatal(err)
	}
	reloaded, err = OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	robots, _ = reloaded.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	identities, _ = reloaded.Identities(context.Background(), shared.BackendS4A21)
	if len(robots) != 0 || len(identities) != 0 {
		t.Fatalf("removed state was persisted: robots=%+v identities=%+v", robots, identities)
	}
}
