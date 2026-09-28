package s4a21

import (
	"context"
	"fmt"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

// disjointStoreOpenTimeout bounds the create-store write. The server reply is
// observed asynchronously by the session drain, so the scheduler polls the
// published runtime status instead of blocking on it here.
const disjointStoreOpenTimeout = 10 * time.Second

// disjointStoreState is the adapter-owned view of one disassembler-machine
// attempt on a session. Field ownership stays inside this package; the action
// transport projects it onto shared.RuntimeStatus.
type disjointStoreState struct {
	createSent bool
	directAck  bool
	active     bool
	lastError  byte
}

func (s disjointStoreState) empty() bool {
	return !s.createSent && !s.directAck && !s.active && s.lastError == 0
}

// OpenDisjointStore opens the disassembler machine at the given position. The
// call only reports write failures; the server ack and CREATE notification are
// applied to the session state by dispatchPacket.
func (s *Session) OpenDisjointStore(ctx context.Context, cost uint32, x, y int16, direction int16) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is closed")
	}
	if s.selfUID == 0 {
		return fmt.Errorf("S4A21 session identity is not known")
	}
	body, err := protocol.CreateExpertJobStoreBody(protocol.ExpertJobStoreKindDisjointMachine, nil, int32(cost), x, y, direction)
	if err != nil {
		return err
	}
	s.disjointGuard.Lock()
	s.disjoint = disjointStoreState{createSent: true}
	s.disjointGuard.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, disjointStoreOpenTimeout)
		defer cancel()
	}
	if err := s.client.CreateExpertJobStore(ctx, body); err != nil {
		s.disjointGuard.Lock()
		s.disjoint = disjointStoreState{}
		s.disjointGuard.Unlock()
		return err
	}
	return nil
}

// CloseDisjointStore asks the server to remove the caller's machine. The server
// also removes it when the owner session ends, so a write failure here is not
// fatal for the scheduler's cleanup path.
func (s *Session) CloseDisjointStore(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, disjointStoreOpenTimeout)
		defer cancel()
	}
	return s.client.CloseExpertJobStore(ctx)
}

// DisjointStoreState snapshots the current machine attempt for the transport.
func (s *Session) DisjointStoreState() (sent, directAck, active bool, lastError byte) {
	if s == nil {
		return false, false, false, 0
	}
	s.disjointGuard.Lock()
	defer s.disjointGuard.Unlock()
	return s.disjoint.createSent, s.disjoint.directAck, s.disjoint.active, s.disjoint.lastError
}

// handleDisjointStorePacket applies server-side machine events to the session
// state. It runs on the session drain goroutine.
func (s *Session) handleDisjointStorePacket(packet protocol.Packet) {
	switch packet.Type {
	case protocol.CmdCreateExpertJobStore:
		ok, errCode, valid := protocol.ParseExpertJobStoreAck(packet.Body)
		if !valid {
			return
		}
		s.disjointGuard.Lock()
		if ok {
			s.disjoint.directAck = true
			s.disjoint.lastError = 0
		} else {
			s.disjoint.lastError = errCode
		}
		s.disjointGuard.Unlock()
	case protocol.NotiCreateExpertJobStore:
		uid, err := protocol.ParseExpertJobStoreCreateOwner(packet.Body)
		if err != nil || uid != s.selfUID {
			return
		}
		s.disjointGuard.Lock()
		s.disjoint.createSent = true
		s.disjoint.directAck = true
		s.disjoint.active = true
		s.disjoint.lastError = 0
		s.disjointGuard.Unlock()
	case protocol.NotiCloseExpertJobStore:
		uid, err := protocol.ParseExpertJobStoreCloseOwner(packet.Body)
		if err != nil || uid != s.selfUID {
			return
		}
		s.disjointGuard.Lock()
		s.disjoint = disjointStoreState{}
		s.disjointGuard.Unlock()
	}
}
