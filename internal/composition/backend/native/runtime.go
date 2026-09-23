package native

import (
	"context"
	"fmt"
	"time"

	"robot/internal/capability/keypair"
	"robot/internal/foundation/config"
	"robot/internal/foundation/dbstatus"
	"robot/internal/shared"
)

type GameCommandGate struct {
	Config *config.SysConfig
}

func (g GameCommandGate) Check() error {
	status := keypair.CurrentStatus(g.Config)
	if status.GameValid {
		return nil
	}
	if status.Error != "" {
		return fmt.Errorf("RSA key unavailable: %s", status.Error)
	}
	if status.KeyReason != "" {
		return fmt.Errorf("RSA key unavailable: %s", status.KeyReason)
	}
	return fmt.Errorf("RSA key unavailable")
}

type PersistenceInspector struct {
	Database dbstatus.Database
	Config   *config.SysConfig
}

func (i PersistenceInspector) Status(_ context.Context) shared.PersistenceStatus {
	status := dbstatus.Check(i.Database, i.Config)
	return shared.PersistenceStatus{
		OK: status.OK, Engine: "mysql", Host: status.Host, Port: status.Port, Database: status.Database, User: status.User,
		Target: status.Target, Writable: status.OK,
		OpenConns: status.OpenConns, InUse: status.InUse, Idle: status.Idle,
		Error: status.Error, CheckedAt: status.CheckedAt, LatencyMS: status.LatencyMS,
		SelectVerified: status.SelectVerified,
	}
}

type AnnouncementSender interface {
	SendMonitorAnnouncement(kind, message, name string, senderID uint16) error
}

type SystemAnnouncer struct {
	Database dbstatus.Database
	Sender   AnnouncementSender
}

func (a SystemAnnouncer) Announce(ctx context.Context, request shared.SystemAnnouncementRequest) (shared.SystemAnnouncementResult, error) {
	now := request.At
	if now.IsZero() {
		now = time.Now()
	}
	result := shared.SystemAnnouncementResult{Kind: request.Kind, UpdatedAt: now}
	if a.Database == nil {
		return result, fmt.Errorf("native announcement database is not configured")
	}
	if a.Sender == nil {
		return result, fmt.Errorf("native announcement sender is not configured")
	}
	if err := a.Database.QueryRowContext(ctx, "SELECT COUNT(*) FROM taiwan_login.login_account_3 WHERE login_status=1").Scan(&result.Online); err != nil {
		return result, fmt.Errorf("query system online count: %w", err)
	}
	if err := a.Database.QueryRowContext(ctx, "SELECT COUNT(DISTINCT item_id) FROM taiwan_cain_auction_gold.auction_main").Scan(&result.AuctionKinds); err != nil {
		return result, fmt.Errorf("query auction kind count: %w", err)
	}
	result.Message = request.Message
	if result.Message == "" {
		result.Message = shared.SystemAnnouncementMessageAt(now, result.Online, result.AuctionKinds)
	}
	if err := a.Sender.SendMonitorAnnouncement(request.Kind, result.Message, request.SenderName, request.SenderID); err != nil {
		return result, err
	}
	result.Sent = true
	return result, nil
}
