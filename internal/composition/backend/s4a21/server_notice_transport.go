package s4a21

import (
	"context"
	"fmt"
	"time"

	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// server notice trigger pacing on the live session. The ack follows the
// request immediately when the server is idle; under a login storm the
// handler can queue for several seconds, so the wait is generous and a
// broadcast without an ack still counts as processed.
const (
	serverNoticeAckTimeout       = 15 * time.Second
	serverNoticeBroadcastTimeout = 4 * time.Second
	serverNoticePollInterval     = 100 * time.Millisecond
	serverNoticeDedupeWindow     = 30 * time.Second
	serverNoticeHistorySize      = 50
)

// noticeHistory keeps the fleet-wide recent 0x0056 broadcasts. Every session
// receives every broadcast, so events are deduplicated by content.
type noticeHistory struct {
	guard lockhub.Locker
	ring  []shared.ServerNoticeEvent
	seen  map[string]time.Time
}

func (h *noticeHistory) observe(event shared.ServerNoticeEvent) {
	if event.ItemID <= 0 {
		return
	}
	key := fmt.Sprintf("%s|%d|%d|%d|%t", event.Kind, event.UID, event.ItemID, event.Level, event.Success)
	now := time.Now()
	h.guard.Lock()
	defer h.guard.Unlock()
	if h.seen == nil {
		h.seen = make(map[string]time.Time)
	}
	if last, ok := h.seen[key]; ok && now.Sub(last) < serverNoticeDedupeWindow {
		return
	}
	if len(h.seen) > 256 {
		for existing, at := range h.seen {
			if now.Sub(at) >= serverNoticeDedupeWindow {
				delete(h.seen, existing)
			}
		}
	}
	h.seen[key] = now
	h.ring = append(h.ring, event)
	if len(h.ring) > serverNoticeHistorySize {
		h.ring = append([]shared.ServerNoticeEvent(nil), h.ring[len(h.ring)-serverNoticeHistorySize:]...)
	}
}

func (h *noticeHistory) recent() []shared.ServerNoticeEvent {
	h.guard.Lock()
	defer h.guard.Unlock()
	out := make([]shared.ServerNoticeEvent, len(h.ring))
	copy(out, h.ring)
	return out
}

// SetServerNoticeStock installs the offline stock planner the trigger uses to
// resolve the live slot layout.
func (t *ActionTransport) SetServerNoticeStock(stock ServerNoticeStock) {
	if t == nil {
		return
	}
	t.noticeStock = stock
}

// TriggerServerNotice performs one live notice action on the robot's session
// and waits for the game server acknowledgement. It implements
// shared.ServerNoticer.
func (t *ActionTransport) TriggerServerNotice(request shared.ServerNoticeTriggerRequest) (shared.ServerNoticeTriggerResult, error) {
	result := shared.ServerNoticeTriggerResult{
		Kind: request.Kind, UID: request.UID, CID: request.CID, At: time.Now(),
	}
	if t == nil {
		result.Reason = "transport_unavailable"
		return result, fmt.Errorf("S4A21 server notice transport is not configured")
	}
	if request.UID <= 0 || request.CID <= 0 {
		result.Reason = "identity_missing"
		return result, fmt.Errorf("S4A21 server notice requires uid and cid")
	}
	session, err := t.session(request.UID)
	if err != nil {
		result.Reason = "session_offline"
		return result, err
	}
	plan, ok, err := t.noticeStock.TriggerPlan(request.CID, request.Kind)
	if err != nil {
		result.Reason = "stock_probe_failed"
		return result, err
	}
	if !ok {
		result.Reason = "stock_missing"
		return result, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), serverNoticeAckTimeout)
	defer cancel()
	switch request.Kind {
	case shared.ServerNoticeLottery:
		sender, ok := session.(interface {
			UseLotteryItem(context.Context, uint16, int16) error
		})
		if !ok {
			result.Reason = "session_unsupported"
			return result, fmt.Errorf("S4A21 session does not support lottery items")
		}
		if err := sender.UseLotteryItem(ctx, 0, plan.Slot); err != nil {
			result.Reason = "send_failed"
			return result, err
		}
	case shared.ServerNoticeUpgrade:
		sender, ok := session.(interface {
			UpgradeItem(context.Context, int16, int32, int16, int16) error
		})
		if !ok {
			result.Reason = "session_unsupported"
			return result, fmt.Errorf("S4A21 session does not support item upgrades")
		}
		if err := sender.UpgradeItem(ctx, plan.Slot, plan.TargetItemID, plan.MaterialSlot, plan.TicketSlot); err != nil {
			result.Reason = "send_failed"
			return result, err
		}
	default:
		result.Reason = "kind_unsupported"
		return result, fmt.Errorf("S4A21 server notice kind %q is unsupported", request.Kind)
	}
	result.Sent = true
	state, ok := waitServerNoticeState(session, serverNoticeAckTimeout)
	if !ok {
		result.Reason = "ack_timeout"
		return result, nil
	}
	result.Accepted = state.accepted
	if state.broadcast {
		// A broadcast is proof the server processed the action even when the
		// ack was lost to a queued handler.
		result.Accepted = true
		result.Broadcast = true
		result.ItemID = state.event.ItemID
		result.Level = state.event.Level
		return result, nil
	}
	if state.rejected {
		result.Reason = fmt.Sprintf("rejected_%d", state.lastError)
		return result, nil
	}
	result.Reason = "accepted"
	if state.broadcast {
		result.Broadcast = true
		result.ItemID = state.event.ItemID
		result.Level = state.event.Level
		return result, nil
	}
	// The lottery pool is probabilistic: a valid open may not select a
	// notice-eligible reward. Give the broadcast a short window to arrive so the
	// result can distinguish "no notice" from "action rejected".
	if state, ok := waitServerNoticeBroadcast(session, serverNoticeBroadcastTimeout); ok && state.broadcast {
		result.Broadcast = true
		result.ItemID = state.event.ItemID
		result.Level = state.event.Level
	}
	return result, nil
}

