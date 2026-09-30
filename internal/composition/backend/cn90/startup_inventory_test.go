package cn90

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestStartupInventorySkipsCandidateChangedAfterScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`
INSERT INTO accounts(account_id,m_id) VALUES (8,'robot17000008');
INSERT INTO characters(character_id,account_id,name,job,grow_type,level,delete_flag) VALUES
 (80,8,'Reusable',1,0,5,0),(81,8,'NewSibling',1,0,5,0);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	db2 := openLoadoutTestDB(t, path)
	defer db2.Close()
	conn, err := db2.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	purger := SQLiteStartupInventory{AccountPrefix: "robot"}

	// The database gained a second character after the scan: the candidate is
	// stale and nothing may be deleted.
	stale := startupAccount{id: 8, uid: 17000008, name: "robot17000008", characters: []startupCharacter{{id: 80}}}
	toDelete, characters, err := purger.verifyInvalidAccounts(context.Background(), conn, "robot", []startupAccount{stale})
	if err != nil {
		t.Fatal(err)
	}
	if len(toDelete) != 0 || characters != 0 {
		t.Fatalf("stale candidate deleted: accounts=%v characters=%d", toDelete, characters)
	}

	// The unchanged candidate still matches and is deleted.
	fresh := startupAccount{id: 8, uid: 17000008, name: "robot17000008", characters: []startupCharacter{{id: 80}, {id: 81}}}
	toDelete, characters, err = purger.verifyInvalidAccounts(context.Background(), conn, "robot", []startupAccount{fresh})
	if err != nil {
		t.Fatal(err)
	}
	if len(toDelete) != 1 || toDelete[0].id != 8 || characters != 2 {
		t.Fatalf("fresh candidate = %v characters=%d, want account 8 with 2 characters", toDelete, characters)
	}
}

func TestGrowCompliantAlignsWithGrowthReconcile(t *testing.T) {
	purger := SQLiteStartupInventory{
		Config:     robotconfig.RuntimeConfig{GrowTypes: []int{2}, ReconcileAwakening: true},
		JobGrows:   map[int][]int{1: {2, 3}},
		StatTables: testStatTables(1, 9),
	}
	for _, test := range []struct {
		name string
		job  int
		grow int
		want bool
	}{
		{name: "untransferred with branches", job: 1, grow: 0, want: true},
		{name: "branch-less job stage 0", job: 9, grow: 0, want: true},
		{name: "unawakened reconcilable", job: 1, grow: 2, want: true},
		{name: "configured stage", job: 1, grow: 0x22, want: true},
		{name: "unconfigured stage is not compliant", job: 1, grow: 0x12, want: false},
		{name: "awakening without transfer is repaired", job: 1, grow: 0x20, want: true},
		{name: "stage out of range is repaired", job: 1, grow: 0x32, want: true},
		{name: "branch out of range is repaired", job: 1, grow: 0x1F, want: true},
		{name: "second nibble out of range is repaired", job: 1, grow: 0xF0, want: true},
	} {
		if got := purger.growCompliant(startupCharacter{job: test.job, grow: test.grow}); got != test.want {
			t.Fatalf("%s: job=%d grow=0x%02X compliant=%t want=%t", test.name, test.job, test.grow, got, test.want)
		}
	}

	// With awakening reconcile disabled the stored stage must match exactly.
	purger.Config = robotconfig.RuntimeConfig{GrowTypes: []int{0, 2}, ReconcileAwakening: false}
	if !purger.growCompliant(startupCharacter{job: 1, grow: 2}) {
		t.Fatal("stage 0 must stay accepted when awakening reconcile is disabled")
	}
	purger.Config = robotconfig.RuntimeConfig{GrowTypes: []int{2}, ReconcileAwakening: false}
	if purger.growCompliant(startupCharacter{job: 1, grow: 2}) {
		t.Fatal("unawakened character accepted while awakening reconcile is disabled")
	}
}

func TestStartupInventorySkipsCandidateRepairedAfterScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES (8,'robot17000008');
INSERT INTO characters(character_id,account_id,name,job,grow_type,level,delete_flag)
VALUES (80,8,'Repaired',1,0,5,0);`); err != nil {
		t.Fatal(err)
	}
	rc := robotconfig.Default()
	rc.LevelMin, rc.LevelMax = 50, 85
	rc.Jobs, rc.GrowTypes = []int{1}, []int{0}
	rc.EquipSlots = []int{1}
	rc.MinAvatarSlots = 0
	rc.PreferEquipSets, rc.PreferAvatarSets = false, false
	rc.PetEnabled = false
	item := shared.EquipmentCatalogItem{ID: 1001, ItemType: 1, Level: 40, UseJob: []int{1}}
	if _, err := db.Exec(`UPDATE characters SET level=50 WHERE character_id=80`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO character_inventory_items(character_id,list_type,slot_index,item_core)
VALUES (80,?,?,?)`, cn90ListTypeEquipment, 12, cn90ItemCore(cn90ItemKindEquipment, item, 0, 1)); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	purger := SQLiteStartupInventory{AccountPrefix: "robot", Config: rc, Equipment: []shared.EquipmentCatalogItem{item}}
	stale := startupAccount{id: 8, uid: 17000008, name: "robot17000008", characters: []startupCharacter{{id: 80, level: 5}}}
	toDelete, characters, err := purger.verifyInvalidAccounts(context.Background(), conn, "robot", []startupAccount{stale})
	if err != nil {
		t.Fatal(err)
	}
	if len(toDelete) != 0 || characters != 0 {
		t.Fatalf("repaired candidate marked for deletion: accounts=%v characters=%d", toDelete, characters)
	}
}

func TestStartupInventoryAdoptsCompliantAndHardDeletesInvalidAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openLoadoutTestDB(t, path)
	if _, err := db.Exec(`
INSERT INTO accounts(account_id,m_id) VALUES
 (7,'robot17000007'),(8,'robot17000008'),(9,'laoxxx'),(10,'robot18000010');
INSERT INTO characters(character_id,account_id,name,job,grow_type,level,delete_flag) VALUES
 (70,7,'Compliant',1,18,50,0),
 (80,8,'Reusable',1,0,5,0),
 (81,8,'DeletedSibling',1,0,50,1),
 (90,9,'RealPlayer',1,0,5,0),
 (100,10,'OtherSegment',1,0,5,0);
UPDATE characters SET town_id=3,area_id=2,pos_x=480,pos_y=240,slot_index=0 WHERE character_id=70;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	rc := robotconfig.Default()
	rc.RobotUIDStart, rc.RobotUIDEnd = 17000000, 17000099
	rc.LevelMin, rc.LevelMax = 50, 85
	rc.Jobs, rc.GrowTypes = []int{1}, []int{0, 1, 2}
	rc.EquipSlots = []int{1, 11, 12}
	rc.MinAvatarSlots = 0
	rc.PetEnabled, rc.PetProbabilityPercent = true, 100
	rc.PetArtifactEnabled = true
	rc.PetArtifactSlots = []int{31}
	rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots = 1, 1
	items := []shared.EquipmentCatalogItem{
		{ID: 1001, ItemType: 1, Level: 40, Durability: 45, UseJob: []int{1}, SetKey: "starter-equip"},
		{ID: 1011, ItemType: 11, Level: 60, Durability: 30, UseJob: []int{100}, ClientIncompatible: true, SetKey: "starter-equip"},
		{ID: 1012, ItemType: 12, Level: 68, Durability: 30, UseJob: []int{100}, ClientIncompatible: true, SetKey: "starter-equip"},
		{ID: 3000, Name: "Creature", ItemType: 30, Icon: "creature/pet.img"},
		{ID: 3100, Name: "Artifact", ItemType: 31, Path: "equipment/creature/artifact_red/hand.equ", Icon: "Item/creature/artifact_red.img"},
	}
	applier := SQLiteLoadoutApplier{DatabasePath: path, Config: rc, Equipment: items, RandIntn: func(int) int { return 0 }}
	if err := applier.ApplyCharacterLoadout(context.Background(), "robot17000007", robotcap.Info{Name: "Compliant", Job: 1, Level: 50}); err != nil {
		t.Fatal(err)
	}

	result, err := (SQLiteStartupInventory{
		DatabasePath: path, AccountPrefix: "robot", Config: rc, Equipment: items,
	}).ScanAndClean(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.ScannedAccounts != 2 || result.DeletedAccounts != 1 || result.DeletedCharacters != 2 {
		t.Fatalf("scan result=%+v", result)
	}
	if len(result.Robots) != 1 || result.Robots[0].UID != 17000007 || result.Robots[0].CID != 70 || result.Robots[0].Village != 3 || result.Robots[0].Grow != 0x12 {
		t.Fatalf("adopted robots=%+v", result.Robots)
	}
	if len(result.Identities) != 1 || result.Identities[0].Account != "robot17000007" || result.Identities[0].Slot == nil || *result.Identities[0].Slot != 0 {
		t.Fatalf("adopted identities=%+v", result.Identities)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var invalidAccounts, invalidCharacters, protectedAccounts int
	if err := db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE account_id=8`).Scan(&invalidAccounts); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM characters WHERE account_id=8`).Scan(&invalidCharacters); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM accounts WHERE account_id IN (9,10)`).Scan(&protectedAccounts); err != nil {
		t.Fatal(err)
	}
	if invalidAccounts != 0 || invalidCharacters != 0 || protectedAccounts != 2 {
		t.Fatalf("remaining invalid_accounts=%d invalid_characters=%d protected=%d", invalidAccounts, invalidCharacters, protectedAccounts)
	}
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES(11,'robot17000008');
INSERT INTO characters(character_id,account_id,name,job,grow_type,level,delete_flag) VALUES(80,11,'Reusable',1,0,50,0)`); err != nil {
		t.Fatalf("deleted account or name was not reusable: %v", err)
	}
}

