package cn90

import (
	"context"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestRedistributeRobotSpawnsAcrossVillages(t *testing.T) {
	path := newEquipmentTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	statement := `INSERT INTO dnf_characters(character_id, account_id, slot, name, job, level, delete_flag, tutorial_completed) VALUES (?, ?, 0, '测试', '2', 70, 0, 1)`
	for index := 0; index < 8; index++ {
		if _, err := db.Exec(statement, 20+index, "robot1700000"+string(rune('1'+index))); err != nil {
			t.Fatal(err)
		}
	}
	maps := []shared.MapCatalogItem{
		{Village: 1, VillageName: "one", Area: 1, Use: true, XMin: 0, XMax: 100, YMin: 0, YMax: 100, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 2, VillageName: "two", Area: 1, Use: true, XMin: 0, XMax: 100, YMin: 0, YMax: 100, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
		{Village: 3, VillageName: "three", Area: 1, Use: true, XMin: 0, XMax: 100, YMin: 0, YMax: 100, Rectangles: []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}},
	}
	config := robotconfig.Default()
	updated, err := RedistributeRobotSpawnsWithMaps(context.Background(), path, "robot", config, maps, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if updated != 9 {
		t.Fatalf("updated characters = %d, want 9", updated)
	}
	rows, err := db.Query(`SELECT town_id, COUNT(*) FROM dnf_characters WHERE account_id LIKE 'robot%' AND delete_flag=0 GROUP BY town_id ORDER BY town_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[int]int{}
	for rows.Next() {
		var village, count int
		if err := rows.Scan(&village, &count); err != nil {
			t.Fatal(err)
		}
		counts[village] = count
	}
	for village, want := range map[int]int{1: 3, 2: 3, 3: 3} {
		if counts[village] != want {
			t.Fatalf("village %d has %d robots, want %d (all: %v)", village, counts[village], want, counts)
		}
	}
	var distinct int
	if err := db.QueryRow(`SELECT COUNT(DISTINCT pos_x || ':' || pos_y) FROM dnf_characters WHERE account_id LIKE 'robot%' AND delete_flag=0`).Scan(&distinct); err != nil {
		t.Fatal(err)
	}
	if distinct < 5 {
		t.Fatalf("distinct positions = %d, want spread points", distinct)
	}
}
