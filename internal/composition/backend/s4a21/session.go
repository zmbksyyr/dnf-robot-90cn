package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

type SessionFactory struct {
	Address string
	Timeout time.Duration
}

const sessionKeepaliveInterval = 5 * time.Second
const townAreaTransitionTimeout = 5 * time.Second
const guildInviteQueueSize = 4

// sessionKeepaliveAckTimeout is how long the server may stop answering
// CHECK_CONNECTION before the session is treated as half-open. Enforcement
// only starts after at least one keepalive ACK has been observed, so a server
// that answers only the login probe never triggers reconnect churn.
var sessionKeepaliveAckTimeout = 6 * sessionKeepaliveInterval

type Session struct {
	client             *protocol.Client
	cancel             context.CancelFunc
	done               chan struct{}
	keepaliveDone      chan struct{}
	guildInviteEvents  chan protocol.GuildInvite
	packetObserverLock lockhub.RWLocker
	packetObservers    []*packetObserverRegistration
	terminationGuard   lockhub.Locker
	termination        func()
	followerGuard      lockhub.Locker
	followerCancel     context.CancelFunc
	followerEvents     chan protocol.Packet
	followerDone       chan struct{}
	followerStarting   bool
	selfUID            uint16
	partyID            uint16
	partyLeaderUID     uint16
	partyActive        bool
	keepaliveGuard     lockhub.Locker
	lastCheckAck       time.Time
	checkAckSeen       bool
	disjointGuard      lockhub.Locker
	disjoint           disjointStoreState
}

type packetObserverRegistration struct {
	fn func(protocol.Packet)
}

func (f SessionFactory) OpenSession(ctx context.Context, request shared.OpenSessionRequest) (shared.RobotSession, error) {
	if strings.TrimSpace(request.AccountName) == "" {
		return nil, fmt.Errorf("S4A21 session account name is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	openCtx, cancelOpen := context.WithTimeout(ctx, timeout)
	defer cancelOpen()
	client, err := protocol.Dial(openCtx, f.Address)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = client.Close()
		}
	}()
	if err := client.Login(openCtx, request.AccountName, request.PasswordHash); err != nil {
		return nil, err
	}
	if err := waitFor(openCtx, client, protocol.CmdLogin, 1); err != nil {
		return nil, fmt.Errorf("S4A21 login: %w", err)
	}
	if err := client.SelectCharacter(openCtx, request.CharacterSlot); err != nil {
		return nil, err
	}
	selectPacket, err := waitPacket(openCtx, client, protocol.CmdSelectCharacter, 1)
	if err != nil {
		return nil, fmt.Errorf("S4A21 select character: %w", err)
	}
	selfUID, identityErr := protocol.SelectCharacterUID(selectPacket.Body)
	if request.EnablePartyDungeonFollower && identityErr != nil {
		return nil, fmt.Errorf("S4A21 follower identity: %w", identityErr)
	}
	// Ordinary sessions only need the identity for expert-job store ownership;
	// SelectCharacterUID already reports zero when the projection is missing.
	if request.EnablePartyDungeonFollower {
		client.SetPartyIdentity(selfUID)
		if err := client.RegisterUDPEndpoint(openCtx); err != nil {
			return nil, err
		}
	}
	if err := client.CheckConnection(openCtx); err != nil {
		return nil, err
	}
	if err := waitFor(openCtx, client, protocol.CmdCheckConnection, 1); err != nil {
		return nil, fmt.Errorf("S4A21 session readiness: %w", err)
	}
	if request.InitialTownKnown {
		if err := initializeTownPresence(openCtx, client, request); err != nil {
			return nil, err
		}
	}
	runCtx, cancel := context.WithCancel(context.Background())
	session := &Session{
		client: client, cancel: cancel, done: make(chan struct{}), keepaliveDone: make(chan struct{}),
		guildInviteEvents: make(chan protocol.GuildInvite, guildInviteQueueSize), selfUID: selfUID,
	}
	go session.drain(runCtx)
	go session.keepalive(runCtx)
	if request.EnablePartyDungeonFollower {
		go session.prepareOptionalDungeonFollower(runCtx)
	}
	closeOnError = false
	return session, nil
}

func (s *Session) prepareOptionalDungeonFollower(ctx context.Context) {
	if err := s.EnableDungeonFollower(ctx); err != nil {
		select {
		case <-s.Done():
			return
		default:
			foundationlog.Robotf("S4A21_PARTY_PREPARE_FAILED uid=%d err=%v\n", s.selfUID, err)
		}
	}
}

