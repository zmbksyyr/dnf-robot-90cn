package scheduler

import (
	"context"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/shared"
)

func TestRobotsStatusUsesSimulatorRobotStateWithoutSchemaRepository(t *testing.T) {
	store := robotstate.NewMemoryStore([]robotcap.Info{{
		UID: 7, CID: 70, Name: "sim-robot", Level: 50, Job: 1,
		Village: 1, Area: 2, X: 480, Y: 240,
	}})
	if err := store.RegisterIdentity(context.Background(), robotstate.Identity{
		Backend: shared.BackendS4A21, Account: "sim-account", CharacterName: "sim-robot",
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewRobotManager(nil, nil, nil)
	manager.SetRobotStateDirectory(store)
	manager.SetBackendRobotCreator(testS4BackendInfo(), nil)
	manager.SetTownMapCatalog([]shared.MapCatalogItem{{Village: 1, VillageName: "Town"}})

	result, err := manager.RobotsStatus(robotcap.CommandRequest{Count: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Robots) != 1 {
		t.Fatalf("result = %+v", result)
	}
	item := result.Robots[0]
	if item.UID != 7 || item.Name != "sim-robot" || item.Account != "sim-account" || item.VillageName != "Town" {
		t.Fatalf("item = %+v", item)
	}
}

func TestSimulatorShoutNameUsesRobotStateWithoutSchemaRepository(t *testing.T) {
	store := robotstate.NewMemoryStore([]robotcap.Info{{UID: 7, Name: "sim-robot"}})
	manager := NewRobotManager(nil, nil, nil)
	manager.SetRobotStateDirectory(store)
	manager.SetBackendRobotCreator(testS4BackendInfo(), nil)
	if got := (shoutActionEnv{manager: manager}).LookupRobotName(7); got != "sim-robot" {
		t.Fatalf("name=%q, want sim-robot", got)
	}
}

func TestFileStoreRobotRegistrationSurvivesReload(t *testing.T) {
	path := t.TempDir() + "/robot_state.json"
	store, err := robotstate.OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	robot := robotcap.Info{UID: 8, Name: "persisted"}
	if err := store.RegisterRobots(context.Background(), []robotcap.Info{robot}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := robotstate.OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	robots, err := reloaded.SelectRobots(context.Background(), robotcap.CommandRequest{UIDs: []int{robot.UID}})
	if err != nil || len(robots) != 1 || robots[0].Name != robot.Name {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
}
