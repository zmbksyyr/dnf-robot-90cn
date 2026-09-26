package s4a21

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func newFollowAccountTestDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE accounts(account_id INTEGER PRIMARY KEY,m_id TEXT UNIQUE);
CREATE TABLE characters(character_id INTEGER PRIMARY KEY,account_id INTEGER NOT NULL,name TEXT,town_id INTEGER DEFAULT 0,delete_flag INTEGER DEFAULT 0,updated_at TEXT);
INSERT INTO accounts(account_id,m_id) VALUES (1,'leader'),(2,'empty-account');
INSERT INTO characters(character_id,account_id,name,town_id,delete_flag,updated_at) VALUES
 (10,1,'old',3,0,'2026-09-01 00:00:00'),
 (11,1,'newest',9,0,'2026-09-02 00:00:00'),
 (12,1,'deleted',7,1,'2026-09-03 00:00:00');`); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFollowAccountLocatorPicksNewestLiveCharacter(t *testing.T) {
	locator := FollowAccountLocator{DatabasePath: newFollowAccountTestDatabase(t)}
	village, ok, err := locator.FollowAccountVillageLastPlayed(context.Background(), "leader")
	if err != nil || !ok || village != 9 {
		t.Fatalf("village=%d ok=%t err=%v, want town 9 from the newest live character", village, ok, err)
	}
	if _, ok, err := locator.FollowAccountVillageLastPlayed(context.Background(), "missing"); err != nil || ok {
		t.Fatalf("missing account ok=%t err=%v, want no result", ok, err)
	}
	if _, ok, err := locator.FollowAccountVillageLastPlayed(context.Background(), "empty-account"); err != nil || ok {
		t.Fatalf("account without characters ok=%t err=%v, want no result", ok, err)
	}
	if _, _, err := locator.FollowAccountVillageLastPlayed(context.Background(), " "); err == nil {
		t.Fatal("empty account was accepted")
	}
	if _, _, err := (FollowAccountLocator{}).FollowAccountVillageLastPlayed(context.Background(), "leader"); err == nil {
		t.Fatal("missing database path was accepted")
	}
}
