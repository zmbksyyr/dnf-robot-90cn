package s4a21

import (
	"context"
	"path/filepath"
	"testing"

	robotcap "robot/internal/capability/robot"
)

func TestReconcileRobotGrowthFillsUntransferredRobots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES
(9,7,'Alpha',1,0,50,0,0),
(10,7,'Beta',1,17,50,0,0),
(11,7,'Gamma',9,0,50,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_skills(character_id,skill_index,level) VALUES(9,1,1),(10,1,1),(11,1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	robots := []robotcap.Info{
		{UID: 17000009, CID: 9, Name: "Alpha", Job: 1, Grow: 0},
		{UID: 17000010, CID: 10, Name: "Beta", Job: 1, Grow: 0x11},
		{UID: 17000011, CID: 11, Name: "Gamma", Job: 9, Grow: 0},
	}
	changed, err := ReconcileRobotGrowth(context.Background(), path, robots, []int{1}, map[int][]int{1: {2, 3}}, func(int) int { return 0 })
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	// branch 2 + first awakening stage.
	if robots[0].Grow != 0x12 {
		t.Fatalf("reconciled grow=0x%02X", robots[0].Grow)
	}
	if robots[1].Grow != 0x11 || robots[2].Grow != 0 {
		t.Fatalf("untouched grows=%v", []int{robots[1].Grow, robots[2].Grow})
	}

	db = openLoadoutTestDB(t, path)
	defer db.Close()
	var grow9, grow10, grow11 int
	if err := db.QueryRow(`SELECT grow_type FROM characters WHERE character_id=9`).Scan(&grow9); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT grow_type FROM characters WHERE character_id=10`).Scan(&grow10); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT grow_type FROM characters WHERE character_id=11`).Scan(&grow11); err != nil {
		t.Fatal(err)
	}
	if grow9 != 0x12 || grow10 != 0x11 || grow11 != 0 {
		t.Fatalf("persisted grows=%d/%d/%d", grow9, grow10, grow11)
	}
	var skills9, skills10, skills11 int
	for characterID, destination := range map[int]*int{9: &skills9, 10: &skills10, 11: &skills11} {
		if err := db.QueryRow(`SELECT COUNT(*) FROM character_skills WHERE character_id=?`, characterID).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if skills9 != 0 || skills10 != 1 || skills11 != 1 {
		t.Fatalf("skills reset=%d/%d/%d", skills9, skills10, skills11)
	}

	// A second reconcile is a no-op.
	changed, err = ReconcileRobotGrowth(context.Background(), path, robots, []int{1}, map[int][]int{1: {2, 3}}, func(int) int { return 0 })
	if err != nil || changed != 0 {
		t.Fatalf("second reconcile changed=%d err=%v", changed, err)
	}
}

func TestReconcileRobotGrowthSkipsWithoutBranches(t *testing.T) {
	changed, err := ReconcileRobotGrowth(context.Background(), "", nil, []int{1}, nil, nil)
	if err != nil || changed != 0 {
		t.Fatalf("empty reconcile changed=%d err=%v", changed, err)
	}
}
