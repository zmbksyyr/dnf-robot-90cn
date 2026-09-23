package webadmin

import (
	"time"

	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

func (s *Server) startRuntimeFileWatcher() func() {
	if s == nil || s.cfg == nil {
		return func() {}
	}
	entries := s.runtimeFileEntries()
	if len(entries) == 0 {
		return func() {}
	}
	poller := filewatch.New(time.Second, entries, func(entry filewatch.Entry, err error) {
		foundationlog.Robotf("[WEB_RUNTIME_FILE] rejected name=%s path=%s err=%v\n", entry.Name, entry.Path, err)
	})
	poller.Start()
	return poller.Close
}

func (s *Server) runtimeFileEntries() []filewatch.Entry {
	if s == nil || s.cfg == nil {
		return nil
	}
	paths := layout.New(s.cfg.ConfigDir)
	var entries []filewatch.Entry
	if s.supportsBackendCapability(shared.CapabilityCompatibility) {
		entries = append(entries, filewatch.Entry{Name: "mailbox_guard", Path: paths.MailboxGuard(), Apply: s.reloadMailboxGuardFile})
	}
	if s.supportsBackendCapability(shared.CapabilityParty) {
		entries = append(entries, filewatch.Entry{Name: "party_compatibility", Path: paths.PartyCompatibility(), Apply: s.reloadPartyCompatFile})
	}
	if s.supportsBackendCapability(shared.CapabilitySkill) {
		entries = append(entries, filewatch.Entry{Name: "party_skills", Path: paths.PartySkills(), Apply: s.reloadPartySkillFile})
	}
	return entries
}
