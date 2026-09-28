package scheduler

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/shared"
)

func TestProtocolPrepareOnlineRepairsInvalidStoredArea(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = false\nfollow_account =\n")
	m.SetTownMapCatalog([]shared.MapCatalogItem{
		{Village: 2, Area: 5, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 200, YMin: 0, YMax: 200}}},
	})
	m.SetRobotStateDirectory(robotstate.NewMemoryStore(nil))
	driver := protocolSessionDriver{manager: m}

	repaired := driver.PrepareOnline(robotcap.Info{UID: 1, Level: 80, Village: 1, Area: 0, X: 10, Y: 10}, m.loadRobotConfig())
	if repaired.Village != 2 || repaired.Area != 5 {
		t.Fatalf("invalid stored area not repaired: %+v", repaired)
	}
	if repaired.X < 0 || repaired.X > 200 || repaired.Y < 0 || repaired.Y > 200 {
		t.Fatalf("repaired point outside map rectangle: %+v", repaired)
	}

	kept := driver.PrepareOnline(robotcap.Info{UID: 2, Level: 80, Village: 2, Area: 5, X: 11, Y: 12}, m.loadRobotConfig())
	if kept.Village != 2 || kept.Area != 5 || kept.X != 11 || kept.Y != 12 {
		t.Fatalf("valid stored area changed: %+v", kept)
	}
}

func TestProtocolPrepareOnlineKeepsFixedSpawnBehaviour(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = true\nspawn_village = 4\nspawn_area = 9\n")
	m.SetTownMapCatalog([]shared.MapCatalogItem{
		{Village: 4, Area: 9, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 50, YMin: 0, YMax: 50}}},
	})
	m.SetRobotStateDirectory(robotstate.NewMemoryStore(nil))
	driver := protocolSessionDriver{manager: m}

	out := driver.PrepareOnline(robotcap.Info{UID: 3, Level: 80, Village: 1, Area: 0, X: 10, Y: 10}, m.loadRobotConfig())
	if out.Village != 4 || out.Area != 9 {
		t.Fatalf("fixed spawn not applied: %+v", out)
	}
}
