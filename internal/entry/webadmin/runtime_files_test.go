package webadmin

import (
	"os"
	"testing"
	"time"

	"robot/internal/foundation/config"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func TestRuntimeFileWatcherOnlyTracksNativeBackendFiles(t *testing.T) {
	cfg := &config.SysConfig{ConfigDir: t.TempDir()}
	if entries := newTestServerForBackend(cfg, shared.BackendS4A21).runtimeFileEntries(); len(entries) != 0 {
		t.Fatalf("S4A21 runtime entries=%v, want none", entries)
	}
	if entries := NewRecovery(cfg, "", "", shared.BackendNative).runtimeFileEntries(); len(entries) != 0 {
		t.Fatalf("recovery runtime entries=%v, want none", entries)
	}
	if entries := New(cfg, "", "", shared.BackendNative).runtimeFileEntries(); len(entries) != 3 {
		t.Fatalf("native runtime entry count=%d, want 3", len(entries))
	}
}

func TestPartyCapabilityDoesNotEnableNativePatchFiles(t *testing.T) {
	cfg := &config.SysConfig{ConfigDir: t.TempDir()}
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported"})
	capabilities[shared.CapabilityParty] = shared.CapabilityStatus{Enabled: true}
	server := NewWithCatalog(cfg, "", "", "party-only", []shared.BackendInfo{{
		ID: "party-only", Capabilities: capabilities,
	}})
	if entries := server.runtimeFileEntries(); len(entries) != 0 {
		t.Fatalf("ordinary party support enabled native patch files: %+v", entries)
	}
}

func TestRuntimeFileWatcherRetainsLastValidWebConfig(t *testing.T) {
	root := t.TempDir()
	paths := layout.New(root)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.MailboxGuard(), []byte(`{"mailbox_bad_node_guard":false}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PartyCompatibility(), []byte(`{"enabled":true,"account_start":17000000,"account_end":17001000}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PartySkills(), []byte(`{"enabled":true,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	server := New(&config.SysConfig{ConfigDir: root}, "", "")
	poller := filewatch.New(time.Hour, server.runtimeFileEntries(), nil)
	poller.CheckNow()

	mailbox, err := server.loadMailboxGuardConfig()
	if err != nil || mailbox.Enabled {
		t.Fatalf("initial mailbox config=%+v err=%v", mailbox, err)
	}
	party, err := server.loadPartyCompatConfig()
	if err != nil || !party.Enabled || party.AccountStart != 17000000 {
		t.Fatalf("initial party config=%+v err=%v", party, err)
	}
	if !server.loadPartySkillEnabled() {
		t.Fatal("initial party skill switch is off")
	}

	if err := os.WriteFile(paths.MailboxGuard(), []byte(`{"mailbox_bad_node_guard":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	poller.CheckNow()
	mailbox, err = server.loadMailboxGuardConfig()
	if err != nil || !mailbox.Enabled {
		t.Fatalf("updated mailbox config=%+v err=%v", mailbox, err)
	}

	if err := os.WriteFile(paths.MailboxGuard(), []byte(`{"mailbox_bad_node_guard":false,"legacy":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PartyCompatibility(), []byte(`{"enabled":false,"account_start":17001000,"account_end":17000000}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PartySkills(), []byte(`{"enabled":false,"max_skill_level":70,"skills":[{"enabled":true,"job":1,"skill_index":2,"state":3,"level":1,"legacy":true}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	poller.CheckNow()
	mailbox, err = server.loadMailboxGuardConfig()
	if err != nil || !mailbox.Enabled {
		t.Fatalf("invalid edit replaced mailbox snapshot: config=%+v err=%v", mailbox, err)
	}
	party, err = server.loadPartyCompatConfig()
	if err != nil || !party.Enabled || party.AccountStart != 17000000 {
		t.Fatalf("invalid edit replaced party snapshot: config=%+v err=%v", party, err)
	}
	if !server.loadPartySkillEnabled() {
		t.Fatal("invalid edit replaced party skill snapshot")
	}

	if err := os.Remove(paths.MailboxGuard()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.PartyCompatibility()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.PartySkills()); err != nil {
		t.Fatal(err)
	}
	poller.CheckNow()
	mailbox, err = server.loadMailboxGuardConfig()
	if err != nil || !mailbox.Enabled {
		t.Fatalf("deleted mailbox file replaced snapshot: config=%+v err=%v", mailbox, err)
	}
	party, err = server.loadPartyCompatConfig()
	if err != nil || !party.Enabled || party.AccountStart != 17000000 {
		t.Fatalf("deleted party file replaced snapshot: config=%+v err=%v", party, err)
	}
	if !server.loadPartySkillEnabled() {
		t.Fatal("deleted party skill file replaced snapshot")
	}
}
