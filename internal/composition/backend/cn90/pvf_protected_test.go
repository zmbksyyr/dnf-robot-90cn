package cn90

import (
	"os"
	"strings"
	"testing"
)

// defaultLivePVFPath is the local 90CN runtime archive used when the operator
// did not set CN90_TEST_PVF. The real-file checks skip when it is absent.
const defaultLivePVFPath = `D:\cache\game\DNF\DNF90-source-oneclick\runtime\data\dnf\Script.pvf`

func livePVFPath(t *testing.T) string {
	t.Helper()
	if path := strings.TrimSpace(os.Getenv("CN90_TEST_PVF")); path != "" {
		return path
	}
	if isRegularFile(defaultLivePVFPath) {
		return defaultLivePVFPath
	}
	t.Skip("CN90_TEST_PVF is not set and the local 90CN Script.pvf is absent")
	return ""
}

// TestLiveProtectedArchiveReadsScriptTables proves the protected_nkpi string
// pools decode: without them every PVF path resolves to an empty string.
func TestLiveProtectedArchiveReadsScriptTables(t *testing.T) {
	path := livePVFPath(t)
	archive, err := openCN90PVF(path)
	if err != nil {
		t.Fatal(err)
	}
	if !archive.protected {
		t.Fatalf("the 90CN runtime archive is expected to be protected_nkpi: %s", path)
	}
	if len(archive.paths) == 0 {
		t.Fatal("protected archive produced no file paths")
	}
	townList, err := archive.ReadText("town/town.lst")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(townList, ".twn") {
		t.Fatalf("town/town.lst does not reference .twn files (len=%d)", len(townList))
	}
	characterList, err := archive.ReadText("character/character.lst")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(characterList, ".chr") {
		t.Fatalf("character/character.lst does not reference .chr files (len=%d)", len(characterList))
	}
	maps, err := ReadTownMapCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) == 0 {
		t.Fatal("the protected archive produced an empty town map catalog")
	}
	t.Logf("protected archive: %d paths, town.lst=%d bytes, character.lst=%d bytes, maps=%d", len(archive.paths), len(townList), len(characterList), len(maps))
}

// TestLiveReadAllCatalogs runs the complete catalogs projection against the
// real archive: town maps, item catalogs, character stat tables, job growth,
// quest gates and level thresholds.
func TestLiveReadAllCatalogs(t *testing.T) {
	path := livePVFPath(t)
	catalogs, err := ReadCatalogs(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalogs.TownMaps) == 0 || len(catalogs.Equipment) == 0 || len(catalogs.Stackable) == 0 {
		t.Fatalf("catalogs = maps %d, equipment %d, stackable %d", len(catalogs.TownMaps), len(catalogs.Equipment), len(catalogs.Stackable))
	}
	if len(catalogs.StatTables) == 0 {
		t.Fatalf("character stat tables are empty (fallback jobs=%v)", catalogs.StatFallbackJobs)
	}
	if len(catalogs.LevelThresholds) == 0 {
		t.Fatal("level thresholds are empty")
	}
	t.Logf("catalogs: maps=%d equipment=%d stackable=%d stat_jobs=%d fallback_jobs=%v grows=%d level_thresholds=%d quest_completed=%d quest_active=%d",
		len(catalogs.TownMaps), len(catalogs.Equipment), len(catalogs.Stackable),
		len(catalogs.StatTables), catalogs.StatFallbackJobs, len(catalogs.JobGrows),
		len(catalogs.LevelThresholds), len(catalogs.QuestGates.CompletedQuestIDs), len(catalogs.QuestGates.ActiveQuestIDs))
}
