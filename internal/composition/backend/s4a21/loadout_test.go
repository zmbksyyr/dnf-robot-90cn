package s4a21

import (
	"context"
	"database/sql"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	equipmentcap "robot/internal/capability/equipment"
	capabilitypvf "robot/internal/capability/pvf"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
	"robot/internal/shared"
)

func TestSQLiteLoadoutApplierReplacesEquipmentAndAvatarAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	encodedName, err := charset.EncodeGBKString("机器人")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,level,delete_flag) VALUES(9,7,?,1,50,0)`, encodedName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_subtype0_fields(character_id) VALUES(9); INSERT INTO character_subtype1_fields(character_id) VALUES(9)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1, 3, 11, 12}
	rc.AvatarSlots = []int{0, 1}
	rc.MinAvatarSlots = 2
	rc.PetEnabled = true
	rc.PetProbabilityPercent = 100
	rc.PetArtifactEnabled = true
	rc.PetArtifactSlots = []int{31, 32, 33}
	rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots = 1, 1
	rc.EquipIntensifyMin, rc.EquipIntensifyMax = 7, 7
	items := []shared.EquipmentCatalogItem{
		{ID: 1001, ItemType: 1, Level: 40, Durability: 45, UseJob: []int{1}},
		{ID: 1003, ItemType: 3, Level: 40, Durability: 55, UseJob: []int{1}},
		{ID: 1011, ItemType: 11, Level: 60, Durability: 30, UseJob: []int{100}, ClientIncompatible: true},
		{ID: 1012, ItemType: 12, Level: 68, Durability: 30, UseJob: []int{100}, ClientIncompatible: true},
		{ID: 2000, Name: "Hat", ItemType: 20, UseJob: []int{1}, Icon: "avatar/a.img"},
		{ID: 2001, Name: "Hair", ItemType: 21, UseJob: []int{1}, Icon: "avatar/b.img"},
		{ID: 2011, Name: "Wrong job hat", ItemType: 20, UseJob: []int{11}, Icon: "avatar/c.img"},
		{ID: 3000, Name: "Creature", ItemType: 30, Icon: "creature/pet.img"},
		{ID: 3100, Name: "Creature artifact", ItemType: 31, Durability: 25, Path: "equipment/creature/artifact_red/hand.equ", Icon: "Item/creature/artifact_red.img"},
	}
	applier := SQLiteLoadoutApplier{DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 }}
	info := robotcap.Info{Name: "机器人", Job: 10, Level: 50}
	if err := applier.ApplyCharacterLoadout(context.Background(), "robot7", info); err != nil {
		t.Fatal(err)
	}
	db = openLoadoutTestDB(t, path)
	if _, err := db.Exec(`UPDATE character_inventory_items SET item_core=? WHERE character_id=9 AND list_type=3 AND slot_index=0`, a21ItemCore(a21ItemKindAvatar, items[4], 0, 99)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := applier.ApplyCharacterLoadout(context.Background(), "robot7", info); err != nil {
		t.Fatal(err)
	}
	db = openLoadoutTestDB(t, path)
	defer db.Close()
	rows, err := db.Query(`SELECT slot_index,item_core FROM character_inventory_items WHERE character_id=9 AND list_type=3 ORDER BY slot_index`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int][]byte{}
	for rows.Next() {
		var slot int
		var core []byte
		if err := rows.Scan(&slot, &core); err != nil {
			t.Fatal(err)
		}
		got[slot] = core
	}
	rows.Close()
	if len(got) != 8 {
		t.Fatalf("equipped rows=%d slots=%v", len(got), got)
	}
	for slot, itemID := range map[int]int{0: 2000, 1: 2001, 12: 1001, 14: 1003, 22: 1011, 23: 1012} {
		core := got[slot]
		if len(core) != a21ItemCoreSize || int(binary.LittleEndian.Uint32(core[1:5])) != itemID {
			t.Fatalf("slot=%d core=%v", slot, core)
		}
	}
	if core := got[25]; len(core) != a21ItemCoreSize || core[0] != a21ItemKindCreature || int(binary.LittleEndian.Uint32(core[1:5])) != 3000 {
		t.Fatalf("pet slot=%v", core)
	}
	if core := got[26]; len(core) != a21ItemCoreSize || core[0] != a21ItemKindArtifact || int(binary.LittleEndian.Uint32(core[1:5])) != 3100 || binary.LittleEndian.Uint16(core[10:12]) != 25 {
		t.Fatalf("pet artifact slot=%v", core)
	}
	var creatureUID int
	if err := db.QueryRow(`SELECT creature_key FROM character_creatures WHERE character_id=9 AND sort_order=0`).Scan(&creatureUID); err != nil {
		t.Fatal(err)
	}
	if creatureUID <= 0 || int(binary.LittleEndian.Uint32(got[25][5:9])) != creatureUID {
		t.Fatalf("creature uid=%d core=%v", creatureUID, got[25])
	}
	var creatureBuffer []byte
	if err := db.QueryRow(`SELECT creature_buffer FROM character_subtype0_fields WHERE character_id=9`).Scan(&creatureBuffer); err != nil {
		t.Fatal(err)
	}
	// creature_buffer is the name-tag window and stays cleared for robots.
	if len(creatureBuffer) != 8 || binary.LittleEndian.Uint32(creatureBuffer[:4]) != 0 || binary.LittleEndian.Uint32(creatureBuffer[4:8]) != 0 {
		t.Fatalf("creature buffer=%x", creatureBuffer)
	}
	var creatureStomach, creatureLevel, equippedCreatureLevel int
	if err := db.QueryRow(`SELECT field04,field_after_value FROM character_creatures WHERE character_id=9 AND sort_order=0`).Scan(&creatureStomach, &creatureLevel); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT equipped_creature_level FROM character_subtype1_fields WHERE character_id=9`).Scan(&equippedCreatureLevel); err != nil {
		t.Fatal(err)
	}
	if creatureStomach != 100 || creatureLevel != 1 || equippedCreatureLevel != 1 {
		t.Fatalf("creature stomach=%d level=%d subtype1_level=%d", creatureStomach, creatureLevel, equippedCreatureLevel)
	}
	if got[12][9] != 7 || binary.LittleEndian.Uint16(got[12][10:12]) != 45 {
		t.Fatalf("equipment defaults=%v", got[12][:13])
	}
	var detailCount, distinctUIDs int
	if err := db.QueryRow(`SELECT COUNT(*),COUNT(DISTINCT item_uid) FROM character_avatar_detail WHERE character_id=9`).Scan(&detailCount, &distinctUIDs); err != nil {
		t.Fatal(err)
	}
	if detailCount != 2 || distinctUIDs != 2 {
		t.Fatalf("avatar details=%d distinct=%d", detailCount, distinctUIDs)
	}
}

