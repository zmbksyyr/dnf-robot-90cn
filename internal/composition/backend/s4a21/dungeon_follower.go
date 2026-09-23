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
	if s.selfUID == 0 || s.selfUID == 0xFFFF {
		return fmt.Errorf("S4A21 dungeon follower identity is unavailable")
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
	s.partyID = 0
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
		case <-s.Done():
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
			if err := s.client.AcceptPartyInvite(ctx, inviterUID); err != nil {
				s.abortFollowerSession(ctx)
			}
		}
	case protocol.NotiPartyInfo:
		s.followerGuard.Lock()
		selfUID, currentPartyID := s.selfUID, s.partyID
		s.followerGuard.Unlock()
		memberPartyID, clearedPartyIDs, ok := parsePartyInfoProjection(packet, selfUID)
		if ok {
			active, changed := false, false
			if memberPartyID != 0 {
				active, changed = true, true
				currentPartyID = memberPartyID
			} else if containsPartyID(clearedPartyIDs, currentPartyID) {
				changed = true
				currentPartyID = 0
			}
			if !changed {
				return
			}
			s.followerGuard.Lock()
			s.partyActive = active
			s.partyID = currentPartyID
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
		if err := s.client.FinishLoading(ctx); err != nil {
			s.abortFollowerSession(ctx)
		}
	}
}

func (s *Session) abortFollowerSession(ctx context.Context) {
	if s == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		_ = s.client.Close()
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

func parsePartyInfoProjection(packet protocol.Packet, selfUID uint16) (uint16, []uint16, bool) {
	if packet.Command != 0 || len(packet.Body) < 2 || selfUID == 0 || selfUID == 0xFFFF {
		return 0, nil, false
	}
	// Deployed A21 packs six bytes after the optional party name. Newer
	// ServerS4A21 builds pack eleven; require either shape to consume the
	// complete packet so roster UIDs cannot be matched at an arbitrary offset.
	shortPartyID, shortCleared, shortOK := parsePartyInfoBody(packet.Body, selfUID, 6)
	longPartyID, longCleared, longOK := parsePartyInfoBody(packet.Body, selfUID, 11)
	if shortOK && longOK {
		if shortPartyID != longPartyID || !samePartyIDs(shortCleared, longCleared) {
			return 0, nil, false
		}
		return shortPartyID, shortCleared, true
	}
	if shortOK {
		return shortPartyID, shortCleared, true
	}
	return longPartyID, longCleared, longOK
}

func parsePartyInfoBody(body []byte, selfUID uint16, infoTailLength int) (uint16, []uint16, bool) {
	blocks := int(binary.LittleEndian.Uint16(body[:2]))
	offset := 2
	memberPartyID := uint16(0)
	clearedPartyIDs := make([]uint16, 0, blocks)
	for i := 0; i < blocks; i++ {
		if len(body)-offset < 3 {
			return 0, nil, false
		}
		partyID := binary.LittleEndian.Uint16(body[offset : offset+2])
		typ := body[offset+2]
		offset += 3
		switch typ {
		case 0, 1, 2, 5:
		case 3:
			clearedPartyIDs = append(clearedPartyIDs, partyID)
		default:
			return 0, nil, false
		}
		// Types 0/1 contain the party-info block; type 0/2 contain the
		// eight member slots. We only need to skip their wire shape here.
		if typ == 0 || typ == 1 {
			if len(body)-offset < 1+4+infoTailLength {
				return 0, nil, false
			}
			if body[offset] == 0 {
				offset++
				if len(body)-offset < 4 {
					return 0, nil, false
				}
				nameLength := int(binary.LittleEndian.Uint32(body[offset : offset+4]))
				offset += 4
				if nameLength > len(body)-offset {
					return 0, nil, false
				}
				offset += nameLength
			} else {
				offset++
			}
			if len(body)-offset < infoTailLength {
				return 0, nil, false
			}
			offset += infoTailLength
		}
		if typ == 0 || typ == 2 {
			if len(body)-offset < 43 {
				return 0, nil, false
			}
			for slot := 0; slot < 8; slot++ {
				uidOffset := offset + slot*5
				if binary.LittleEndian.Uint16(body[uidOffset:uidOffset+2]) == selfUID {
					memberPartyID = partyID
				}
			}
			offset += 43
		}
		if typ == 5 {
			if len(body)-offset < 1 {
				return 0, nil, false
			}
			offset++
		}
		if typ <= 2 {
			if len(body)-offset < 1 {
				return 0, nil, false
			}
			offset++
		}
	}
	if offset != len(body) {
		return 0, nil, false
	}
	return memberPartyID, clearedPartyIDs, true
}

func samePartyIDs(left, right []uint16) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func containsPartyID(ids []uint16, want uint16) bool {
	if want == 0 {
		return false
	}
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
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
