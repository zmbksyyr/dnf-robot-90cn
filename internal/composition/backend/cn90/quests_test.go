package cn90

import (
	"context"
	"path/filepath"
	"testing"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func newQuestTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`
CREATE TABLE dnf_characters(character_id TEXT PRIMARY KEY, account_id TEXT NOT NULL);
CREATE TABLE dnf_quests(character_id TEXT PRIMARY KEY, updated_at TEXT);
CREATE TABLE dnf_quest_states(character_id TEXT NOT NULL, state_group TEXT NOT NULL, quest_id BIGINT NOT NULL, status TEXT NOT NULL DEFAULT '', trigger_type TINYINT DEFAULT 0, progress_value BIGINT DEFAULT 0, reward_select_index BIGINT DEFAULT 0, multiplier BIGINT DEFAULT 0, state_updated_at TEXT, PRIMARY KEY (character_id, state_group, quest_id));
CREATE TABLE dnf_quest_state_extra(character_id TEXT NOT NULL, state_group TEXT NOT NULL, quest_id BIGINT NOT NULL, extra_key TEXT NOT NULL, extra_value TEXT NOT NULL, PRIMARY KEY (character_id, state_group, quest_id, extra_key));
INSERT INTO dnf_characters(character_id,account_id) VALUES ('7','robot17000007');`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMergeTownNeedQuestsPromotesActiveRequirements(t *testing.T) {
	gates := capabilitypvf.QuestGates{
		CompletedQuestIDs: []int{100, 200},
		ActiveQuestIDs:    []int{300, 3248, 400},
	}
	maps := []shared.MapCatalogItem{
		{Village: 38, Area: 3, NeedQuests: []int{3248, 500}},
		{Village: 38, Area: 6, NeedQuests: []int{500, 200}},
		{Village: 38, Area: 7, NeedQuests: []int{0}},
	}
	merged := mergeTownNeedQuests(gates, maps)
	completed := make(map[int]bool)
	for _, id := range merged.CompletedQuestIDs {
		completed[id] = true
	}
	for _, want := range []int{100, 200, 500, 3248} {
		if !completed[want] {
			t.Fatalf("completed set %v missing %d", merged.CompletedQuestIDs, want)
		}
	}
	for _, id := range merged.ActiveQuestIDs {
		if id == 3248 {
			t.Fatalf("active set still contains area-required quest: %v", merged.ActiveQuestIDs)
		}
	}
	if len(merged.ActiveQuestIDs) != 2 {
		t.Fatalf("active set = %v", merged.ActiveQuestIDs)
	}
}

func TestApplyQuestGatesSeedsAndIsIdempotent(t *testing.T) {
	path := newQuestTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	gates := capabilitypvf.QuestGates{
		CompletedQuestIDs: []int{100, 200, 300},
		ActiveQuestIDs:    []int{400, 500},
	}
	if err := applyQuestGates(context.Background(), db, "7", gates); err != nil {
		t.Fatal(err)
	}
	var completed, active int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_quest_states WHERE character_id='7' AND status='completed'`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM dnf_quest_states WHERE character_id='7' AND status='active'`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if completed != 3 || active != 2 {
		t.Fatalf("seeded completed=%d active=%d", completed, active)
	}
	// A second pass must detect the satisfied set and change nothing.
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	satisfied, err := questGatesSatisfied(context.Background(), tx, "7", gates)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if !satisfied {
		t.Fatal("seeded gate set is not reported as satisfied")
	}
}

func TestSeedRobotQuestGatesSkipsEmptyGates(t *testing.T) {
	path := newQuestTestDatabase(t)
	seeded, err := SeedRobotQuestGates(context.Background(), path, []robotcap.Info{{CID: 7}}, capabilitypvf.QuestGates{})
	if err != nil || seeded != 0 {
		t.Fatalf("seeded=%d err=%v", seeded, err)
	}
}
