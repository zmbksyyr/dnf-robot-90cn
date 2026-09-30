package cn90

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
)

func TestSQLiteRobotPurgerOpenConfiguresBusyTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dnf90.db")
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot"}
	db, err := purger.open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var timeout int
	if err := db.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestSQLiteRobotPurgerRangeDeletesStrictRobotAccountsAndState(t *testing.T) {
	path := newPurgeTestDatabase(t)
	state := robotstate.NewMemoryStore([]robotcap.Info{
		{UID: 17000001, CID: 101, Name: "one"},
		{UID: 17000002, CID: 102, Name: "two"},
	})
	if err := state.RegisterIdentities(context.Background(), []robotstate.Identity{
		{Backend: BackendID, Account: "robot17000001", CharacterName: "one"},
		{Backend: BackendID, Account: "robot17000002", CharacterName: "two"},
		// Orphan identity: the account exists in the database but has no live
		// robot entry, so RemoveRobots can never see its character name.
		{Backend: BackendID, Account: "robot17000005", CharacterName: "orphan"},
		{Backend: BackendID, Account: "player17000001", CharacterName: "keep"},
	}); err != nil {
		t.Fatal(err)
	}
	closer := &recordingSessionCloser{}
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot", State: state, Sessions: closer}
	request := robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeRange, MinUID: 17000001, MaxUID: 17000005}
	plan, err := purger.PlanDangerousDelete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if plan.AccountCount != 3 || plan.CharacterCount != 3 || plan.RegistryCount != 2 {
		t.Fatalf("plan=%+v", plan)
	}
	result, err := purger.ExecuteDangerousDelete(context.Background(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Deleted || result.AccountCount != 3 || result.CharacterCount != 3 || result.RegistryCount != 2 {
		t.Fatalf("result=%+v", result)
	}
	if !reflect.DeepEqual(closer.uids, []int{17000001, 17000002}) {
		t.Fatalf("closed=%v", closer.uids)
	}
	robots, err := state.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if err != nil || len(robots) != 0 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	identities, err := state.Identities(context.Background(), BackendID)
	if err != nil || len(identities) != 1 || identities[0].Account != "player17000001" {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id IN ('robot17000001','robot17000002','robot17000005')`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id IN ('player17000001','robot17000003x','robot0017000004')`, 3)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id IN (101,102,103)`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_inventory_items WHERE character_id IN (101,102,103)`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope IN ('character:101:container_state','character:102:container_state','character:103:container_state')`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_character_stats WHERE character_id=104`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_settings WHERE scope='character:104:container_state'`, 1)
}

func TestSQLiteRobotPurgerCIDDeletesOneCharacterAndRetainsNonemptyAccount(t *testing.T) {
	path := newPurgeTestDatabase(t)
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 17000002, CID: 102, Name: "two"}})
	if err := state.RegisterIdentities(context.Background(), []robotstate.Identity{
		{Backend: BackendID, Account: "robot17000002", CharacterName: "two"},
		{Backend: BackendID, Account: "player17000001", CharacterName: "keep"},
	}); err != nil {
		t.Fatal(err)
	}
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot", State: state}
	plan, err := purger.PlanDangerousDelete(context.Background(), robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeCID, CID: 102})
	if err != nil {
		t.Fatal(err)
	}
	if plan.AccountCount != 0 || plan.CharacterCount != 1 || plan.RegistryCount != 1 || plan.UID != 17000002 {
		t.Fatalf("plan=%+v", plan)
	}
	result, err := purger.ExecuteDangerousDelete(context.Background(), plan)
	if err != nil || !result.Deleted || result.AccountCount != 0 || result.CharacterCount != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id='robot17000002'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='102'`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='103'`, 1)
	identities, err := state.Identities(context.Background(), BackendID)
	if err != nil || len(identities) != 1 || identities[0].Account != "player17000001" {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
}

func TestSQLiteRobotPurgerRejectsChangedDeletePlan(t *testing.T) {
	path := newPurgeTestDatabase(t)
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot"}
	plan, err := purger.PlanDangerousDelete(context.Background(), robotcap.DangerousDeleteRequest{
		Mode: robotcap.DangerousDeleteModeRange, MinUID: 17000001, MaxUID: 17000009,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.AccountCount != 3 || plan.CharacterCount != 3 {
		t.Fatalf("plan=%+v", plan)
	}

	// A new matching robot appears after the preview: the execution must abort
	// rather than delete a set the operator never confirmed.
	db := openPurgeTestDatabase(t, path)
	if _, err := db.Exec(`INSERT INTO dnf_accounts(account_id) VALUES ('robot17000007');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type) VALUES ('107','robot17000007',0,'late','1',70,0);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	if _, err := purger.ExecuteDangerousDelete(context.Background(), plan); err == nil {
		t.Fatal("execute accepted a changed delete plan")
	} else if !strings.Contains(err.Error(), "plan changed since preview") {
		t.Fatalf("error = %v, want a plan drift error", err)
	}

	db = openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id IN ('robot17000001','robot17000002','robot17000005','robot17000007')`, 4)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id IN ('101','102','103','107')`, 4)
}

