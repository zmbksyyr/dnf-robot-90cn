package scheduler

import (
	"errors"
	"testing"
	"time"

	"robot/internal/shared"
)

func TestSystemAnnouncementMessage(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 34, 56, 0, time.Local)
	if got := SystemAnnouncementMessageAt(now, 123, 456); got != "12:34:56 在线人数123；拍卖行456类" {
		t.Fatalf("message=%q", got)
	}
	if got := SystemAnnouncementMessageAt(now, -1, -2); got != "12:34:56 在线人数0；拍卖行0类" {
		t.Fatalf("negative message=%q", got)
	}
}

func TestUnsupportedAnnouncementDoesNotDereferencePersistence(t *testing.T) {
	m := NewRobotManager(nil, nil, nil)
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	_, err := m.SystemAnnouncement()
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendID("test") {
		t.Fatalf("announcement error = %T %v", err, err)
	}
}
