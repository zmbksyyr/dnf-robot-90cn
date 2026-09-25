package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"robot/internal/shared"
)

func TestLoadPartySkillsIndexesFilteredCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "party_skill_catalog.json")
	data := []byte(`{
  "enabled": true,
  "max_skill_level": 50,
  "skills": [
    {"enabled":true,"job":6,"skill_index":3,"state":22,"level":5,"name":"ok","state_data":[3],"risk":1},
    {"enabled":true,"job":6,"skill_index":4,"state":23,"level":60,"name":"too_high","state_data":[0],"risk":1},
    {"enabled":true,"job":6,"skill_index":5,"state":24,"level":10,"disabled":true,"state_data":[0],"risk":1},
    {"enabled":true,"job":2,"skill_index":6,"state":25,"level":10,"state_data":[0],"risk":1}
  ]
}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	previous := shared.PartySkillStatesForJob(6)
	t.Cleanup(func() { shared.SetPartySkillStates(previous) })

	if err := LoadPartySkills(dir); err != nil {
		t.Fatal(err)
	}
	got := shared.PartySkillStatesForJob(6)
	if len(got) != 1 || got[0].SkillIndex != 3 || got[0].Level != 5 || got[0].Name != "ok" || !bytes.Equal(got[0].StateData, []byte{3, 0, 0}) {
		t.Fatalf("filtered catalog = %+v", got)
	}
}

func TestLoadPartySkillsRejectsInvalidStateData(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"enabled":true,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1,"state_data":[16777216]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "party_skill_catalog.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := LoadPartySkills(dir); err == nil {
		t.Fatal("invalid state_data was accepted")
	}
}

func TestLoadPartySkillsRetainsPreviousSnapshotOnInvalidEntry(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{
  "enabled": true,
  "max_skill_level": 70,
  "skills": [
    {"enabled":true,"job":6,"skill_index":3,"state":22,"level":5,"state_data":[3]},
    {"enabled":true,"job":6,"skill_index":4,"state":23,"level":10,"state_data":[16777216]},
    {"enabled":true,"job":6,"skill_index":5,"state":24,"level":15,"state_data":"AQID"},
    {"enabled":true,"job":6,"skill_index":6,"state":25,"level":20,"state_data":[4,5]}
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, "party_skill_catalog.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	previous := []shared.PartySkillState{{Job: 6, SkillIndex: 99, State: 99, Level: 1}}
	shared.SetPartySkillStates(previous)
	t.Cleanup(func() { shared.SetPartySkillStates(nil) })

	err := LoadPartySkills(dir)
	var validationErr *PartySkillCatalogValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want validation error", err)
	}
	if len(validationErr.Issues) != 2 || !strings.Contains(err.Error(), "2 invalid entries") {
		t.Fatalf("validation error = %v", err)
	}
	got := shared.PartySkillStatesForJob(6)
	if len(got) != 1 || got[0].SkillIndex != 99 {
		t.Fatalf("invalid file replaced previous snapshot: %+v", got)
	}
}

func TestPartySkillValidationErrorSummarizesMultipleIssues(t *testing.T) {
	err := (&PartySkillCatalogValidationError{Issues: []PartySkillCatalogIssue{
		{Index: 0, Job: 1, SkillIndex: 2, State: 3, Reason: "first"},
		{Index: 1, Job: 2, SkillIndex: 3, State: 4, Reason: "second"},
		{Index: 2, Job: 3, SkillIndex: 4, State: 5, Reason: "third"},
		{Index: 3, Job: 4, SkillIndex: 5, State: 6, Reason: "fourth"},
	}}).Error()
	for _, want := range []string{"4 invalid entries", "first", "second", "third", "and 1 more"} {
		if !strings.Contains(err, want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err, "fourth") {
		t.Fatalf("error was not bounded: %q", err)
	}
}

func TestLoadPartySkillsRejectsOutOfRangeMaxSkillLevel(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{
  "enabled": true,
  "max_skill_level": 85,
  "skills": [
    {"enabled":true,"job":2,"skill_index":6,"state":25,"level":70,"state_data":[0]},
    {"enabled":true,"job":2,"skill_index":7,"state":26,"level":71,"state_data":[0]}
  ]
}`)
	if err := os.WriteFile(filepath.Join(dir, "party_skill_catalog.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := LoadPartySkills(dir); err == nil {
		t.Fatal("out-of-range max_skill_level unexpectedly loaded")
	}
}

