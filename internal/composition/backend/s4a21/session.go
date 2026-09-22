package s4a21

import (
	"context"
	"fmt"
	"strings"
	"time"

	"robot/internal/foundation/charset"
	"robot/internal/foundation/lockhub"
	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

type SessionFactory struct {
	Address string
	Timeout time.Duration
}

const sessionKeepaliveInterval = 5 * time.Second

type Session struct {
	client             *protocol.Client
	cancel             context.CancelFunc
	done               chan struct{}
	keepaliveDone      chan struct{}
	packetObserverLock lockhub.RWLocker
	packetObserver     *packetObserverRegistration
}

type packetObserverRegistration struct {
	fn func(protocol.Packet)
}

func (f SessionFactory) OpenSession(ctx context.Context, request shared.OpenSessionRequest) (shared.RobotSession, error) {
	if strings.TrimSpace(request.AccountName) == "" {
		return nil, fmt.Errorf("account name is required")
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
	if err := waitFor(openCtx, client, protocol.CmdSelectCharacter, 1); err != nil {
		return nil, fmt.Errorf("S4A21 select character: %w", err)
	}
	if err := client.CheckConnection(openCtx); err != nil {
		return nil, err
	}
	if err := waitFor(openCtx, client, protocol.CmdCheckConnection, 1); err != nil {
		return nil, fmt.Errorf("S4A21 session readiness: %w", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	session := &Session{client: client, cancel: cancel, done: make(chan struct{}), keepaliveDone: make(chan struct{})}
	go session.drain(runCtx)
	go session.keepalive(runCtx)
	closeOnError = false
	return session, nil
}

func (s *Session) MoveTown(ctx context.Context, intent shared.TownMoveIntent) error {
	return s.client.SetUserPosition(ctx, intent.X, intent.Y, intent.Direction, intent.Motion)
}

func (s *Session) MoveDungeon(ctx context.Context, intent shared.DungeonMoveIntent) error {
	return shared.UnsupportedCapabilityError{
		Backend:   shared.BackendS4A21,
		Operation: shared.CapabilityDungeonMove,
		Reason:    "dungeon entry workflow is not integrated yet",
	}
}

func (s *Session) Shout(ctx context.Context, intent shared.ShoutIntent) error {
	mode := byte(0)
	switch intent.Channel {
	case shared.ShoutChannelArea:
		mode = 3
	case shared.ShoutChannelParty:
		mode = 2
	case shared.ShoutChannelWorld:
		return shared.UnsupportedCapabilityError{Backend: shared.BackendS4A21, Operation: shared.CapabilityWorldShout, Reason: "S4A21 has no verified world-shout protocol mode"}
	default:
		return fmt.Errorf("unknown shout channel %q", intent.Channel)
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
	s.cancel()
	err := s.client.Close()
	<-s.done
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

// setPacketObserver is intentionally private to the S4A21 adapter. It gives a
// future verified dungeon workflow a narrow way to consume packets already
// owned by the session drain, without exposing raw packets to shared layers.
func (s *Session) setPacketObserver(observer func(protocol.Packet)) func() {
	if s == nil {
		return func() {}
	}
	registration := &packetObserverRegistration{fn: observer}
	s.packetObserverLock.Lock()
	s.packetObserver = registration
	s.packetObserverLock.Unlock()
	return func() {
		s.packetObserverLock.Lock()
		if s.packetObserver == registration {
			s.packetObserver = nil
		}
		s.packetObserverLock.Unlock()
	}
}

func (s *Session) dispatchPacket(packet protocol.Packet) {
	if s == nil {
		return
	}
	s.packetObserverLock.RLock()
	observer := s.packetObserver
	s.packetObserverLock.RUnlock()
	if observer != nil && observer.fn != nil {
		observer.fn(packet)
	}
}

func (s *Session) drain(ctx context.Context) {
	defer close(s.done)
	for {
		packet, err := s.client.Read(ctx)
		if err != nil {
			return
		}
		s.dispatchPacket(packet)
	}
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
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, sessionKeepaliveInterval/2)
			err := s.client.CheckConnection(pingCtx)
			cancel()
			if err != nil {
				s.cancel()
				_ = s.client.Close()
				return
			}
		}
	}
}

var _ shared.SessionFactory = SessionFactory{}
var _ shared.RobotSession = (*Session)(nil)
