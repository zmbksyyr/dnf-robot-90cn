package s4a21

import (
	"context"
	"fmt"
	"time"

	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

// expertJobStoreOpenTimeout bounds a create/close write. Server replies are
// observed asynchronously by the session drain, so the scheduler polls the
// published runtime status instead of blocking on them here.
const expertJobStoreOpenTimeout = 10 * time.Second

// expertJobStoreState is the adapter-owned view of one expert-job stall
// attempt (disassembler machine or enchanter stall). Field ownership stays
// inside this package; the action transport projects it onto
// shared.RuntimeStatus.
type expertJobStoreState struct {
	kind       shared.ExpertJobStoreKind
	createSent bool
	directAck  bool
	active     bool
	lastError  byte
}

// wireExpertJobStoreKind maps the shared stall kind to the A21 wire byte.
func wireExpertJobStoreKind(kind shared.ExpertJobStoreKind) (byte, error) {
	switch kind {
	case shared.ExpertJobStoreDisjoint:
		return protocol.ExpertJobStoreKindDisjointMachine, nil
	case shared.ExpertJobStoreEnchant:
		return protocol.ExpertJobStoreKindEnchantShop, nil
	default:
		return 0, fmt.Errorf("unsupported expert job store kind %d", kind)
	}
}

// sharedExpertJobStoreKind maps an A21 wire kind back to the shared enum.
func sharedExpertJobStoreKind(wire byte) (shared.ExpertJobStoreKind, error) {
	switch wire {
	case protocol.ExpertJobStoreKindDisjointMachine:
		return shared.ExpertJobStoreDisjoint, nil
	case protocol.ExpertJobStoreKindEnchantShop:
		return shared.ExpertJobStoreEnchant, nil
	default:
		return shared.ExpertJobStoreNone, fmt.Errorf("unknown expert job store kind %d", wire)
	}
}

// OpenExpertJobStore opens the requested stall at the given position. The call
// only reports write failures; the server ack and CREATE notification are
// applied to the session state by dispatchPacket.
func (s *Session) OpenExpertJobStore(ctx context.Context, kind shared.ExpertJobStoreKind, cost uint32, x, y int16, direction int16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is closed")
	}
	if s.selfUID == 0 {
		return fmt.Errorf("S4A21 session identity is not known")
	}
	wireKind, err := wireExpertJobStoreKind(kind)
	if err != nil {
		return err
	}
	body, err := protocol.CreateExpertJobStoreBody(wireKind, nil, int32(cost), x, y, direction)
	if err != nil {
		return err
	}
	s.storeGuard.Lock()
	s.store = expertJobStoreState{kind: kind, createSent: true}
	s.storeGuard.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, expertJobStoreOpenTimeout)
		defer cancel()
	}
	if err := s.client.CreateExpertJobStore(ctx, body); err != nil {
		s.storeGuard.Lock()
		s.store = expertJobStoreState{}
		s.storeGuard.Unlock()
		return err
	}
	return nil
}

// CloseExpertJobStore asks the server to remove the caller's stall. The server
// also removes it when the owner session ends, so a write failure here is not
// fatal for the scheduler's cleanup path.
func (s *Session) CloseExpertJobStore(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, expertJobStoreOpenTimeout)
		defer cancel()
	}
	return s.client.CloseExpertJobStore(ctx)
}

// ExpertJobStoreState snapshots the current stall attempt for the transport.
func (s *Session) ExpertJobStoreState() (kind shared.ExpertJobStoreKind, sent, directAck, active bool, lastError byte) {
	if s == nil {
		return shared.ExpertJobStoreNone, false, false, false, 0
	}
	s.storeGuard.Lock()
	defer s.storeGuard.Unlock()
	return s.store.kind, s.store.createSent, s.store.directAck, s.store.active, s.store.lastError
}

// handleExpertJobStorePacket applies server-side stall events to the session
// state. It runs on the session drain goroutine.
func (s *Session) handleExpertJobStorePacket(packet protocol.Packet) {
	switch packet.Type {
	case protocol.CmdCreateExpertJobStore:
		ok, errCode, valid := protocol.ParseExpertJobStoreAck(packet.Body)
		if !valid {
			return
		}
		s.storeGuard.Lock()
		if ok {
			s.store.directAck = true
			s.store.lastError = 0
		} else {
			s.store.lastError = errCode
		}
		s.storeGuard.Unlock()
	case protocol.NotiCreateExpertJobStore:
		uid, err := protocol.ParseExpertJobStoreCreateOwner(packet.Body)
		if err != nil || uid != s.selfUID {
			return
		}
		kind := s.store.kind
		if wireKind, ok := notificationStoreKind(packet.Body); ok {
			if mapped, err := sharedExpertJobStoreKind(wireKind); err == nil {
				kind = mapped
			}
		}
		s.storeGuard.Lock()
		s.store = expertJobStoreState{kind: kind, createSent: true, directAck: true, active: true}
		s.storeGuard.Unlock()
	case protocol.NotiCloseExpertJobStore:
		uid, err := protocol.ParseExpertJobStoreCloseOwner(packet.Body)
		if err != nil || uid != s.selfUID {
			return
		}
		s.storeGuard.Lock()
		s.store = expertJobStoreState{}
		s.storeGuard.Unlock()
	}
}

// notificationStoreKind reads the store kind byte at the notification head.
func notificationStoreKind(body []byte) (byte, bool) {
	if len(body) < 1 {
		return 0, false
	}
	return body[0], true
}
