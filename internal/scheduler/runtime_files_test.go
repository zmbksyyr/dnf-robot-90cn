package scheduler

import (
	"os"
	"testing"
	"time"

	storecap "robot/internal/capability/store"
	"robot/internal/foundation/config"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestRuntimeFileWatcherPublishesValidSnapshotsAndRejectsInvalidEdits(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		paths.RobotConfig():    "[auto]\nauto_target_online_count = 20\n",
		paths.NameTemplates():  `{"common":["Alpha"],"jobs":{}}`,
		paths.ShoutTemplates(): `{"channel":"world","type":3,"messages":["one"]}`,
		paths.StoreTitles():    `["Shop A","Shop B"]`,
		paths.PartySkills():    `{"enabled":true,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1}]}`,
	}
	for path, data := range writes {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	shared.SetPartySkillStates(nil)
	t.Cleanup(func() { shared.SetPartySkillStates(nil) })

	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: root}, nil)
	poller := filewatch.New(time.Hour, manager.RuntimeFileEntries(), nil)
	poller.CheckNow()
	if got := manager.loadRobotConfig().AutoTargetOnlineCount; got != 20 {
		t.Fatalf("initial target=%d, want 20", got)
	}
	if got := manager.loadNameTemplates().Common; len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("initial names=%v", got)
	}
	if got := manager.loadShoutTemplates().Messages; len(got) != 1 || got[0] != "one" {
		t.Fatalf("initial shouts=%v", got)
	}
	if titles := manager.storeTitleSnapshot.Load(); titles == nil || titles.Len() != 2 {
		t.Fatalf("initial store titles=%v", titles)
	}
	if got := shared.PartySkillStatesForJob(1); len(got) != 1 || got[0].SkillIndex != 2 {
		t.Fatalf("initial party skills=%v", got)
	}
	sentinelPool := &storecap.ItemPool{}
	manager.storePoolLock.Lock()
	manager.storeItemPool = sentinelPool
	manager.storePoolLock.Unlock()

	updates := map[string]string{
		paths.RobotConfig():    "[auto]\nauto_target_online_count = 25\n",
		paths.NameTemplates():  `{"common":["Beta","Gamma"],"jobs":{}}`,
		paths.ShoutTemplates(): `{"channel":"world","type":3,"messages":["two","three"]}`,
		paths.StoreTitles():    `["Shop C","Shop D","Shop E"]`,
		paths.PartySkills():    `{"enabled":true,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":4,"state":5,"level":2}]}`,
	}
	for path, data := range updates {
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	poller.CheckNow()
	if got := manager.loadRobotConfig().AutoTargetOnlineCount; got != 25 {
		t.Fatalf("updated target=%d, want 25", got)
	}
	if got := manager.loadNameTemplates().Common; len(got) != 2 || got[0] != "Beta" {
		t.Fatalf("updated names=%v", got)
	}
	if got := manager.loadShoutTemplates().Messages; len(got) != 2 || got[0] != "two" {
		t.Fatalf("updated shouts=%v", got)
	}
	if titles := manager.storeTitleSnapshot.Load(); titles == nil || titles.Len() != 3 {
		t.Fatalf("updated store titles=%v", titles)
	}
	if got := shared.PartySkillStatesForJob(1); len(got) != 1 || got[0].SkillIndex != 4 {
		t.Fatalf("updated party skills=%v", got)
	}
	if manager.storeItemPool != sentinelPool {
		t.Fatal("unrelated config change rebuilt the store item pool")
	}
	if err := os.WriteFile(paths.RobotConfig(), []byte("[auto]\nauto_target_online_count = 25\n[store]\nstore_equipment_intensify_min = 8\nstore_equipment_intensify_max = 12\n"), 0644); err != nil {
		t.Fatal(err)
	}
	poller.CheckNow()
	if manager.storeItemPool != nil {
		t.Fatal("store pool was not invalidated after its configuration changed")
	}

	if err := os.Remove(paths.RobotConfig()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.NameTemplates(), paths.ShoutTemplates()} {
		if err := os.WriteFile(path, []byte(`["legacy-array"]`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(paths.StoreTitles(), []byte(`{"broken":`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PartySkills(), []byte(`{"enabled":true,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":0,"state":5,"level":2}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	poller.CheckNow()
	if got := manager.loadRobotConfig().AutoTargetOnlineCount; got != 25 {
		t.Fatalf("invalid edit replaced robot config: target=%d", got)
	}
	if got := manager.loadNameTemplates().Common; len(got) != 2 || got[0] != "Beta" {
		t.Fatalf("invalid edit replaced names=%v", got)
	}
	if got := manager.loadShoutTemplates().Messages; len(got) != 2 || got[0] != "two" {
		t.Fatalf("invalid edit replaced shouts=%v", got)
	}
	if titles := manager.storeTitleSnapshot.Load(); titles == nil || titles.Len() != 3 {
		t.Fatalf("invalid edit replaced store titles=%v", titles)
	}
	if got := shared.PartySkillStatesForJob(1); len(got) != 1 || got[0].SkillIndex != 4 {
		t.Fatalf("invalid edit replaced party skills=%v", got)
	}
}

func TestReloadRobotConfigDisablingAutoStopsExistingActors(t *testing.T) {
	manager := testRobotManagerWithConfig(t, "[auto]\nauto_actions = true\n")
	_ = manager.loadRobotConfig()
	supervisor := NewRobotSupervisor(manager, actorTestRuntime{})
	ensureSupervisorActors(t, supervisor, 2)
	manager.autoMu.Lock()
	manager.supervisor = supervisor
	manager.autoEnabled = true
	manager.autoMu.Unlock()

	path := layout.New(manager.cfg.ConfigDir).RobotConfig()
	if err := os.WriteFile(path, []byte("[auto]\nauto_actions = false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := manager.reloadRobotConfigFile(path); err != nil {
		t.Fatal(err)
	}
	if actors := supervisor.ledger.ActorPointers(); len(actors) != 0 {
		t.Fatalf("auto actors still active after file disable: %d", len(actors))
	}
	manager.autoMu.Lock()
	enabled := manager.autoEnabled
	manager.autoMu.Unlock()
	if !enabled {
		t.Fatal("file disable cleared runtime supervisor switch; re-enable could not resume naturally")
	}
}

func TestRuntimeFileEntriesFollowBackendCapabilities(t *testing.T) {
	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, nil)
	manager.SetBackendRobotCreator(testS4BackendInfo(), nil)
	entries := manager.RuntimeFileEntries()
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name] = true
	}
	for _, required := range []string{"robot_config", "name_templates", "shout_templates"} {
		if !names[required] {
			t.Fatalf("shared runtime file %q is missing", required)
		}
	}
	for _, unsupported := range []string{"store_titles", "party_skills"} {
		if names[unsupported] {
			t.Fatalf("unsupported runtime file %q is still watched", unsupported)
		}
	}
}