func TestSQLiteProfileAdapterPersistsPlannedLevel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',2,0,1,123,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	rc := robotconfig.Default()
	rc.LevelMin, rc.LevelMax = 50, 85
	adapter := SQLiteLoadoutApplier{
		DatabasePath: path, Config: rc, RandIntn: func(int) int { return 0 },
		StatTables: testStatTables(2), LevelThresholds: testLevelThresholds(),
	}
	info := robotcap.Info{UID: 17000007, Name: "Alpha", Job: 9, Grow: 2, Level: 70}

	actual, err := adapter.ApplyPlannedCharacterLevel(context.Background(), "robot7", info, 73)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Level != 73 || actual.Job != 2 || actual.Grow != 0 {
		t.Fatalf("planned profile=%+v", actual)
	}
	db = openLoadoutTestDB(t, path)
	var level, exp, statLevel, statHp int
	if err := db.QueryRow(`SELECT level,exp FROM characters WHERE character_id=9`).Scan(&level, &exp); err != nil {
		t.Fatal(err)
	}
	if level != 73 || exp != 7200 {
		t.Fatalf("persisted planned level=%d exp=%d", level, exp)
	}
	if err := db.QueryRow(`SELECT stat_level,stat_hp_max FROM character_subtype1_fields WHERE character_id=9`).Scan(&statLevel, &statHp); err != nil {
		t.Fatal(err)
	}
	// base 1000 + 72 base grow-row levels (10/level) + premium 9800.
	if statLevel != 100 || statHp != 1000+720+9800 {
		t.Fatalf("planned stats level=%d hp=%d", statLevel, statHp)
	}
	if _, err := db.Exec(`UPDATE characters SET level=1,exp=456 WHERE character_id=9`); err != nil {
		t.Fatal(err)
	}
	db.Close()
}