func initializeTownPresence(ctx context.Context, client *protocol.Client, request shared.OpenSessionRequest) error {
	if request.InitialVillage < 0 || request.InitialVillage > 255 || request.InitialArea < 0 || request.InitialArea > 255 {
		return fmt.Errorf("S4A21 initial town out of range: %d/%d", request.InitialVillage, request.InitialArea)
	}
	if request.InitialX < math.MinInt16 || request.InitialX > math.MaxInt16 || request.InitialY < math.MinInt16 || request.InitialY > math.MaxInt16 {
		return fmt.Errorf("S4A21 initial town position out of range: %d,%d", request.InitialX, request.InitialY)
	}
	if err := client.SetUserArea(ctx, byte(request.InitialVillage), byte(request.InitialArea), int16(request.InitialX), int16(request.InitialY)); err != nil {
		return fmt.Errorf("S4A21 enter town: %w", err)
	}
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return fmt.Errorf("S4A21 enter town confirmation: %w", err)
		}
		if packet.Type != protocol.NotiUserArea || len(packet.Body) < 8 {
			continue
		}
		if int(packet.Body[2]) != request.InitialVillage || int(packet.Body[3]) != request.InitialArea ||
			int16(binary.LittleEndian.Uint16(packet.Body[4:6])) != int16(request.InitialX) ||
			int16(binary.LittleEndian.Uint16(packet.Body[6:8])) != int16(request.InitialY) {
			continue
		}
		return nil
	}
}

func (s *Session) MoveTown(ctx context.Context, intent shared.TownMoveIntent) error {
	return s.client.SetUserPosition(ctx, intent.X, intent.Y, intent.Direction, intent.Motion)
}

func (s *Session) MoveTownArea(ctx context.Context, intent shared.TownAreaMoveIntent) error {
	if intent.Village < 0 || intent.Village > 255 || intent.Area < 0 || intent.Area > 255 {
		return fmt.Errorf("S4A21 town area out of range: %d/%d", intent.Village, intent.Area)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, townAreaTransitionTimeout)
		defer cancel()
	}
	confirmed := make(chan struct{}, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Type != protocol.NotiUserArea || len(packet.Body) < 8 ||
			int(packet.Body[2]) != intent.Village ||
			int(packet.Body[3]) != intent.Area ||
			int16(binary.LittleEndian.Uint16(packet.Body[4:6])) != intent.X ||
			int16(binary.LittleEndian.Uint16(packet.Body[6:8])) != intent.Y {
			return
		}
		select {
		case confirmed <- struct{}{}:
		default:
		}
	})
	defer cleanup()
	if err := s.client.SetUserArea(ctx, byte(intent.Village), byte(intent.Area), intent.X, intent.Y); err != nil {
		return err
	}
	select {
	case <-confirmed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-s.Done():
		return fmt.Errorf("S4A21 session ended during town area transition")
	}
}

func (s *Session) Shout(ctx context.Context, intent shared.ShoutIntent) error {
	mode := byte(0)
	switch intent.Channel {
	case shared.ShoutChannelArea:
		mode = 3
	case shared.ShoutChannelParty:
		return shared.UnsupportedCapabilityError{Backend: BackendID, Operation: shared.CapabilityShout, Reason: "S4A21 party-recipient message mode is not verified"}
	case shared.ShoutChannelWorld:
		return shared.UnsupportedCapabilityError{Backend: BackendID, Operation: shared.CapabilityWorldShout, Reason: "S4A21 has no verified world-shout protocol mode"}
	default:
		return fmt.Errorf("S4A21 unknown shout channel %q", intent.Channel)
	}
	message, err := charset.EncodeGBKString(strings.TrimSpace(intent.Message))
	if err != nil {
		return err
	}
	return s.client.SendMessage(ctx, mode, 0, 0, message)
}

func (s *Session) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	if s.cancel != nil {
		s.cancel()
	}
	// Cancel the follower before closing the socket. Closing the socket is
	// still done before waiting so a worker blocked in a protocol write can
	// leave promptly.
	s.stopDungeonFollower(false)
	err := s.client.Close()
	if s.done != nil {
		<-s.done
	}
	s.stopDungeonFollower(true)
	if s.keepaliveDone != nil {
		<-s.keepaliveDone
	}
	return err
}

func (s *Session) Done() <-chan struct{} {
	if s == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return s.done
}

// setTerminationCallback keeps session lifecycle notification inside the
// S4A21 adapter. It avoids one waiter goroutine per online robot without
// widening the shared RobotSession contract.
func (s *Session) setTerminationCallback(callback func()) {
	if s == nil {
		return
	}
	s.terminationGuard.Lock()
	select {
	case <-s.done:
		s.terminationGuard.Unlock()
		if callback != nil {
			callback()
		}
		return
	default:
		s.termination = callback
		s.terminationGuard.Unlock()
	}
}

func (s *Session) signalTermination() {
	close(s.done)
	s.terminationGuard.Lock()
	callback := s.termination
	s.termination = nil
	s.terminationGuard.Unlock()
	if callback != nil {
		callback()
	}
}

