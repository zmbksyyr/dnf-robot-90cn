package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

const dungeonFollowerPrepareTimeout = 5 * time.Second

// EnableDungeonFollower explicitly opts a session into the small party
// follower workflow. It persists the tutorial-skip flag through the server
// protocol, accepts ordinary party invitations, and follows server-projected
// START_MAP notifications. It never sends MOVE_MAP or combat packets.
func (s *Session) EnableDungeonFollower(ctx context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is not ready")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	prepareCtx, cancel := followerPrepareContext(ctx)
	defer cancel()

	events := make(chan protocol.Packet, 64)
	s.followerGuard.Lock()
	if s.followerDone != nil || s.followerStarting {
		s.followerGuard.Unlock()
		return fmt.Errorf("S4A21 dungeon follower is already enabled")
	}
	s.followerStarting = true
	// Publish the bounded queue before preparing the tutorial flag. The server
	// can deliver an invitation immediately after the ACK; buffering it here
	// closes that race without doing protocol writes on the drain goroutine.
	s.followerEvents = events
	s.followerGuard.Unlock()

	if err := s.prepareDungeonFollower(prepareCtx); err != nil {
		s.followerGuard.Lock()
		s.followerStarting = false
		if s.followerEvents == events {
			s.followerEvents = nil
		}
		s.followerGuard.Unlock()
		return err
	}

	followerCtx, followerCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.followerGuard.Lock()
	s.followerStarting = false
	s.followerCancel = followerCancel
	s.followerEvents = events
	s.followerDone = done
	s.followerGuard.Unlock()
	go s.followerLoop(followerCtx, events, done)
	return nil
}

// DisableDungeonFollower removes the opt-in packet consumer. It is useful to
// stop following while keeping the authenticated town session alive.
func (s *Session) DisableDungeonFollower() {
	s.stopDungeonFollower(true)
	s.followerGuard.Lock()
	s.partyActive = false
	s.followerGuard.Unlock()
	s.dungeonStateGuard.Lock()
	s.dungeonState = nil
	s.dungeonStateGuard.Unlock()
}

func (s *Session) prepareDungeonFollower(ctx context.Context) error {
	ack := make(chan protocol.Packet, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Type != protocol.CmdChangeTutorialFlag {
			return
		}
		select {
		case ack <- packet:
		default:
		}
	})
	defer cleanup()
	if err := s.client.ChangeTutorialFlag(ctx, 31, 0); err != nil {
		return err
	}
	select {
	case <-s.Done():
		return fmt.Errorf("S4A21 session ended while preparing dungeon follower")
	case <-ctx.Done():
		return ctx.Err()
	case packet := <-ack:
		if packet.Command != 1 || len(packet.Body) == 0 || packet.Body[0] != 1 {
			return fmt.Errorf("S4A21 dungeon follower tutorial skip was rejected")
		}
		return nil
	}
}

func followerPrepareContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, dungeonFollowerPrepareTimeout)
}

func (s *Session) followerLoop(ctx context.Context, events <-chan protocol.Packet, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case packet := <-events:
			s.handleFollowerPacket(ctx, packet)
		}
	}
}

func (s *Session) handleFollowerPacket(ctx context.Context, packet protocol.Packet) {
	switch packet.Type {
	case protocol.NotiRequestPeer:
		inviterUID, ok := parsePartyInvite(packet)
		if ok && !s.PartyActive() {
			_ = s.client.AcceptPartyInvite(ctx, inviterUID)
		}
	case protocol.NotiPartyInfo:
		if active, ok := parsePartyInfoActive(packet); ok {
			s.followerGuard.Lock()
			s.partyActive = active
			s.followerGuard.Unlock()
			if !active {
				s.dungeonStateGuard.Lock()
				s.dungeonState = nil
				s.dungeonStateGuard.Unlock()
			}
		}
	case protocol.NotiStartMap:
		s.acceptFollowerStartMap(ctx, packet)
	case protocol.NotiFinishLoading:
		s.commitFollowerFinishLoading(packet)
	}
}

