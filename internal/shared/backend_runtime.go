package shared

import (
	"context"
	"fmt"
	"time"
)

// PersistenceStatus is backend-neutral. Engine-specific details stay inside
// the selected backend's inspector.
type PersistenceStatus struct {
	OK             bool      `json:"ok"`
	Engine         string    `json:"engine,omitempty"`
	Host           string    `json:"host,omitempty"`
	Port           int       `json:"port,omitempty"`
	Database       string    `json:"database,omitempty"`
	User           string    `json:"user,omitempty"`
	Target         string    `json:"target,omitempty"`
	Writable       bool      `json:"writable"`
	OpenConns      int       `json:"open_conns,omitempty"`
	InUse          int       `json:"in_use,omitempty"`
	Idle           int       `json:"idle,omitempty"`
	Error          string    `json:"error,omitempty"`
	CheckedAt      time.Time `json:"checked_at"`
	LatencyMS      int64     `json:"latency_ms"`
	SelectVerified bool      `json:"select_verified"`
}

type PersistenceInspector interface {
	Status(context.Context) PersistenceStatus
}

type SystemAnnouncementRequest struct {
	Kind       string
	Message    string
	SenderName string
	SenderID   uint16
	At         time.Time
}

type SystemAnnouncementResult struct {
	Online       int       `json:"online"`
	AuctionKinds int       `json:"auction_kinds"`
	Kind         string    `json:"kind"`
	Message      string    `json:"message"`
	Sent         bool      `json:"sent"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SystemAnnouncer interface {
	Announce(context.Context, SystemAnnouncementRequest) (SystemAnnouncementResult, error)
}

func SystemAnnouncementMessageAt(now time.Time, online, auctionKinds int) string {
	if online < 0 {
		online = 0
	}
	if auctionKinds < 0 {
		auctionKinds = 0
	}
	return fmt.Sprintf("%s 在线人数%d；拍卖行%d类", now.Format("15:04:05"), online, auctionKinds)
}