// setPacketObserver is intentionally private to the S4A21 adapter. It gives a
// future verified dungeon workflow a narrow way to consume packets already
// owned by the session drain, without exposing raw packets to shared layers.
// Registrations are additive: overlapping waiters (move, follower preparation,
// dungeon workflow) must not steal each other's confirmation packets.
func (s *Session) setPacketObserver(observer func(protocol.Packet)) func() {
	if s == nil {
		return func() {}
	}
	registration := &packetObserverRegistration{fn: observer}
	s.packetObserverLock.Lock()
	s.packetObservers = append(s.packetObservers, registration)
	s.packetObserverLock.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.packetObserverLock.Lock()
			for index, candidate := range s.packetObservers {
				if candidate == registration {
					s.packetObservers = append(s.packetObservers[:index], s.packetObservers[index+1:]...)
					break
				}
			}
			s.packetObserverLock.Unlock()
		})
	}
}

func (s *Session) dispatchPacket(packet protocol.Packet) {
	if s == nil {
		return
	}
	if packet.Type == protocol.CmdCheckConnection {
		s.recordCheckAck(time.Now())
	}
	s.handleDisjointStorePacket(packet)
	s.followerGuard.Lock()
	followerEvents := s.followerEvents
	s.followerGuard.Unlock()
	if followerEvents != nil {
		queueFollowerPacket := false
		switch packet.Type {
		case protocol.NotiRequestPeer, protocol.NotiPartyInfo:
			queueFollowerPacket = true
		case protocol.NotiUserPosition, protocol.NotiUserArea:
			if len(packet.Body) >= 2 {
				uid := binary.LittleEndian.Uint16(packet.Body[:2])
				s.followerGuard.Lock()
				queueFollowerPacket = s.partyActive && s.partyLeaderUID != 0 && uid == s.partyLeaderUID && uid != s.selfUID
				s.followerGuard.Unlock()
			}
		}
		if queueFollowerPacket {
			// Never send from drain. The follower worker owns all writes.
			select {
			case followerEvents <- packet:
			default:
				// Dropping a party or loading projection would leave the
				// adapter out of sync while the session still appeared healthy.
				// End it so the existing scheduler can reconnect cleanly.
				s.abortFollowerQueue(followerEvents)
			}
		}
	}
	if packet.Type == protocol.NotiGuildInvite && s.guildInviteEvents != nil {
		invitation, err := protocol.ParseGuildInvite(packet.Body)
		if err == nil {
			select {
			case s.guildInviteEvents <- invitation:
			default:
				s.abort()
			}
		}
	}
	s.packetObserverLock.RLock()
	observers := append([]*packetObserverRegistration(nil), s.packetObservers...)
	s.packetObserverLock.RUnlock()
	for _, observer := range observers {
		if observer != nil && observer.fn != nil {
			observer.fn(packet)
		}
	}
}

func (s *Session) drain(ctx context.Context) {
	defer s.signalTermination()
	_ = s.client.Run(ctx, func(packet protocol.Packet) error {
		s.dispatchPacket(packet)
		return nil
	})
}

func (s *Session) keepalive(ctx context.Context) {
	if s.keepaliveDone == nil {
		return
	}
	defer close(s.keepaliveDone)
	ticker := time.NewTicker(sessionKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.guildInviteEvents:
			replyCtx, cancel := context.WithTimeout(ctx, sessionKeepaliveInterval/2)
			err := s.client.AcceptGuildInvite(replyCtx)
			cancel()
			if err != nil {
				s.abort()
				return
			}
		case <-ticker.C:
			if s.keepaliveStalled(time.Now()) {
				foundationlog.Robotf("S4A21_KEEPALIVE_STALLED uid=%d timeout=%s\n", s.selfUID, sessionKeepaliveAckTimeout)
				s.abort()
				return
			}
			pingCtx, cancel := context.WithTimeout(ctx, sessionKeepaliveInterval/2)
			err := s.client.CheckConnection(pingCtx)
			cancel()
			if err != nil {
				s.abort()
				return
			}
		}
	}
}

// recordCheckAck notes a CHECK_CONNECTION response. Enforcement of the
// half-open timeout only starts after the first observed response.
func (s *Session) recordCheckAck(now time.Time) {
	if s == nil {
		return
	}
	s.keepaliveGuard.Lock()
	s.lastCheckAck = now
	s.checkAckSeen = true
	s.keepaliveGuard.Unlock()
}

// keepaliveStalled reports whether the server stopped answering keepalives for
// longer than the configured timeout after having answered at least once.
func (s *Session) keepaliveStalled(now time.Time) bool {
	if s == nil {
		return false
	}
	s.keepaliveGuard.Lock()
	defer s.keepaliveGuard.Unlock()
	if !s.checkAckSeen {
		return false
	}
	return now.Sub(s.lastCheckAck) > sessionKeepaliveAckTimeout
}

func (s *Session) abort() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.client != nil {
		_ = s.client.Close()
	}
}

var _ shared.SessionFactory = SessionFactory{}
var _ shared.RobotSession = (*Session)(nil)
