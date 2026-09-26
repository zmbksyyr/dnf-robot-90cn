package scheduler

import (
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestPetProbabilityHitBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		percent int
		roll    int
		want    bool
	}{
		{name: "zero never hits", percent: 0, roll: 0, want: false},
		{name: "hundred always hits", percent: 100, roll: 99, want: true},
		{name: "below threshold hits", percent: 80, roll: 79, want: true},
		{name: "threshold misses", percent: 80, roll: 80, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := petProbabilityHit(test.percent, func(int) int { return test.roll }); got != test.want {
				t.Fatalf("petProbabilityHit(%d, %d) = %t, want %t", test.percent, test.roll, got, test.want)
			}
		})
	}
}

func TestOptionalPetWriteIsSkippedWithoutPersistencePort(t *testing.T) {
	manager := NewRobotManager(nil, nil, nil)
	rc := robotconfig.RuntimeConfig{PetEnabled: true, PetProbabilityPercent: 100}
	// The legacy creation boundary has no persistence port: the optional pet
	// write is skipped and must not fail the caller.
	if err := manager.petFromCatalog(661, rc, []shared.EquipmentCatalogItem{{ID: 63050, ItemType: 30}}); err != nil {
		t.Fatalf("petFromCatalog error=%v, want optional skip", err)
	}
}
