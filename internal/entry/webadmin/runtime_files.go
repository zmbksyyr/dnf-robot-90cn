package webadmin

import (
	"time"

	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/layout"
	foundationlog "robot/internal/foundation/log"
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
	if s == nil || s.cfg == nil || !s.isNativeBackend() {
		return nil
	}
	paths := layout.New(s.cfg.ConfigDir)
	return []filewatch.Entry{
		{Name: "mailbox_guard", Path: paths.MailboxGuard(), Apply: s.reloadMailboxGuardFile},
		{Name: "party_compatibility", Path: paths.PartyCompatibility(), Apply: s.reloadPartyCompatFile},
		{Name: "party_skills", Path: paths.PartySkills(), Apply: s.reloadPartySkillFile},
	}
}
