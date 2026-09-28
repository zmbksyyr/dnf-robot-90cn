package store

import (
	"testing"

	"robot/internal/shared"
)

func TestClaimForStoreInAreaPrefersRequestedArea(t *testing.T) {
	configDir := t.TempDir()
	writeStoreMapCatalog(t, configDir, []shared.MapCatalogItem{
		{Village: 3, Area: 0, XMin: 1, XMax: 400, YMin: 200, YMax: 440, Use: true},
		{Village: 4, Area: 0, XMin: 1, XMax: 400, YMin: 200, YMax: 440, Use: true},
	})
	coordinator := newTestPointCoordinator(configDir, nil)
	pos, ok := coordinator.ClaimForStoreInAreaWhere(1001, 120, 3, 0, nil)
	if !ok || pos.Village != 3 || pos.Area != 0 {
		t.Fatalf("preferred claim=%+v ok=%t", pos, ok)
	}
	if _, ok := coordinator.ClaimForStoreInAreaWhere(1002, 120, 9, 9, nil); ok {
		t.Fatal("claim in an area with no points unexpectedly succeeded")
	}
	if _, ok := coordinator.ClaimForStoreWhere(1003, 120, nil); !ok {
		t.Fatal("global fallback claim failed")
	}
}
