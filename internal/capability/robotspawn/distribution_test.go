package robotspawn

import (
	"testing"

	"robot/internal/shared"
)

type zeroRandom struct{}

func (zeroRandom) RandBetween(min, max int) int { return min }

func TestBalancedFamilyLocationDoesNotMultiplyMirrorInstances(t *testing.T) {	maps := []shared.MapCatalogItem{
		{Village: 3, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 500, XMax: 600, YMin: 0, YMax: 100}}},
	}
	// Ten mirror instances of one map in village 2 (identical geometry).
	for area := 1; area <= 10; area++ {
		maps = append(maps, shared.MapCatalogItem{
			Village: 2, Area: area, Use: true,
			Rectangles: []shared.MapRectangle{{XMin: 1000, XMax: 1100, YMin: 0, YMax: 100}},
		})
	}

	var locations []shared.MapLocation
	for index := 0; index < 6; index++ {
		target, ok := BalancedFamilyLocation(zeroRandom{}, maps, 80, locations)
		if !ok {
			t.Fatal("BalancedFamilyLocation returned no target")
		}
		locations = append(locations, shared.MapLocation{Village: target.Map.Village, Area: target.Map.Area, X: target.X, Y: target.Y})
	}
	instances := map[int]bool{}
	village2 := 0
	for _, location := range locations {
		if location.Village == 2 {
			village2++
			instances[location.Area] = true
		}
	}
	if village2 > 3 {
		t.Fatalf("mirror family took %d/6 robots; mirror instances must share capacity", village2)
	}
	if len(instances) != village2 {
		t.Fatalf("mirror instances collided: areas=%v", instances)
	}
}

func TestCrowdedCountsMirrorInstancesAsOneFamily(t *testing.T) {
	maps := []shared.MapCatalogItem{
		{Village: 2, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, Area: 2, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, Area: 1, Use: true, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 1000, YMin: 0, YMax: 1000}}},
	}
	var locations []shared.MapLocation
	for index := 0; index < 8; index++ {
		locations = append(locations, shared.MapLocation{Village: 2, Area: 1 + index%2})
	}
	if !Crowded(maps, locations, 2, 1, 4, 2) {
		t.Fatal("mirror family with an empty other village must count as crowded")
	}
	if Crowded(maps, nil, 2, 1, 4, 2) {
		t.Fatal("empty directory must not be crowded")
	}
	if Crowded(maps, locations, 9, 9, 4, 2) {
		t.Fatal("unknown area must not be crowded")
	}
}