// waitServerNoticeState polls the session until the ack or a broadcast lands.
func waitServerNoticeState(session shared.RobotSession, timeout time.Duration) (serverNoticeState, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if provider, ok := session.(interface{ ServerNoticeState() serverNoticeState }); ok {
			state := provider.ServerNoticeState()
			if state.accepted || state.rejected || state.broadcast {
				return state, true
			}
		}
		if time.Now().After(deadline) {
			return serverNoticeState{}, false
		}
		time.Sleep(serverNoticePollInterval)
	}
}

// waitServerNoticeBroadcast polls the session for the 0x0056 broadcast.
func waitServerNoticeBroadcast(session shared.RobotSession, timeout time.Duration) (serverNoticeState, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if provider, ok := session.(interface{ ServerNoticeState() serverNoticeState }); ok {
			state := provider.ServerNoticeState()
			if state.broadcast {
				return state, true
			}
		}
		if time.Now().After(deadline) {
			return serverNoticeState{}, false
		}
		time.Sleep(serverNoticePollInterval)
	}
}

// attachServerNoticeObserver subscribes the transport history to one session.
func (t *ActionTransport) attachServerNoticeObserver(session shared.RobotSession) {
	subscriber, ok := session.(interface {
		SetServerNoticeObserver(func(shared.ServerNoticeEvent)) func()
	})
	if !ok || subscriber == nil {
		return
	}
	subscriber.SetServerNoticeObserver(func(event shared.ServerNoticeEvent) {
		t.notices.observe(event)
	})
}

// RecentServerNotices returns the deduplicated fleet-wide broadcast history.
func (t *ActionTransport) RecentServerNotices() []shared.ServerNoticeEvent {
	if t == nil {
		return nil
	}
	return t.notices.recent()
}