func (s *Session) acceptFollowerStartMap(ctx context.Context, packet protocol.Packet) {
	if packet.Command != 0 {
		return
	}
	s.dungeonStateGuard.Lock()
	if s.dungeonState == nil {
		s.dungeonState = &dungeonRunState{}
		if err := s.dungeonState.BeginFollowerEntry(); err != nil {
			s.dungeonState = nil
			s.dungeonStateGuard.Unlock()
			return
		}
	}
	state := s.dungeonState
	err := state.AcceptStartMap(packet.Body)
	s.dungeonStateGuard.Unlock()
	if err == nil {
		// This is deliberately outside the drain callback and outside the
		// dungeon state lock. The server expects a follower to acknowledge
		// loading, but never expects a follower MOVE_MAP.
		_ = s.client.FinishLoading(ctx)
	}
}

func (s *Session) commitFollowerFinishLoading(packet protocol.Packet) {
	s.dungeonStateGuard.Lock()
	defer s.dungeonStateGuard.Unlock()
	if s.dungeonState != nil {
		_ = s.dungeonState.AcceptFinishLoading(packet.Body)
	}
}

func parsePartyInvite(packet protocol.Packet) (uint16, bool) {
	if packet.Command != 0 || len(packet.Body) < 3 || packet.Body[2] != 0 {
		return 0, false
	}
	return binary.LittleEndian.Uint16(packet.Body[:2]), true
}

func parsePartyInfoActive(packet protocol.Packet) (bool, bool) {
	if packet.Command != 0 || len(packet.Body) < 5 {
		return false, false
	}
	blocks := int(binary.LittleEndian.Uint16(packet.Body[:2]))
	if blocks == 0 {
		return false, false
	}
	offset := 2
	active := false
	for i := 0; i < blocks; i++ {
		if len(packet.Body)-offset < 3 {
			return false, false
		}
		typ := packet.Body[offset+2]
		offset += 3
		switch typ {
		case 0, 1, 2:
			active = true
		case 3:
			// Clear blocks do not contribute to an active party state.
		default:
			return false, false
		}
		// Types 0/1 contain the party-info block; type 0/2 contain the
		// eight member slots. We only need to skip their wire shape here.
		if typ == 0 || typ == 1 {
			if len(packet.Body)-offset < 13 {
				return false, false
			}
			if packet.Body[offset] == 0 {
				offset++
				if len(packet.Body)-offset < 4 {
					return false, false
				}
				offset += 4
			} else {
				offset++
			}
			offset += 11
		}
		if typ == 0 || typ == 2 {
			if len(packet.Body)-offset < 43 {
				return false, false
			}
			offset += 43
		}
		if typ <= 2 {
			if len(packet.Body)-offset < 1 {
				return false, false
			}
			offset++
		}
	}
	return active, true
}

func (s *Session) stopDungeonFollower(wait bool) {
	if s == nil {
		return
	}
	s.followerGuard.Lock()
	cancel := s.followerCancel
	done := s.followerDone
	s.followerCancel = nil
	s.followerEvents = nil
	if wait {
		s.followerDone = nil
	}
	s.followerGuard.Unlock()
	if cancel != nil {
		cancel()
	}
	if wait && done != nil {
		<-done
		s.followerGuard.Lock()
		if s.followerDone == done {
			s.followerDone = nil
		}
		s.followerGuard.Unlock()
	}
}

// PartyActive is deliberately an optional adapter observation, not a shared
// party capability. It lets the scheduler suppress town actions for an opted
// in follower while the server owns dungeon movement.
func (s *Session) PartyActive() bool {
	if s == nil {
		return false
	}
	s.followerGuard.Lock()
	active := s.partyActive
	s.followerGuard.Unlock()
	return active
}
