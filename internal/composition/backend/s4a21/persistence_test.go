package s4a21

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistenceInspectorValidatesWritableSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inventory.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"accounts", "characters", "character_inventory_items", "character_avatar_detail", "character_avatar_uid_sequence"} {
		if _, err := db.Exec("CREATE TABLE " + table + " (id INTEGER)"); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	status := (PersistenceInspector{DatabasePath: path}).Status(context.Background())
	if !status.OK || !status.Writable || !status.SelectVerified || status.Engine != "sqlite" || status.Target != path {
		t.Fatalf("status = %+v", status)
	}
}

func TestPersistenceInspectorRejectsUnrelatedSQLiteSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE unrelated (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	status := (PersistenceInspector{DatabasePath: path}).Status(context.Background())
	if status.OK || !strings.Contains(status.Error, "missing table") {
		t.Fatalf("status = %+v", status)
	}
}
