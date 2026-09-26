package s4a21

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSimulatorDatabaseAccessIsIsolatedToPersistenceAdapter(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "loadout.go" || entry.Name() == "startup_inventory.go" || entry.Name() == "purge.go" || entry.Name() == "population.go" || entry.Name() == "descriptor.go" || entry.Name() == "runtime_assembly.go" || entry.Name() == "follow_account.go" {
			// runtime_assembly.go owns the database path derivation and the
			// startup inventory composition; follow_account.go is the adapter's
			// read-only follow-account lookup. Both are persistence concerns.
			continue
		}
		path := filepath.Join(".", entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := strings.ToLower(string(data))
		for _, forbidden := range []string{
			"database/sql", "sqlite", "mysql", "inventory.db", "taiwan_cain", "d_starsky",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s references database marker %q outside the persistence adapter or backend descriptor", path, forbidden)
			}
		}
	}
}