func TestLoadPartySkillsRequiresExplicitEnabled(t *testing.T) {
	dir := t.TempDir()
	data := []byte(`{"max_skill_level":70,"skills":[{"enabled":true,"job":6,"skill_index":3,"state":22,"level":5,"state_data":[3]}]}`)
	if err := os.WriteFile(filepath.Join(dir, "party_skill_catalog.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := LoadPartySkills(dir); err == nil {
		t.Fatal("catalog without enabled unexpectedly loaded")
	}
}

func TestPartySkillCatalogRejectsUnknownMissingAndOutOfRangeFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "party_skill_catalog.json")
	for _, data := range []string{
		`{"enabled":false,"max_skill_level":70,"skills":[],"legacy":true}`,
		`{"enabled":false,"skills":[]}`,
		`{"enabled":false,"max_skill_level":0,"skills":[]}`,
		`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1,"legacy":true}]}`,
		`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadPartySkillCatalog(path); err == nil {
			t.Fatalf("invalid catalog unexpectedly decoded: %s", data)
		}
	}

	for _, data := range []string{
		`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":71}]}`,
		`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1,"risk":3}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		report, err := ReadPartySkillCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(report.Issues) != 1 {
			t.Fatalf("invalid disabled entry not rejected: report=%+v", report)
		}
	}
}

func TestReleasedRuntimeCatalogsUseCanonicalSchemas(t *testing.T) {
	defaults := filepath.Join("..", "..", "bootstrap", "runtime", "defaults")
	if _, err := ReadNameTemplates(filepath.Join(defaults, "robot_name_templates.json")); err != nil {
		t.Fatalf("released name template rejected: %v", err)
	}
	if _, err := ReadShoutTemplates(filepath.Join(defaults, "robot_shout_templates.json")); err != nil {
		t.Fatalf("released shout template rejected: %v", err)
	}
}

func TestSetPartySkillCatalogEnabledUpdatesTopLevelFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "party_skill_catalog.json")
	data := []byte(`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":6,"skill_index":3,"state":22,"level":5,"state_data":[3]}]}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetPartySkillCatalogEnabled(path, true); err != nil {
		t.Fatal(err)
	}
	report, err := ReadPartySkillCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Enabled || len(report.Entries) != 1 {
		t.Fatalf("enabled catalog = %+v", report)
	}
	if err := SetPartySkillCatalogEnabled(path, false); err != nil {
		t.Fatal(err)
	}
	report, err = ReadPartySkillCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Enabled || len(report.Entries) != 0 || report.SourceCount != 1 {
		t.Fatalf("disabled catalog = %+v", report)
	}
}

func TestSetPartySkillCatalogEnabledRejectsInvalidEnabledSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "party_skill_catalog.json")
	data := []byte(`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":6,"skill_index":0,"state":22,"level":5}]}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := SetPartySkillCatalogEnabled(path, true); err == nil {
		t.Fatal("invalid enabled snapshot unexpectedly saved")
	}
	report, err := ReadPartySkillCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.Enabled {
		t.Fatal("failed enable changed the file")
	}
}

func TestSyncPartySkillCatalogBuildsSwitchesFromPVF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "party_skill_catalog.json")
	pvfPath := filepath.Join(dir, "skill_state_catalog.json")
	initial := `{
  "enabled": true,
  "max_skill_level": 70,
  "skills": [
    {"enabled":true,"job":1,"skill_index":2,"state":3,"level":5,"name":"verified","script_path":"sqr/fighter/verified.nut","state_data":[3],"risk":1},
    {"enabled":true,"job":9,"skill_index":9,"state":9,"level":1,"name":"removed"}
  ]
}`
	pvf := `[
  {"job":1,"skill_index":2,"state":3,"script_path":"sqr/fighter/verified.nut"},
  {"job":2,"skill_index":4,"state":5,"script_path":"sqr/gunner/newskill.nut"}
]`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pvfPath, []byte(pvf), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := SyncPartySkillCatalog(path, pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("synchronization did not update the catalog")
	}
	report, err := ReadPartySkillCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Enabled || report.SourceCount != 2 || len(report.Entries) != 1 {
		t.Fatalf("synced report = %+v", report)
	}
	entry := report.Entries[0]
	if entry.Job != 1 || entry.SkillIndex != 2 || entry.Name != "verified" || entry.Level != 5 || entry.Risk != 1 || !bytes.Equal(entry.StateData, []byte{3, 0, 0}) {
		t.Fatalf("preserved entry = %+v", entry)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Skills []struct {
			ID       string `json:"id"`
			Enabled  bool   `json:"enabled"`
			JobLabel string `json:"job_label"`
			Name     string `json:"name"`
		} `json:"skills"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Skills) != 2 {
		t.Fatalf("generated switches = %+v", document.Skills)
	}
	if document.Skills[0].ID == "" || document.Skills[0].JobLabel == "" {
		t.Fatalf("generated switch metadata missing = %+v", document.Skills)
	}
	if document.Skills[1].Enabled {
		t.Fatalf("new switch unexpectedly enabled = %+v", document.Skills[1])
	}
	changed, err = SyncPartySkillCatalog(path, pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second synchronization was not idempotent")
	}
}

func TestSyncPartySkillCatalogRejectsEntriesWithoutSwitches(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "party_skill_catalog.json")
	pvfPath := filepath.Join(dir, "skill_state_catalog.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"max_skill_level":70,"skills":[{"job":1,"skill_index":2,"state":3,"level":1}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pvfPath, []byte(`[{"job":1,"skill_index":2,"state":3,"script_path":"sqr/fighter/skill.nut"}]`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncPartySkillCatalog(path, pvfPath); err == nil {
		t.Fatal("entry without enabled switch was accepted")
	}
}