func TestSQLiteRobotPurgerCIDNeverDeletesPlayerCharacter(t *testing.T) {
	path := newPurgeTestDatabase(t)
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot"}
	plan, err := purger.PlanDangerousDelete(context.Background(), robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeCID, CID: 104})
	if err != nil {
		t.Fatal(err)
	}
	result, err := purger.ExecuteDangerousDelete(context.Background(), plan)
	if err != nil || result.Deleted || result.AccountCount != 0 || result.CharacterCount != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='104'`, 1)
}

func TestSQLiteRobotPurgerTransactionFailureKeepsRuntimeState(t *testing.T) {
	path := newPurgeTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	if _, err := db.Exec(`CREATE TRIGGER reject_robot_delete BEFORE DELETE ON dnf_accounts
WHEN OLD.account_id='robot17000001' BEGIN SELECT RAISE(ABORT,'blocked'); END;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 17000001, CID: 101, Name: "one"}})
	purger := SQLiteRobotPurger{DatabasePath: path, AccountPrefix: "robot", State: state}
	plan, err := purger.PlanDangerousDelete(context.Background(), robotcap.DangerousDeleteRequest{Mode: robotcap.DangerousDeleteModeUID, UID: 17000001})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := purger.ExecuteDangerousDelete(context.Background(), plan); err == nil {
		t.Fatal("purge unexpectedly succeeded")
	}
	robots, err := state.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if err != nil || len(robots) != 1 || robots[0].UID != 17000001 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	db = openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_accounts WHERE account_id='robot17000001'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM dnf_characters WHERE character_id='101'`, 1)
}

func newPurgeTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dnf90.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE dnf_accounts(account_id TEXT PRIMARY KEY, state TEXT, updated_at TEXT);
CREATE TABLE dnf_characters(character_id TEXT PRIMARY KEY, account_id TEXT NOT NULL, slot INT, name TEXT, job TEXT, level INT, grow_type INT, delete_flag INT DEFAULT 0, updated_at TEXT);
CREATE TABLE dnf_character_stats(character_id TEXT, stat_key TEXT, stat_value INT);
CREATE TABLE dnf_inventory_items(character_id TEXT, entry_key TEXT, item_id INT);
CREATE TABLE dnf_settings(scope TEXT, key TEXT);
INSERT INTO dnf_accounts(account_id) VALUES
('robot17000001'),('robot17000002'),('player17000001'),('robot17000003x'),('robot0017000004'),('robot17000005');
INSERT INTO dnf_characters(character_id,account_id,slot,name,job,level,grow_type) VALUES
('101','robot17000001',0,'one','1',70,0),
('102','robot17000002',0,'two','2',71,0),
('103','robot17000002',1,'two-alt','2',72,0),
('104','player17000001',0,'player','3',80,0);
INSERT INTO dnf_character_stats(character_id,stat_key,stat_value) SELECT character_id,'hp',1 FROM dnf_characters;
INSERT INTO dnf_inventory_items(character_id,entry_key,item_id) SELECT character_id,'0:0',1 FROM dnf_characters;
INSERT INTO dnf_settings(scope,key) SELECT 'character:'||character_id||':container_state','header' FROM dnf_characters;`)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func openPurgeTestDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	configureSQLitePool(db)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}

func assertPurgeRowCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("query=%q count=%d want=%d", query, got, want)
	}
}