func TestInitializeCharacterWritesTransferAwakeningAndResetsSkills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_skills(character_id,skill_index,level) VALUES(9,42,1)`); err != nil {
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
		StatTables: testStatTables(1), LevelThresholds: testLevelThresholds(),
	}
	info := robotcap.Info{UID: 17000007, Name: "Alpha", Job: 1, Grow: 0, Level: 1}

	// first grow 2 (branch) + second grow 1 (awakening) = 0x12.
	actual, err := adapter.InitializeCharacter(context.Background(), "robot7", info, 70, 0x12)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Level != 70 || actual.Grow != 0x12 {
		t.Fatalf("initialized profile=%+v", actual)
	}
	db = openLoadoutTestDB(t, path)
	var level, grow, skills, statHp int
	if err := db.QueryRow(`SELECT level,grow_type FROM characters WHERE character_id=9`).Scan(&level, &grow); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_skills WHERE character_id=9`).Scan(&skills); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT stat_hp_max FROM character_subtype1_fields WHERE character_id=9`).Scan(&statHp); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if level != 70 || grow != 0x12 || skills != 0 {
		t.Fatalf("persisted level=%d grow=0x%02X skills=%d", level, grow, skills)
	}
	// base 1000 + 14*10 + 35*20 + 20*30 (awakening row) + premium 9800.
	if statHp != 1000+140+700+600+9800 {
		t.Fatalf("persisted stat_hp_max=%d", statHp)
	}

	if _, err := adapter.InitializeCharacter(context.Background(), "robot7", info, 70, 0x20); err == nil {
		t.Fatal("awakening without transfer was accepted")
	}
}

func TestInitializeCharacterKeepsSkillsWithoutTransferChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,1,0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_skills(character_id,skill_index,level) VALUES(9,42,1)`); err != nil {
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
		StatTables: testStatTables(1), LevelThresholds: testLevelThresholds(),
	}

	if _, err := adapter.InitializeCharacter(context.Background(), "robot7", robotcap.Info{Name: "Alpha", Job: 1}, 70, 0); err != nil {
		t.Fatal(err)
	}
	db = openLoadoutTestDB(t, path)
	defer db.Close()
	var skills int
	if err := db.QueryRow(`SELECT COUNT(*) FROM character_skills WHERE character_id=9`).Scan(&skills); err != nil {
		t.Fatal(err)
	}
	if skills != 1 {
		t.Fatalf("unchanged transfer reset skills=%d", skills)
	}
}

