package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/s4a21"
)

const dungeonFollowerPrepareTimeout = 5 * time.Second

// followerTraceEnv enables bounded follower diagnostics when set to
// 1/true/yes/on. The trace records the party-follow TCP packets and mirror
// sends, which tells whether the server projects leader positions to dungeon
// followers or whether only the client UDP data plane carries them.
const followerTraceEnv = "S4A21_PARTY_FOLLOW_TRACE"

const followerTraceLimit = 512

func followerTraceEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(followerTraceEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *Session) traceFollower(format string, args ...any) {
	if s == nil || !followerTraceEnabled() {
		return
	}
	s.followerGuard.Lock()
	allowed := s.followerTraceCount < followerTraceLimit
	if allowed {
		s.followerTraceCount++
	}
	s.followerGuard.Unlock()
	if allowed {
		foundationlog.Robotf(format, args...)
	}
}

// EnableDungeonFollower explicitly opts a session into the small party
// follower workflow. It persists the tutorial-skip flag through the server
// protocol, accepts ordinary party invitations, and follows server-projected
// START_MAP notifications. It never sends MOVE_MAP or combat packets. The
// follower lifetime is owned by the session, so the caller's context is not
// used for cancellation.
func (s *Session) EnableDungeonFollower(_ context.Context) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("S4A21 session is not ready")
	}
	if s.selfUID == 0 || s.selfUID == 0xFFFF {
		return fmt.Errorf("S4A21 dungeon follower identity is unavailable")
	}
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

	followerCtx, followerCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.followerGuard.Lock()
	s.followerStarting = false
	s.followerCancel = followerCancel
	s.followerEvents = events
	s.followerDone = done
	s.followerGuard.Unlock()
	go s.followerLoop(followerCtx, events, done)
	// Party invitations must be handled immediately. Tutorial preparation is
	// only needed for dungeon entry and can be delayed by a busy server, so it
	// runs independently without blocking the invitation consumer.
	go s.prepareDungeonFollowerEventually(followerCtx)
	return nil
}

