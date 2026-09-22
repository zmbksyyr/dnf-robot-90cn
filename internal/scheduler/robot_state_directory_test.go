package scheduler

import (
	"context"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
)

func TestRobotManagerUsesInjectedRobotStateDirectory(t *testing.T) {
	store := robotstate.NewMemoryStore([]robotcap.Info{{UID: 11, CID: 110, Village: 1, Area: 2, X: 30, Y: 40}})
	manager := NewRobotManager(nil, nil, nil)
	manager.SetRobotStateDirectory(store)
	robots, err := manager.selectRobots(robotcap.CommandRequest{UIDs: []int{11}})
	if err != nil || len(robots) != 1 || robots[0].UID != 11 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	locations, err := manager.robotLocations()
	if err != nil || len(locations) != 1 || locations[0].X != 30 {
		t.Fatalf("locations=%+v err=%v", locations, err)
	}
	if err := store.UpdateRobotPositions(context.Background(), []robotcap.PositionUpdate{{UID: 11, Village: 4, Area: 5, X: 60, Y: 70}}); err != nil {
		t.Fatal(err)
	}
	locations, err = manager.robotLocations()
	if err != nil || len(locations) != 1 || locations[0].Village != 4 {
		t.Fatalf("updated locations=%+v err=%v", locations, err)
	}
}