func TestResolveCharacterProfileMatchesWireName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	// GBK wire name that also decodes as Big5 if the heuristic is used.
	raw := []byte{0xD3, 0xC4, 0xB3, 0xC7, 0xC2, 0xC3, 0xBF, 0xCD}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,?,2,0,70,0,0)`, raw); err != nil {
		t.Fatal(err)
	}
	name := charset.DecodeWireName(raw)
	if name != "幽城旅客" {
		t.Fatalf("decoded wire name=%q", name)
	}
	_, _, resolved, err := resolveCharacterProfile(context.Background(), db, "robot7", robotcap.Info{Name: name})
	if err != nil || resolved.Job != 2 || resolved.Level != 70 {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
}

func TestExistingLoadoutCompatibleRejectsStaleLowLevelWeapon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,70,0,0)`); err != nil {
		t.Fatal(err)
	}
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1}
	rc.AvatarSlots = nil
	rc.MinAvatarSlots = 0
	rc.PetEnabled = false
	rc.PreferEquipSets = false
	rc.PreferAvatarSets = false
	items := []shared.EquipmentCatalogItem{
		{ID: 27850, ItemType: 1, Level: 1, UseJob: []int{1}},
		{ID: 1001, ItemType: 1, Level: 68, UseJob: []int{1}},
	}
	if _, err := db.Exec(`INSERT INTO character_inventory_items(character_id,list_type,slot_index,item_core) VALUES(9,3,12,?)`,
		a21ItemCore(a21ItemKindEquipment, items[0], 0, 0)); err != nil {
		t.Fatal(err)
	}
	info := robotcap.Info{Job: 1, Level: 70}
	byID := equipmentByID(items)
	best := equipmentcap.BestEquipmentLevels(items, info.Level, info.Job, rc)
	compatible, err := existingLoadoutCompatible(context.Background(), db, 9, info, byID, rc, best, 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if compatible {
		t.Fatal("stale level-1 weapon was treated as a compatible loadout")
	}

	// A weapon inside the selection window stays compatible.
	if _, err := db.Exec(`UPDATE character_inventory_items SET item_core=? WHERE character_id=9 AND list_type=3 AND slot_index=12`,
		a21ItemCore(a21ItemKindEquipment, items[1], 0, 0)); err != nil {
		t.Fatal(err)
	}
	compatible, err = existingLoadoutCompatible(context.Background(), db, 9, info, byID, rc, best, 1, 0, false)
	if err != nil || !compatible {
		t.Fatalf("in-window weapon incompatible: compatible=%t err=%v", compatible, err)
	}
}

func TestReconcileRobotLoadoutsReplacesStaleEquipment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot17000009')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,70,0,0)`); err != nil {
		t.Fatal(err)
	}
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1}
	rc.AvatarSlots = nil
	rc.MinAvatarSlots = 0
	rc.PetEnabled = false
	rc.PreferEquipSets = false
	rc.PreferAvatarSets = false
	items := []shared.EquipmentCatalogItem{
		{ID: 27850, ItemType: 1, Level: 1, Durability: 40, UseJob: []int{1}},
		{ID: 1001, ItemType: 1, Level: 68, Durability: 45, UseJob: []int{1}},
	}
	if _, err := db.Exec(`INSERT INTO character_inventory_items(character_id,list_type,slot_index,item_core) VALUES(9,3,12,?)`,
		a21ItemCore(a21ItemKindEquipment, items[0], 0, 0)); err != nil {
		t.Fatal(err)
	}
	db.Close()

	adapter := SQLiteLoadoutApplier{DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 }}
	robots := []robotcap.Info{{UID: 17000009, Name: "Alpha", Job: 1, Level: 70}}
	replaced, err := adapter.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil || replaced != 1 {
		t.Fatalf("replaced=%d err=%v", replaced, err)
	}
	db = openLoadoutTestDB(t, path)
	var core []byte
	if err := db.QueryRow(`SELECT item_core FROM character_inventory_items WHERE character_id=9 AND list_type=3 AND slot_index=12`).Scan(&core); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if len(core) != a21ItemCoreSize || int(binary.LittleEndian.Uint32(core[1:5])) != 1001 {
		t.Fatalf("reconciled weapon core=%X", core)
	}

	replaced, err = adapter.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil || replaced != 0 {
		t.Fatalf("second reconcile replaced=%d err=%v", replaced, err)
	}

	// A core whose kind byte no longer matches its slot must be repaired by the
	// reconcile instead of being left for the startup compliance deletion.
	db = openLoadoutTestDB(t, path)
	corrupt := a21ItemCore(a21ItemKindEquipment, items[1], 0, 0)
	corrupt[0] = a21ItemKindAvatar
	if _, err := db.Exec(`UPDATE character_inventory_items SET item_core=? WHERE character_id=9 AND list_type=3 AND slot_index=12`, corrupt); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	replaced, err = adapter.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil || replaced != 1 {
		t.Fatalf("wrong-kind reconcile replaced=%d err=%v", replaced, err)
	}
	replaced, err = adapter.ReconcileRobotLoadouts(context.Background(), "robot", robots)
	if err != nil || replaced != 0 {
		t.Fatalf("wrong-kind second reconcile replaced=%d err=%v", replaced, err)
	}
}