func (s *Session) prepareDungeonFollowerEventually(ctx context.Context) {
	for attempt := 1; attempt <= 3; attempt++ {
		prepareCtx, cancel := followerPrepareContext(ctx)
		err := s.prepareDungeonFollower(prepareCtx)
		cancel()
		if err == nil {
			return
		} else if attempt == 3 {
			foundationlog.Robotf("S4A21_PARTY_PREPARE_DEFERRED uid=%d attempts=%d err=%v\n", s.selfUID, attempt, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

// DisableDungeonFollower removes the opt-in packet consumer. It is useful to
// stop following while keeping the authenticated town session alive.
func (s *Session) DisableDungeonFollower() {
	s.stopDungeonFollower(true)
	s.followerGuard.Lock()
	s.partyActive = false
	s.partyID = 0
	s.partyLeaderUID = 0
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
			if packet.Type == protocol.NotiUserPosition {
				var deferred *protocol.Packet
				packet, deferred = coalesceFollowerPosition(packet, events)
				s.handleFollowerPacket(ctx, packet)
				if deferred != nil {
					s.handleFollowerPacket(ctx, *deferred)
				}
				continue
			}
			s.handleFollowerPacket(ctx, packet)
		}
	}
}

func coalesceFollowerPosition(latest protocol.Packet, events <-chan protocol.Packet) (protocol.Packet, *protocol.Packet) {
	for {
		select {
		case packet := <-events:
			if packet.Type == protocol.NotiUserPosition {
				latest = packet
				continue
			}
			return latest, &packet
		default:
			return latest, nil
		}
	}
}

func (s *Session) handleFollowerPacket(ctx context.Context, packet protocol.Packet) {
	switch packet.Type {
	case protocol.NotiRequestPeer:
		inviterUID, peerValue, ok := parsePartyInvite(packet)
		if ok && !s.PartyActive() {
			if err := s.client.AcceptPartyInvite(ctx, inviterUID, peerValue); err != nil {
				s.abortFollowerSession(ctx)
			} else {
				foundationlog.Robotf("S4A21_PARTY_INVITE_ACCEPTED uid=%d inviter_uid=%d\n", s.selfUID, inviterUID)
			}
		}
	case protocol.NotiPartyInfo:
		s.followerGuard.Lock()
		selfUID, currentPartyID, wasActive := s.selfUID, s.partyID, s.partyActive
		previousPartyID := currentPartyID
		s.followerGuard.Unlock()
		memberPartyID, leaderUID, clearedPartyIDs, ok := parsePartyInfoProjectionWithLeader(packet, selfUID)
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
			if active {
				s.partyLeaderUID = leaderUID
			} else {
				s.partyLeaderUID = 0
			}
			s.followerGuard.Unlock()
			if !active {
				s.dungeonStateGuard.Lock()
				s.dungeonState = nil
				s.dungeonStateGuard.Unlock()
			}
			if active && (!wasActive || memberPartyID != previousPartyID) {
				foundationlog.Robotf("S4A21_PARTY_JOINED uid=%d party_id=%d leader_uid=%d\n", selfUID, memberPartyID, leaderUID)
			} else if !active && wasActive {
				foundationlog.Robotf("S4A21_PARTY_LEFT uid=%d\n", selfUID)
			}
		}
	case protocol.NotiUserPosition:
		s.followLeaderPosition(ctx, packet)
	case protocol.NotiUserArea:
		s.followLeaderArea(ctx, packet)
	case protocol.NotiStartMap:
		s.acceptFollowerStartMap(ctx, packet)
	case protocol.NotiFinishLoading:
		s.commitFollowerFinishLoading(packet)
	}
}

func (s *Session) followLeaderPosition(ctx context.Context, packet protocol.Packet) {
	if packet.Command != 0 || len(packet.Body) < 9 {
		return
	}
	uid := binary.LittleEndian.Uint16(packet.Body[:2])
	s.followerGuard.Lock()
	active, leaderUID, selfUID := s.partyActive, s.partyLeaderUID, s.selfUID
	s.followerGuard.Unlock()
	if !active || leaderUID == 0 || uid != leaderUID || uid == selfUID {
		return
	}
	x := int16(binary.LittleEndian.Uint16(packet.Body[2:4]))
	y := int16(binary.LittleEndian.Uint16(packet.Body[4:6]))
	s.traceFollower("S4A21_FOLLOW_TRACE_POS uid=%d leader=%d x=%d y=%d dir=%d motion=%d\n",
		selfUID, uid, x, y, packet.Body[6], binary.LittleEndian.Uint16(packet.Body[7:9]))
	if err := s.client.SetUserPosition(ctx, x, y, packet.Body[6], binary.LittleEndian.Uint16(packet.Body[7:9])); err != nil {
		s.traceFollower("S4A21_FOLLOW_TRACE_POS_FAILED uid=%d leader=%d err=%v\n", selfUID, uid, err)
		s.abortFollowerSession(ctx)
	}
}

func (s *Session) followLeaderArea(ctx context.Context, packet protocol.Packet) {
	if packet.Command != 0 || len(packet.Body) < 8 {
		return
	}
	uid := binary.LittleEndian.Uint16(packet.Body[:2])
	s.followerGuard.Lock()
	active, leaderUID, selfUID := s.partyActive, s.partyLeaderUID, s.selfUID
	s.followerGuard.Unlock()
	if !active || leaderUID == 0 || uid != leaderUID || uid == selfUID || packet.Body[2] == 0xFF || packet.Body[3] == 0xFF {
		return
	}
	s.traceFollower("S4A21_FOLLOW_TRACE_AREA uid=%d leader=%d town=%d area=%d x=%d y=%d\n",
		selfUID, uid, packet.Body[2], packet.Body[3],
		int16(binary.LittleEndian.Uint16(packet.Body[4:6])), int16(binary.LittleEndian.Uint16(packet.Body[6:8])))
	if err := s.client.SetUserArea(ctx, packet.Body[2], packet.Body[3],
		int16(binary.LittleEndian.Uint16(packet.Body[4:6])),
		int16(binary.LittleEndian.Uint16(packet.Body[6:8]))); err != nil {
		s.traceFollower("S4A21_FOLLOW_TRACE_AREA_FAILED uid=%d leader=%d err=%v\n", selfUID, uid, err)
		s.abortFollowerSession(ctx)
	}
}

func (s *Session) acceptFollowerStartMap(ctx context.Context, packet protocol.Packet) {
	if packet.Command != 0 || !s.PartyActive() {
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
		s.traceFollower("S4A21_FOLLOW_TRACE_START_MAP uid=%d room=%d,%d party=%d body=%X\n",
			s.selfUID, packet.Body[0], packet.Body[1], len(packet.Body), packet.Body)
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

func (s *Session) abortFollowerQueue(events chan protocol.Packet) {
	if s == nil {
		return
	}
	// Serialize against DisableDungeonFollower. Once disable removes this
	// exact queue, a late dispatch must not close the surviving town session.
	s.followerGuard.Lock()
	if s.followerEvents != events {
		s.followerGuard.Unlock()
		return
	}
	s.followerEvents = nil
	s.followerGuard.Unlock()
	s.abortFollowerSession(context.Background())
}

func (s *Session) commitFollowerFinishLoading(packet protocol.Packet) {
	accepted := false
	s.dungeonStateGuard.Lock()
	if s.dungeonState != nil {
		accepted = s.dungeonState.AcceptFinishLoading(packet.Body) == nil
	}
	s.dungeonStateGuard.Unlock()
	if accepted {
		s.traceFollower("S4A21_FOLLOW_TRACE_FINISH_LOADING uid=%d body=%X\n", s.selfUID, packet.Body)
	}
}

func parsePartyInvite(packet protocol.Packet) (uint16, int32, bool) {
	if packet.Command != 0 || len(packet.Body) < 7 || packet.Body[2] != 0 {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint16(packet.Body[:2]),
		int32(binary.LittleEndian.Uint32(packet.Body[3:7])), true
}

func parsePartyInfoProjection(packet protocol.Packet, selfUID uint16) (uint16, []uint16, bool) {
	partyID, _, cleared, ok := parsePartyInfoProjectionWithLeader(packet, selfUID)
	return partyID, cleared, ok
}

func parsePartyInfoProjectionWithLeader(packet protocol.Packet, selfUID uint16) (uint16, uint16, []uint16, bool) {
	if packet.Command != 0 || len(packet.Body) < 2 || selfUID == 0 || selfUID == 0xFFFF {
		return 0, 0, nil, false
	}
	// Deployed A21 packs six bytes after the optional party name. Newer
	// ServerS4A21 builds pack eleven; require either shape to consume the
	// complete packet so roster UIDs cannot be matched at an arbitrary offset.
	shortPartyID, shortLeader, shortCleared, shortOK := parsePartyInfoBody(packet.Body, selfUID, 6)
	longPartyID, longLeader, longCleared, longOK := parsePartyInfoBody(packet.Body, selfUID, 11)
	if shortOK && longOK {
		if shortPartyID != longPartyID || shortLeader != longLeader || !samePartyIDs(shortCleared, longCleared) {
			return 0, 0, nil, false
		}
		return shortPartyID, shortLeader, shortCleared, true
	}
	if shortOK {
		return shortPartyID, shortLeader, shortCleared, true
	}
	return longPartyID, longLeader, longCleared, longOK
}

func parsePartyInfoBody(body []byte, selfUID uint16, infoTailLength int) (uint16, uint16, []uint16, bool) {
	blocks := int(binary.LittleEndian.Uint16(body[:2]))
	offset := 2
	memberPartyID := uint16(0)
	leaderUID := uint16(0)
	clearedPartyIDs := make([]uint16, 0, blocks)
	for i := 0; i < blocks; i++ {
		if len(body)-offset < 3 {
			return 0, 0, nil, false
		}
		partyID := binary.LittleEndian.Uint16(body[offset : offset+2])
		typ := body[offset+2]
		offset += 3
		switch typ {
		case 0, 1, 2, 5:
		case 3:
			clearedPartyIDs = append(clearedPartyIDs, partyID)
		default:
			return 0, 0, nil, false
		}
		// Types 0/1 contain the party-info block; type 0/2 contain the
		// eight member slots. We only need to skip their wire shape here.
		if typ == 0 || typ == 1 {
			if len(body)-offset < 1+4+infoTailLength {
				return 0, 0, nil, false
			}
			if body[offset] == 0 {
				offset++
				if len(body)-offset < 4 {
					return 0, 0, nil, false
				}
				nameLength := int(binary.LittleEndian.Uint32(body[offset : offset+4]))
				offset += 4
				if nameLength > len(body)-offset {
					return 0, 0, nil, false
				}
				offset += nameLength
			} else {
				offset++
			}
			if len(body)-offset < infoTailLength {
				return 0, 0, nil, false
			}
			offset += infoTailLength
		}
		if typ == 0 || typ == 2 {
			if len(body)-offset < 43 {
				return 0, 0, nil, false
			}
			for slot := 0; slot < 8; slot++ {
				uidOffset := offset + slot*5
				uid := binary.LittleEndian.Uint16(body[uidOffset : uidOffset+2])
				if slot == 0 && uid != 0 && uid != 0xFFFF {
					leaderUID = uid
				}
				if uid == selfUID {
					memberPartyID = partyID
				}
			}
			offset += 43
		}
		if typ == 5 {
			if len(body)-offset < 1 {
				return 0, 0, nil, false
			}
			offset++
		}
		if typ <= 2 {
			if len(body)-offset < 1 {
				return 0, 0, nil, false
			}
			offset++
		}
	}
	if offset != len(body) {
		return 0, 0, nil, false
	}
	return memberPartyID, leaderUID, clearedPartyIDs, true
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
