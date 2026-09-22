package scheduler

import (
	"testing"

	"robot/internal/foundation/config"
	"robot/internal/shared"
)

func TestTownMapCatalogSnapshotOverridesRuntimeFile(t *testing.T) {
	m := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, nil)
	mapSnapshot := []shared.MapCatalogItem{{Village: 7, Area: 3, Rectangles: []shared.MapRectangle{{XMin: 10, XMax: 20, YMin: 30, YMax: 40}}}}
	m.SetTownMapCatalog(mapSnapshot)
	mapSnapshot[0].Rectangles[0].XMin = 999
	got := m.loadMapCatalog()
	if len(got) != 1 || got[0].Village != 7 || got[0].Rectangles[0].XMin != 10 {
		t.Fatalf("catalog snapshot = %+v", got)
	}
	got[0].Rectangles[0].XMin = 888
	if again := m.loadMapCatalog(); again[0].Rectangles[0].XMin != 10 {
		t.Fatalf("catalog snapshot was mutable: %+v", again)
	}
}