// A stored pet must satisfy the configured window instead of matching a freshly
// rolled pet: exact matching rewrote the whole loadout on every startup.
func TestReconcileRobotLoadoutsKeepsStoredPet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot17000009')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO characters(character_id,account_id,name,job,grow_type,level,exp,delete_flag) VALUES(9,7,'Alpha',1,0,70,0,0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	rc := robotconfig.Default()
	rc.EquipSlots = []int{1}
	rc.AvatarSlots = nil
	rc.MinAvatarSlots = 0
	rc.PreferEquipSets, rc.PreferAvatarSets = false, false
	rc.PetEnabled, rc.PetProbabilityPercent = true, 100
	rc.PetArtifactEnabled = true
	rc.PetArtifactSlots = []int{31}
	rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots = 1, 1
	items := []shared.EquipmentCatalogItem{
		{ID: 1001, ItemType: 1, Level: 40, Durability: 45, UseJob: []int{1}},
		{ID: 3000, Name: "Creature", ItemType: 30, Icon: "creature/pet.img"},
		{ID: 3100, Name: "Artifact", ItemType: 31, Durability: 20, Path: "equipment/creature/artifact_red/hand.equ", Icon: "Item/creature/artifact_red.img"},
	}
	adapter := SQLiteLoadoutApplier{
		DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 },
		StatTables: testStatTables(1), LevelThresholds: testLevelThresholds(),
	}
	info := robotcap.Info{UID: 17000009, Name: "Alpha", Job: 1, Level: 70}
	if _, err := adapter.InitializeCharacter(context.Background(), "robot17000009", info, 70, 0); err != nil {
		t.Fatal(err)
	}
	replaced, err := adapter.ReconcileRobotLoadouts(context.Background(), "robot", []robotcap.Info{info})
	if err != nil || replaced != 0 {
		t.Fatalf("pet loadout reconcile replaced=%d err=%v, want 0", replaced, err)
	}
}

func openLoadoutTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS accounts(account_id INTEGER PRIMARY KEY,m_id TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS characters(character_id INTEGER PRIMARY KEY,account_id INTEGER,name TEXT,job INTEGER,grow_type INTEGER NOT NULL DEFAULT 0,level INTEGER,exp INTEGER NOT NULL DEFAULT 0,town_id INTEGER NOT NULL DEFAULT 1,area_id INTEGER NOT NULL DEFAULT 0,pos_x INTEGER NOT NULL DEFAULT 0,pos_y INTEGER NOT NULL DEFAULT 0,slot_index INTEGER NOT NULL DEFAULT 0,delete_flag INTEGER,updated_at TEXT,FOREIGN KEY(account_id) REFERENCES accounts(account_id) ON DELETE CASCADE);
CREATE UNIQUE INDEX IF NOT EXISTS idx_characters_name_unique ON characters(name);
CREATE TABLE IF NOT EXISTS character_inventory_items(item_uid INTEGER PRIMARY KEY AUTOINCREMENT,character_id INTEGER,list_type INTEGER,slot_index INTEGER,item_core BLOB,created_at TEXT,updated_at TEXT,UNIQUE(character_id,list_type,slot_index));
CREATE TABLE IF NOT EXISTS character_avatar_detail(item_uid INTEGER PRIMARY KEY,owner_id INTEGER,character_id INTEGER,item_id INTEGER,expire_date INTEGER,clear_avatar_id INTEGER,jewel_socket BLOB,color1 INTEGER,color2 INTEGER,delete_date INTEGER);
CREATE TABLE IF NOT EXISTS character_avatar_uid_sequence(avatar_uid INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS character_creatures(character_id INTEGER,sort_order INTEGER,creature_key INTEGER,field04 INTEGER,mode_flag INTEGER,progress_value INTEGER,mode1_field0a INTEGER,mode1_field0b INTEGER,field_after_value INTEGER,creature_text BLOB,tail_flag INTEGER,extra_json TEXT,PRIMARY KEY(character_id,sort_order));
CREATE TABLE IF NOT EXISTS character_creature_uid_sequence(creature_uid INTEGER PRIMARY KEY AUTOINCREMENT);
CREATE TABLE IF NOT EXISTS character_subtype0_fields(character_id INTEGER PRIMARY KEY,creature_buffer BLOB,pet_display_flag INTEGER);
CREATE TABLE IF NOT EXISTS character_subtype1_fields(character_id INTEGER PRIMARY KEY,stat_hp_max INTEGER NOT NULL DEFAULT 0,stat_mp_max INTEGER NOT NULL DEFAULT 0,stat_physical_attack INTEGER NOT NULL DEFAULT 0,stat_physical_defense INTEGER NOT NULL DEFAULT 0,stat_magical_attack INTEGER NOT NULL DEFAULT 0,stat_magical_defense INTEGER NOT NULL DEFAULT 0,stat_fire_resistance INTEGER NOT NULL DEFAULT 0,stat_water_resistance INTEGER NOT NULL DEFAULT 0,stat_dark_resistance INTEGER NOT NULL DEFAULT 0,stat_light_resistance INTEGER NOT NULL DEFAULT 0,stat_inventory_limit INTEGER NOT NULL DEFAULT 0,stat_hp_regen_speed INTEGER NOT NULL DEFAULT 0,stat_mp_regen_speed INTEGER NOT NULL DEFAULT 0,stat_move_speed INTEGER NOT NULL DEFAULT 0,stat_attack_speed INTEGER NOT NULL DEFAULT 0,stat_cast_speed INTEGER NOT NULL DEFAULT 0,stat_hit_recovery INTEGER NOT NULL DEFAULT 0,stat_jump_power INTEGER NOT NULL DEFAULT 0,stat_weight INTEGER NOT NULL DEFAULT 0,stat_level INTEGER NOT NULL DEFAULT 0,equipped_creature_level INTEGER);
CREATE TABLE IF NOT EXISTS character_skills(character_id INTEGER,skill_index INTEGER,level INTEGER,PRIMARY KEY(character_id,skill_index));
CREATE TABLE IF NOT EXISTS character_quest_completions(character_id INTEGER NOT NULL,quest_id INTEGER NOT NULL,completion_value INTEGER NOT NULL,PRIMARY KEY(character_id,quest_id));
CREATE TABLE IF NOT EXISTS character_active_quests(character_id INTEGER NOT NULL,slot INTEGER NOT NULL,quest_id INTEGER NOT NULL,trigger_value INTEGER NOT NULL DEFAULT 0,version INTEGER NOT NULL DEFAULT 0,activation_id TEXT NOT NULL,PRIMARY KEY(character_id,slot),UNIQUE(character_id,quest_id),UNIQUE(character_id,activation_id));`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

// testStatTables builds a complete synthetic .chr growth graph for the given
// jobs. HP MAX grows by 10/level up to 14, 20/level up to 49 and 30..40/level
// afterwards, so progression writes are observable in the stat columns.
func testStatTables(jobs ...int) map[int]capabilitypvf.CharacterStatTables {
	table := capabilitypvf.CharacterStatTables{
		Base: capabilitypvf.StatVector{
			HpMax: 1000, MpMax: 500, PhysAtk: 10, PhysDef: 10, MagAtk: 5, MagDef: 5,
			InventoryLimit: 1000, MoveSpeed: 1000, AttackSpeed: 1000, CastSpeed: 1000,
			HitRecovery: 1000, JumpPower: 1000, Weight: 1000,
		},
	}
	table.GrowtypeSet[1] = true
	table.Growtype[1] = capabilitypvf.StatVector{HpMax: 10, MpMax: 5, PhysAtk: 1}
	for n := 2; n <= 6; n++ {
		table.GrowtypeSet[n] = true
		table.Growtype[n] = capabilitypvf.StatVector{HpMax: 20, MpMax: 10, PhysAtk: 2}
		table.AwakenSet[n][1] = true
		table.Awakening[n][1] = capabilitypvf.StatVector{HpMax: 30, MpMax: 15, PhysAtk: 3}
		table.AwakenSet[n][2] = true
		table.Awakening[n][2] = capabilitypvf.StatVector{HpMax: 40, MpMax: 20, PhysAtk: 4}
	}
	result := make(map[int]capabilitypvf.CharacterStatTables, len(jobs))
	for _, job := range jobs {
		result[job] = table
	}
	return result
}

func testLevelThresholds() []int {
	thresholds := make([]int, 85)
	for index := range thresholds {
		thresholds[index] = (index + 1) * 100
	}
	return thresholds
}

func TestLiveSQLiteLoadoutAgainstDatabaseClone(t *testing.T) {
	databasePath := os.Getenv("S4A21_TEST_DB")
	pvfPath := os.Getenv("S4A21_TEST_PVF")
	if databasePath == "" || pvfPath == "" {
		t.Skip("S4A21_TEST_DB and S4A21_TEST_PVF are required")
	}
	clone := filepath.Join(t.TempDir(), "inventory.db")
	source, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	quotedClone := strings.ReplaceAll(filepath.ToSlash(clone), "'", "''")
	if _, err := source.Exec(`VACUUM INTO '` + quotedClone + `'`); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()
	db, err := sql.Open("sqlite", clone)
	if err != nil {
		t.Fatal(err)
	}
	var account, name string
	var characterID, job, level int
	if err := db.QueryRow(`SELECT a.m_id,c.character_id,c.name,c.job,c.level FROM accounts a JOIN characters c ON c.account_id=a.account_id WHERE a.m_id LIKE 'robot%' AND c.delete_flag=0 ORDER BY c.level DESC LIMIT 1`).Scan(&account, &characterID, &name, &job, &level); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	equipment, _, err := ReadItemCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	rc := robotconfig.Default()
	applier := SQLiteLoadoutApplier{DatabasePath: clone, Config: rc, Equipment: equipment, RandIntn: func(int) int { return 0 }}
	if level < 85 {
		level = 85
	}
	jobs := equipmentcap.FilterEquipmentSupportedJobs([]int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, equipment, level, rc)
	if len(jobs) == 0 {
		t.Fatal("live catalog has no supported equipment job")
	}
	typeCounts := map[int]int{}
	for _, item := range equipment {
		typeCounts[item.ItemType]++
	}
	if typeCounts[1] == 0 || typeCounts[3] == 0 || typeCounts[20] == 0 {
		t.Fatalf("live catalog is missing ordinary equipment or avatars: %v", typeCounts)
	}
	job = jobs[0]
	db, err = sql.Open("sqlite", clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM character_inventory_items WHERE character_id=? AND list_type=3 AND slot_index BETWEEN 0 AND 24`, characterID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	if err := applier.ApplyCharacterLoadout(context.Background(), account, robotcap.Info{Name: name, Job: job, Level: level}); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", clone)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var equipmentCount, avatarCount int
	if err := db.QueryRow(`SELECT COUNT(CASE WHEN slot_index BETWEEN 12 AND 23 THEN 1 END), COUNT(CASE WHEN slot_index BETWEEN 0 AND 9 THEN 1 END) FROM character_inventory_items WHERE character_id=? AND list_type=3`, characterID).Scan(&equipmentCount, &avatarCount); err != nil {
		t.Fatal(err)
	}
	if equipmentCount < 1 || avatarCount < rc.MinAvatarSlots {
		t.Fatalf("real clone loadout equipment=%d avatar=%d", equipmentCount, avatarCount)
	}
	if typeCounts[30] > 0 {
		var creatureCount, artifactCount int
		if err := db.QueryRow(`SELECT COUNT(*) FROM character_creatures WHERE character_id=?`, characterID).Scan(&creatureCount); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow(`SELECT COUNT(*) FROM character_inventory_items WHERE character_id=? AND list_type=3 AND slot_index BETWEEN 25 AND 27`, characterID).Scan(&artifactCount); err != nil {
			t.Fatal(err)
		}
		if creatureCount != 1 {
			t.Fatalf("real clone creature count=%d", creatureCount)
		}
		if typeCounts[31]+typeCounts[32]+typeCounts[33] > 0 && artifactCount < 1 {
			t.Fatalf("real clone pet artifact count=%d", artifactCount)
		}
	}
}
