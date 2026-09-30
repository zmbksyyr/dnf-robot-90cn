package cn90

import (
	"context"
	"database/sql"
	"os"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
)

// TestLiveRedistributeRobotSpawns spreads an existing robot fleet over the
// configured village's eligible areas. It runs while the fleet is offline.
func TestLiveRedistributeRobotSpawns(t *testing.T) {
	if os.Getenv("CN90_LIVE_REDISTRIBUTE") == "" {
		t.Skip("set CN90_LIVE_REDISTRIBUTE=1 to redistribute live robot spawns")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
	if err != nil {
		t.Fatal(err)
	}
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	config := robotconfig.Default()
	config.SpawnFixed = true
	config.SpawnVillage = 38
	config.SpawnArea = 99
	config.SpawnXMin = 240
	config.SpawnXMax = 1800
	config.SpawnYMin = 180
	config.SpawnYMax = 460
	updated, err := RedistributeRobotSpawns(context.Background(), databasePath, pvfPath, "robot", config)
	if err != nil {
		t.Fatalf("redistribute: %v", err)
	}
	t.Logf("redistributed %d robot characters", updated)

	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	configureSQLitePool(db)
	rows, err := db.Query(`SELECT town_id, area_id, COUNT(*) FROM dnf_characters WHERE account_id LIKE 'robot%' AND delete_flag=0 GROUP BY town_id, area_id ORDER BY area_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var town, area, count int
		if err := rows.Scan(&town, &area, &count); err != nil {
			t.Fatal(err)
		}
		t.Logf("spawn distribution: town=%d area=%d robots=%d", town, area, count)
	}
	var distinct int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT pos_x || ':' || pos_y) FROM dnf_characters WHERE account_id LIKE 'robot%' AND delete_flag=0`).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	t.Logf("distinct positions: %d", distinct)
}
