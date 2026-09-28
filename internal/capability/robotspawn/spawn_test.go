package robotspawn

import (
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type spawnTestEnv struct{}

func (spawnTestEnv) FollowAccountVillage(string) (int, bool, error) { return 0, false, nil }
func (spawnTestEnv) RandBetween(min, _ int) int                     { return min }
func (spawnTestEnv) RandIntn(int) int                               { return 0 }

func TestConfiguredVillageUsesCurrentPVFCatalogWithoutFixedUpperBound(t *testing.T) {
	info := robotcap.Info{Village: 1, Area: 0, Level: 85}
	rc := robotconfig.Default()
	rc.SpawnFixed = true
	rc.SpawnVillage = 26
	maps := []shared.MapCatalogItem{{Village: 26, Area: 3, Use: true, Rectangles: []shared.MapRectangle{{XMin: 100, XMax: 120, YMin: 200, YMax: 220}}}}
	ApplyConfiguredLocation(spawnTestEnv{}, &info, rc, maps)
	if info.Village != 26 || info.Area != 3 {
		t.Fatalf("configured position=%+v, want PVF village 26 area 3", info)
	}
}

func TestMissingConfiguredVillageKeepsExistingValidPosition(t *testing.T) {
	info := robotcap.Info{Village: 5, Area: 2, X: 10, Y: 20, Level: 85}
	rc := robotconfig.Default()
	rc.SpawnFixed = true
	rc.SpawnVillage = 99
	ApplyConfiguredLocation(spawnTestEnv{}, &info, rc, []shared.MapCatalogItem{{Village: 5, Area: 2, Use: true}})
	if info.Village != 5 || info.Area != 2 || info.X != 10 || info.Y != 20 {
		t.Fatalf("missing configured village changed position: %+v", info)
	}
}

func TestHasUsableMap(t *testing.T) {
	maps := []shared.MapCatalogItem{
		{Village: 1, Area: 0, Use: false, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 10, YMin: 0, YMax: 10}}},
		{Village: 2, Area: 5, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 10, YMin: 0, YMax: 10}}},
		{Village: 3, Area: 1, Use: true, XMin: 100, XMax: 200, YMin: 100, YMax: 200},
	}
	if HasUsableMap(maps, 1, 0) {
		t.Fatal("disabled map must not count as usable")
	}
	if !HasUsableMap(maps, 2, 5) {
		t.Fatal("map with rectangles must count as usable")
	}
	if !HasUsableMap(maps, 3, 1) {
		t.Fatal("map with bounding box must count as usable")
	}
	if HasUsableMap(maps, 9, 9) {
		t.Fatal("missing village/area must not count as usable")
	}
}

func TestNormalMapsFiltersPvPAndDisabled(t *testing.T) {
	no := false
	yes := true
	maps := []shared.MapCatalogItem{
		{Village: 1, Area: 0, Use: false, NormalEligible: &yes, StoreEligible: &yes},
		{Village: 2, Area: 0, Use: true, NormalEligible: &no, StoreEligible: &no},
		{Village: 3, Area: 0, Use: true, NormalEligible: &yes, StoreEligible: &yes},
		// Non-store town kinds are still normal towns and stay eligible.
		{Village: 4, Area: 0, Use: true, NormalEligible: &yes, StoreEligible: &no},
		{Village: 5, Area: 0, Use: true, NormalEligible: &yes},
	}
	out := NormalMaps(maps)
	if len(out) != 3 || out[0].Village != 3 || out[1].Village != 4 || out[2].Village != 5 {
		t.Fatalf("NormalMaps got %+v", out)
	}
}
