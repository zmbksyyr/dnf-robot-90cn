package scheduler

import (
	"context"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

const (
	systemAnnouncementName     = "系统"
	systemAnnouncementSenderID = uint16(1)
	systemAnnouncementInterval = time.Minute

	SystemAnnouncementWebNoticeSingle = "web_notice_single"
)

type AnnouncementResult = shared.SystemAnnouncementResult

func (m *RobotManager) SystemAnnouncement() (AnnouncementResult, error) {
	return m.SystemAnnouncementAs(SystemAnnouncementWebNoticeSingle)
}

func (m *RobotManager) SystemAnnouncementAs(kind string) (AnnouncementResult, error) {
	return m.MonitorAnnouncement(kind, "")
}

func (m *RobotManager) MonitorAnnouncement(kind, message string) (AnnouncementResult, error) {
	now := time.Now()
	if err := m.requireBackendCapability(shared.CapabilitySystemAnnouncement); err != nil {
		return AnnouncementResult{Kind: kind, UpdatedAt: now}, err
	}
	if m.systemAnnouncer == nil {
		return AnnouncementResult{Kind: kind, UpdatedAt: now}, shared.UnsupportedCapabilityError{
			Backend: m.backendInfo.ID, Operation: shared.CapabilitySystemAnnouncement, Reason: "system announcement port is not configured",
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return m.systemAnnouncer.Announce(ctx, shared.SystemAnnouncementRequest{
		Kind: kind, Message: message, SenderName: systemAnnouncementName, SenderID: systemAnnouncementSenderID, At: now,
	})
}

func (s *RobotSupervisor) sendSystemAnnouncementIfDue(now time.Time, rc robotconfig.RuntimeConfig) {
	if !rc.AutoSystemAnnouncement || !s.manager.supportsBackendCapability(shared.CapabilitySystemAnnouncement) {
		return
	}
	if s.nextAnnouncement.IsZero() {
		s.nextAnnouncement = now.Add(systemAnnouncementInterval)
		return
	}
	if now.Before(s.nextAnnouncement) {
		return
	}
	s.nextAnnouncement = now.Add(systemAnnouncementInterval)
	res, err := s.manager.SystemAnnouncement()
	if err != nil {
		robotLogf("[Announcement] system failed online=%d auction_kinds=%d err=%v\n", res.Online, res.AuctionKinds, err)
		return
	}
	robotLogf("[Announcement] system sent online=%d auction_kinds=%d message=%s\n", res.Online, res.AuctionKinds, res.Message)
}

func SystemAnnouncementMessageAt(now time.Time, online, auctionKinds int) string {
	return shared.SystemAnnouncementMessageAt(now, online, auctionKinds)
}
