package scheduler

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	"robot/internal/shared"
)

func twoAreaCatalog() []shared.MapCatalogItem {
	return []shared.MapCatalogItem{
		{Village: 2, Area: 5, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 200, YMin: 0, YMax: 200}}},
		{Village: 3, Area: 7, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 200, YMin: 0, YMax: 200}}},
	}
}

func TestProtocolPrepareOnlineRepairsInvalidStoredArea(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = false\nfollow_account =\n")
	m.SetTownMapCatalog(twoAreaCatalog())
	m.SetRobotStateDirectory(robotstate.NewMemoryStore([]robotcap.Info{
		{UID: 1, Village: 1, Area: 0},
		{UID: 2, Village: 2, Area: 5},
	}))
	driver := protocolSessionDriver{manager: m}

	repaired := driver.PrepareOnline(robotcap.Info{UID: 1, Level: 80, Village: 1, Area: 0, X: 10, Y: 10}, m.loadRobotConfig())
	if repaired.Village == 1 && repaired.Area == 0 {
		t.Fatalf("invalid stored area not repaired: %+v", repaired)
	}

	kept := driver.PrepareOnline(robotcap.Info{UID: 2, Level: 80, Village: 2, Area: 5, X: 11, Y: 12}, m.loadRobotConfig())
	if kept.Village != 2 || kept.Area != 5 || kept.X != 11 || kept.Y != 12 {
		t.Fatalf("valid stored area changed: %+v", kept)
	}
}

func TestProtocolPrepareOnlineSpreadsConcurrentRepairs(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = false\nfollow_account =\n")
	m.SetTownMapCatalog(twoAreaCatalog())
	m.SetRobotStateDirectory(robotstate.NewMemoryStore([]robotcap.Info{
		{UID: 1, Village: 1, Area: 0},
		{UID: 2, Village: 1, Area: 0},
	}))
	driver := protocolSessionDriver{manager: m}

	first := driver.PrepareOnline(robotcap.Info{UID: 1, Level: 80, Village: 1, Area: 0}, m.loadRobotConfig())
	second := driver.PrepareOnline(robotcap.Info{UID: 2, Level: 80, Village: 1, Area: 0}, m.loadRobotConfig())
	if first.Village == second.Village && first.Area == second.Area {
		t.Fatalf("repairs piled up in the same area: %d/%d and %d/%d", first.Village, first.Area, second.Village, second.Area)
	}
}

func TestProtocolPrepareOnlineRelocatesCrowdedArea(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = false\nfollow_account =\n")
	m.SetTownMapCatalog(twoAreaCatalog())
	robots := make([]robotcap.Info, 0, 31)
	for index := 0; index < 30; index++ {
		robots = append(robots, robotcap.Info{UID: 100 + index, Village: 2, Area: 5})
	}
	robots = append(robots, robotcap.Info{UID: 1, Village: 2, Area: 5})
	m.SetRobotStateDirectory(robotstate.NewMemoryStore(robots))
	driver := protocolSessionDriver{manager: m}

	moved := driver.PrepareOnline(robotcap.Info{UID: 1, Level: 80, Village: 2, Area: 5, X: 10, Y: 10}, m.loadRobotConfig())
	if moved.Village != 3 || moved.Area != 7 {
		t.Fatalf("crowded area not relocated: %+v", moved)
	}
}

func TestProtocolPrepareOnlineRelocatesOverDenseMirrorFamily(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[spawn]\nspawn_fixed = false\nfollow_account =\n")
	m.SetTownMapCatalog([]shared.MapCatalogItem{
		{Village: 2, Area: 5, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, Area: 6, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, Area: 7, Level: 0, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 1000, YMin: 0, YMax: 1000}}},
	})
	robots := make([]robotcap.Info, 0, 17)
	for index := 0; index < 16; index++ {
		robots = append(robots, robotcap.Info{UID: 100 + index, Village: 2, Area: 5 + index%2})
	}
	robots = append(robots, robotcap.Info{UID: 1, Village: 2, Area: 5})
	m.SetRobotStateDirectory(robotstate.NewMemoryStore(robots))
	driver := protocolSessionDriver{manager: m}

	moved := driver.PrepareOnline(robotcap.Info{UID: 1, Level: 80, Village: 2, Area: 5, X: 10, Y: 10}, m.loadRobotConfig())
	if moved.Village != 3 || moved.Area != 7 {
		t.Fatalf("over-dense mirror family not relocated: %+v", moved)
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
