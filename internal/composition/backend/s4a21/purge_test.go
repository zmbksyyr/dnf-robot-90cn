package s4a21

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
	path := filepath.Join(t.TempDir(), "inventory.db")
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
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM accounts WHERE m_id IN ('robot17000001','robot17000002','robot17000005')`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM accounts WHERE m_id IN ('player17000001','robot17000003x','robot0017000004')`, 3)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM character_data WHERE character_id IN (101,102,103)`, 0)
}

func TestSQLiteRobotPurgerCIDDeletesOneCharacterAndRetainsNonemptyAccount(t *testing.T) {
	path := newPurgeTestDatabase(t)
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 17000002, CID: 102, Name: "two"}})
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
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM accounts WHERE m_id='robot17000002'`, 1)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM characters WHERE character_id=102`, 0)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM characters WHERE character_id=103`, 1)
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
	if _, err := db.Exec(`INSERT INTO accounts(account_id,m_id) VALUES (7,'robot17000007');
INSERT INTO characters(character_id,account_id,name) VALUES (107,7,'late');`); err != nil {
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
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM accounts WHERE m_id IN ('robot17000001','robot17000002','robot17000005','robot17000007')`, 4)
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM characters WHERE character_id IN (101,102,103,107)`, 4)
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
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM characters WHERE character_id=104`, 1)
}

func TestSQLiteRobotPurgerTransactionFailureKeepsRuntimeState(t *testing.T) {
	path := newPurgeTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	if _, err := db.Exec(`CREATE TRIGGER reject_robot_delete BEFORE DELETE ON accounts
WHEN OLD.m_id='robot17000001' BEGIN SELECT RAISE(ABORT,'blocked'); END;`); err != nil {
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
	assertPurgeRowCount(t, db, `SELECT COUNT(*) FROM accounts WHERE m_id='robot17000001'`, 1)
}

func newPurgeTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.db")
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	_, err := db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE accounts(account_id INTEGER PRIMARY KEY,m_id TEXT UNIQUE);
CREATE TABLE characters(character_id INTEGER PRIMARY KEY,account_id INTEGER NOT NULL,name TEXT,FOREIGN KEY(account_id) REFERENCES accounts(account_id) ON DELETE CASCADE);
CREATE TABLE character_data(character_id INTEGER PRIMARY KEY,value TEXT,FOREIGN KEY(character_id) REFERENCES characters(character_id) ON DELETE CASCADE);
INSERT INTO accounts(account_id,m_id) VALUES
(1,'robot17000001'),(2,'robot17000002'),(3,'player17000001'),(4,'robot17000003x'),(5,'robot0017000004'),(6,'robot17000005');
INSERT INTO characters(character_id,account_id,name) VALUES
(101,1,'one'),(102,2,'two'),(103,2,'two-alt'),(104,3,'player'),(105,4,'suffix'),(106,5,'leading-zero');
INSERT INTO character_data(character_id,value) SELECT character_id,'data' FROM characters;`)
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
