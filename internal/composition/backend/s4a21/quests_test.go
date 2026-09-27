package s4a21

import (
	"context"
	"path/filepath"
	"testing"

	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestSeedRobotQuestGatesReplacesQuestState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES
(9,7,'Alpha',1,0,60,0,0),
(10,7,'Beta',1,0,60,0,0)`); err != nil {
		t.Fatal(err)
	}
	// Robot 9 starts with unrelated quest rows that the seed must clear.
	if _, err := db.Exec(`INSERT INTO character_quest_completions(character_id,quest_id,completion_value) VALUES(9,7,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_active_quests(character_id,slot,quest_id,trigger_value,version,activation_id) VALUES(9,0,8,3,0,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	gates := capabilitypvf.QuestGates{
		CompletedQuestIDs: []int{1790, 1792},
		ActiveQuestIDs:    []int{500, 1791},
	}
	robots := []robotcap.Info{{UID: 17000009, CID: 9}, {UID: 17000010, CID: 10}}
	changed, err := SeedRobotQuestGates(context.Background(), path, robots, gates)
	if err != nil || changed != 2 {
		t.Fatalf("changed=%d err=%v", changed, err)
	}
	db = openLoadoutTestDB(t, path)
	defer db.Close()
	var completed, active, stale int
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_quest_completions WHERE character_id=9`).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_active_quests WHERE character_id=9`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT
(SELECT COUNT(*) FROM character_quest_completions WHERE character_id=9 AND quest_id=7) +
(SELECT COUNT(*) FROM character_active_quests WHERE character_id=9 AND quest_id=8)`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if completed != 2 || active != 2 || stale != 0 {
		t.Fatalf("completed=%d active=%d stale=%d", completed, active, stale)
	}
	var slot int
	if err := db.QueryRow(`SELECT slot FROM character_active_quests WHERE character_id=9 AND quest_id=1791`).Scan(&slot); err != nil {
		t.Fatal(err)
	}
	if slot != 1 {
		t.Fatalf("active gate slot=%d", slot)
	}

	changed, err = SeedRobotQuestGates(context.Background(), path, robots, gates)
	if err != nil || changed != 0 {
		t.Fatalf("second seed changed=%d err=%v", changed, err)
	}
}

func TestInitializeCharacterSeedsQuestGates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	rc := robotconfig.Default()
	rc.EquipSlots = []int{1}
	rc.AvatarSlots = nil
	rc.MinAvatarSlots = 0
	rc.PetEnabled = false
	rc.PreferEquipSets = false
	rc.PreferAvatarSets = false
	items := []shared.EquipmentCatalogItem{{ID: 1001, Name: "Sword", ItemType: 1, Level: 40, Durability: 45, UseJob: []int{1}}}
	adapter := SQLiteLoadoutApplier{
		DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 },
		QuestGates: capabilitypvf.QuestGates{CompletedQuestIDs: []int{1790}, ActiveQuestIDs: []int{1791}},
	}
	if _, err := adapter.InitializeCharacter(context.Background(), "robot7", robotcap.Info{Name: "Alpha", Job: 1}, 70, 0x12); err != nil {
		t.Fatal(err)
	}
	db = openLoadoutTestDB(t, path)
	defer db.Close()
	var value, trigger int
	if err := db.QueryRow(`SELECT completion_value FROM character_quest_completions WHERE character_id=9 AND quest_id=1790`).Scan(&value); err != nil || value != 1 {
		t.Fatalf("seeded completion value=%d err=%v", value, err)
	}
	if err := db.QueryRow(`SELECT trigger_value FROM character_active_quests WHERE character_id=9 AND quest_id=1791`).Scan(&trigger); err != nil || trigger != 0 {
		t.Fatalf("seeded active trigger=%d err=%v", trigger, err)
	}
}