func TestOwnedRobotUIDRequiresCanonicalConfiguredAccount(t *testing.T) {
	for _, test := range []struct {
		account string
		want    bool
	}{
		{account: "robot17000000", want: true},
		{account: "robot017000000", want: false},
		{account: "robot17000001", want: false},
		{account: "laoxxx", want: false},
	} {
		_, got := ownedRobotUID(test.account, "robot", 17000000, 17000000)
		if got != test.want {
			t.Fatalf("account=%s owned=%t want=%t", test.account, got, test.want)
		}
	}
}

func TestLiveStartupInventoryAgainstDatabaseClone(t *testing.T) {
	databasePath := os.Getenv("CN90_TEST_DB")
	pvfPath := os.Getenv("CN90_TEST_PVF")
	if databasePath == "" || pvfPath == "" {
		t.Skip("CN90_TEST_DB and CN90_TEST_PVF are required")
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
	equipment, _, err := ReadItemCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (SQLiteStartupInventory{
		DatabasePath: clone, AccountPrefix: "robot", Config: robotconfig.Default(), Equipment: equipment,
	}).ScanAndClean(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("startup inventory: scanned=%d adopted=%d deleted_accounts=%d deleted_characters=%d", result.ScannedAccounts, len(result.Robots), result.DeletedAccounts, result.DeletedCharacters)
	if result.ScannedAccounts != len(result.Robots)+result.DeletedAccounts {
		t.Fatalf("incomplete startup classification: %+v", result)
	}
}
