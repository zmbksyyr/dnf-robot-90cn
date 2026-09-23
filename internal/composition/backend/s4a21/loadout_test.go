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
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestSQLiteLoadoutApplierReplacesEquipmentAndAvatarAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(7,'robot7');
INSERT INTO characters(character_id,account_id,name,job,level,delete_flag) VALUES(9,7,'bot',1,85,0);`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1, 3}
	rc.AvatarSlots = []int{0, 1}
	rc.MinAvatarSlots = 2
	rc.EquipIntensifyMin, rc.EquipIntensifyMax = 7, 7
	items := []shared.EquipmentCatalogItem{
		{ID: 1001, ItemType: 1, Level: 80, Durability: 45, UseJob: []int{1}},
		{ID: 1003, ItemType: 3, Level: 80, Durability: 55, UseJob: []int{1}},
		{ID: 2000, Name: "Hat", ItemType: 20, UseJob: []int{1}, Icon: "avatar/a.img"},
		{ID: 2001, Name: "Hair", ItemType: 21, UseJob: []int{1}, Icon: "avatar/b.img"},
	}
	applier := SQLiteLoadoutApplier{DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 }}
	info := robotcap.Info{Name: "bot", Job: 1, Level: 85}
	if err := applier.ApplyCharacterLoadout(context.Background(), "robot7", info); err != nil {
		t.Fatal(err)
	}
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
	if len(got) != 4 {
		t.Fatalf("equipped rows=%d slots=%v", len(got), got)
	}
	for slot, itemID := range map[int]int{0: 2000, 1: 2001, 12: 1001, 14: 1003} {
		core := got[slot]
		if len(core) != a21ItemCoreSize || int(binary.LittleEndian.Uint32(core[1:5])) != itemID {
			t.Fatalf("slot=%d core=%v", slot, core)
		}
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

func openLoadoutTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS accounts(account_id INTEGER PRIMARY KEY,m_id TEXT UNIQUE);
CREATE TABLE IF NOT EXISTS characters(character_id INTEGER PRIMARY KEY,account_id INTEGER,name TEXT,job INTEGER,level INTEGER,delete_flag INTEGER);
CREATE TABLE IF NOT EXISTS character_inventory_items(item_uid INTEGER PRIMARY KEY AUTOINCREMENT,character_id INTEGER,list_type INTEGER,slot_index INTEGER,item_core BLOB,created_at TEXT,updated_at TEXT,UNIQUE(character_id,list_type,slot_index));
CREATE TABLE IF NOT EXISTS character_avatar_detail(item_uid INTEGER PRIMARY KEY,owner_id INTEGER,character_id INTEGER,item_id INTEGER,expire_date INTEGER,clear_avatar_id INTEGER,jewel_socket BLOB,color1 INTEGER,color2 INTEGER,delete_date INTEGER);
CREATE TABLE IF NOT EXISTS character_avatar_uid_sequence(avatar_uid INTEGER PRIMARY KEY AUTOINCREMENT);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
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
	if equipmentCount < 8 || avatarCount < rc.MinAvatarSlots {
		t.Fatalf("real clone loadout equipment=%d avatar=%d", equipmentCount, avatarCount)
	}
}
