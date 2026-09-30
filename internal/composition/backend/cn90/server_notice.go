package cn90

import (
	"context"
	"fmt"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// serverNoticeActionTimeout bounds the write of one notice action. The ack and
// the broadcast are observed by the session drain and published through the
// session state.
const serverNoticeActionTimeout = 10 * time.Second

// serverNoticeState is the adapter-owned view of one server-notice trigger
// attempt. The action transport polls it for the server ack and for the
// broadcast the trigger produced.
type serverNoticeState struct {
	kind      shared.ServerNoticeKind
	sent      bool
	accepted  bool
	rejected  bool
	lastError byte
	broadcast bool
	event     shared.ServerNoticeEvent
}

// SetServerNoticeObserver installs the callback that receives every parsed
// 0x0056 broadcast. It returns a removal function.
func (s *Session) SetServerNoticeObserver(observer func(shared.ServerNoticeEvent)) func() {
	if s == nil {
		return func() {}
	}
	s.noticeGuard.Lock()
	s.noticeObserver = observer
	s.noticeGuard.Unlock()
	return func() {
		s.noticeGuard.Lock()
		s.noticeObserver = nil
		s.noticeGuard.Unlock()
	}
}

// UseLotteryItem opens one lottery box on the live session and records the
// attempt so the transport can wait for the server ack.
func (s *Session) UseLotteryItem(ctx context.Context, phase uint16, slot int16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	s.noticeGuard.Lock()
	s.notice = serverNoticeState{kind: shared.ServerNoticeLottery, sent: true}
	s.noticeGuard.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, serverNoticeActionTimeout)
		defer cancel()
	}
	if err := s.client.UseLotteryItem(ctx, phase, slot); err != nil {
		s.noticeGuard.Lock()
		s.notice = serverNoticeState{}
		s.noticeGuard.Unlock()
		return err
	}
	return nil
}

// UpgradeItem sends one reinforcement request on the live session.
func (s *Session) UpgradeItem(ctx context.Context, targetSlot int16, targetItemID int32, materialSlot, ticketSlot int16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	if targetSlot < 0 || targetItemID <= 0 {
		return fmt.Errorf("90CN upgrade target is incomplete")
	}
	s.noticeGuard.Lock()
	s.notice = serverNoticeState{kind: shared.ServerNoticeUpgrade, sent: true}
	s.noticeGuard.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, serverNoticeActionTimeout)
		defer cancel()
	}
	body := protocol.UpgradeItemBody(protocol.UpgradeMethodReinforce, targetSlot, targetItemID, materialSlot, ticketSlot, nil)
	if err := s.client.UpgradeItem(ctx, body); err != nil {
		s.noticeGuard.Lock()
		s.notice = serverNoticeState{}
		s.noticeGuard.Unlock()
		return err
	}
	return nil
}

// ServerNoticeState snapshots the current trigger attempt.
func (s *Session) ServerNoticeState() serverNoticeState {
	if s == nil {
		return serverNoticeState{}
	}
	s.noticeGuard.Lock()
	defer s.noticeGuard.Unlock()
	return s.notice
}

// handleServerNoticePacket applies acks and broadcasts to the session state.
// It runs on the session drain goroutine.
func (s *Session) handleServerNoticePacket(packet protocol.Packet) {
	switch packet.Type {
	case protocol.CmdUseLotteryItem, protocol.CmdUpgradeItem:
		accepted, valid := protocol.ParseServerNoticeAck(packet.Body)
		if !valid {
			return
		}
		s.noticeGuard.Lock()
		s.notice.sent = true
		s.notice.accepted = accepted
		s.notice.rejected = !accepted
		if !accepted && len(packet.Body) >= 2 {
			s.notice.lastError = packet.Body[1]
		}
		s.noticeGuard.Unlock()
	case protocol.NotiServerNotice:
		notice, ok := protocol.ParseServerNotice(packet.Body)
		if !ok {
			return
		}
		event := shared.ServerNoticeEvent{
			Kind: notice.Kind, UID: notice.UID, ItemID: notice.ItemID, Level: notice.Level,
			Success: notice.Success, Observed: time.Now(),
		}
		s.noticeGuard.Lock()
		if notice.UID == int(s.selfUID) {
			s.notice.broadcast = true
			s.notice.event = event
		}
		observer := s.noticeObserver
		s.noticeGuard.Unlock()
		if observer != nil {
			observer(event)
		}
	}
}
