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
(10,7,'Beta',1,34,50,0,0),
(11,7,'Gamma',9,0,50,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_skills(character_id,skill_index,level) VALUES(9,1,1),(10,1,1),(11,1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	robots := []robotcap.Info{
		{UID: 17000009, CID: 9, Name: "Alpha", Job: 1, Grow: 0},
		{UID: 17000010, CID: 10, Name: "Beta", Job: 1, Grow: 0x22},
		{UID: 17000011, CID: 11, Name: "Gamma", Job: 9, Grow: 0},
	}
	changed, err := ReconcileRobotGrowth(context.Background(), path, robots, []int{1, 2}, map[int][]int{1: {2, 3}}, testStatTables(1), false, func(int) int { return 0 })
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	// branch 2 + first awakening stage.
	if robots[0].Grow != 0x12 {
		t.Fatalf("reconciled grow=0x%02X", robots[0].Grow)
	}
	// Beta already matches the configured stage; without reconcile_awakening the
	// remaining stage-0 robots stay untouched.
	if robots[1].Grow != 0x22 || robots[2].Grow != 0 {
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
	if grow9 != 0x12 || grow10 != 0x22 || grow11 != 0 {
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
	var statHp9 int
	if err := db.QueryRow(`SELECT stat_hp_max FROM character_subtype1_fields WHERE character_id=9`).Scan(&statHp9); err != nil {
		t.Fatal(err)
	}
	// base 1000 + 14*10 + 35*20 + premium 9800 (level 50 stays in the transfer segment).
	if statHp9 != 1000+140+700+9800 {
		t.Fatalf("reconciled stat_hp_max=%d", statHp9)
	}

	// A second reconcile is a no-op.
	changed, err = ReconcileRobotGrowth(context.Background(), path, robots, []int{1}, map[int][]int{1: {2, 3}}, testStatTables(1), false, func(int) int { return 0 })
	if err != nil || changed != 0 {
		t.Fatalf("second reconcile changed=%d err=%v", changed, err)
	}
}

func TestReconcileRobotGrowthAwakensTransferredRobotsWhenEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES
(9,7,'Alpha',1,2,70,0,0),
(10,7,'Beta',1,34,70,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_skills(character_id,skill_index,level) VALUES(9,1,1),(10,1,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	robots := []robotcap.Info{
		{UID: 17000009, CID: 9, Name: "Alpha", Job: 1, Grow: 2},
		{UID: 17000010, CID: 10, Name: "Beta", Job: 1, Grow: 0x22},
	}
	// Only stage 2 is configured; stage 0 is not, so Alpha must be awakened.
	changed, err := ReconcileRobotGrowth(context.Background(), path, robots, []int{0, 2}, map[int][]int{1: {2, 3}}, testStatTables(1), true, func(int) int { return 0 })
	if err != nil || changed != 1 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	if robots[0].Grow != 0x22 || robots[1].Grow != 0x22 {
		t.Fatalf("awakened grows=%v", []int{robots[0].Grow, robots[1].Grow})
	}

	db = openLoadoutTestDB(t, path)
	defer db.Close()
	var grow9, grow10, skills9, skills10, statHp9 int
	if err := db.QueryRow(`SELECT grow_type FROM characters WHERE character_id=9`).Scan(&grow9); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT grow_type FROM characters WHERE character_id=10`).Scan(&grow10); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_skills WHERE character_id=9`).Scan(&skills9); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_skills WHERE character_id=10`).Scan(&skills10); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT stat_hp_max FROM character_subtype1_fields WHERE character_id=9`).Scan(&statHp9); err != nil {
		t.Fatal(err)
	}
	if grow9 != 0x22 || grow10 != 0x22 || skills9 != 0 || skills10 != 1 {
		t.Fatalf("persisted grows=%d/%d skills=%d/%d", grow9, grow10, skills9, skills10)
	}
	// base 1000 + 14*10 + 35*20 + 20*40 (stage 2 row) + premium 9800.
	if statHp9 != 1000+140+700+800+9800 {
		t.Fatalf("awakened stat_hp_max=%d", statHp9)
	}
}

func TestReconciledGrowMatrix(t *testing.T) {
	tables := testStatTables(1, 9)
	cases := []struct {
		name       string
		job, grow  int
		growTypes  []int
		jobGrows   map[int][]int
		reconcile  bool
		want       int
		wantChange bool
	}{
		{name: "untransferred gets a branch", job: 1, grow: 0, growTypes: []int{1}, jobGrows: map[int][]int{1: {2}}, want: 0x12, wantChange: true},
		{name: "branch-less job stays", job: 9, grow: 0, growTypes: []int{0}, want: 0, wantChange: false},
		{name: "unawakened gets a stage", job: 1, grow: 2, growTypes: []int{2}, jobGrows: map[int][]int{1: {2}}, reconcile: true, want: 0x22, wantChange: true},
		{name: "awakening disabled keeps stage 0", job: 1, grow: 2, growTypes: []int{2}, jobGrows: map[int][]int{1: {2}}, reconcile: false, want: 0, wantChange: false},
		{name: "already awakened stays", job: 1, grow: 0x22, growTypes: []int{0, 2}, jobGrows: map[int][]int{1: {2}}, reconcile: true, want: 0, wantChange: false},
		{name: "stage not configured stays", job: 1, grow: 0x12, growTypes: []int{0}, jobGrows: map[int][]int{1: {2}}, reconcile: true, want: 0, wantChange: false},
		{name: "unreleased branch is repicked", job: 1, grow: 0x1F, growTypes: []int{2}, jobGrows: map[int][]int{1: {2}}, reconcile: true, want: 0x22, wantChange: true},
		{name: "awakening without transfer is repicked", job: 1, grow: 0x20, growTypes: []int{2}, jobGrows: map[int][]int{1: {2}}, reconcile: true, want: 0x22, wantChange: true},
		{name: "branch-less corrupt value resets", job: 9, grow: 0x12, growTypes: []int{0}, want: 0, wantChange: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			grow, changed := reconciledGrow(testCase.job, testCase.grow, testCase.growTypes, testCase.jobGrows[testCase.job], tables, testCase.reconcile, func(int) int { return 0 })
			if changed != testCase.wantChange || grow != testCase.want {
				t.Fatalf("grow=0x%02X changed=%t want=0x%02X/%t", grow, changed, testCase.want, testCase.wantChange)
			}
		})
	}
}

func TestReconcileRobotGrowthSkipsWithoutBranches(t *testing.T) {
	changed, err := ReconcileRobotGrowth(context.Background(), "", nil, []int{1}, nil, nil, false, nil)
	if err != nil || changed != 0 {
		t.Fatalf("empty reconcile changed=%d err=%v", changed, err)
	}
}
