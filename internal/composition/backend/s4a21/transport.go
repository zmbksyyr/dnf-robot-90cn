package s4a21

import (
	"context"
	"errors"
	"fmt"
	"math"

	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// ActionTransport adapts already-open S4A21 sessions to scheduler actions.
// Session creation and lifecycle remain outside this adapter.
type ActionTransport struct {
	mu       lockhub.RWLocker
	factory  shared.SessionFactory
	sessions map[int]shared.RobotSession
	status   map[int]shared.RuntimeStatus
}

func NewActionTransport(factory ...shared.SessionFactory) *ActionTransport {
	var sessionFactory shared.SessionFactory
	if len(factory) > 0 {
		sessionFactory = factory[0]
	}
	return &ActionTransport{factory: sessionFactory, sessions: make(map[int]shared.RobotSession), status: make(map[int]shared.RuntimeStatus)}
}

func (t *ActionTransport) Open(ctx context.Context, uid int, request shared.OpenSessionRequest) error {
	if t == nil || t.factory == nil {
		return fmt.Errorf("S4A21 session factory is not configured")
	}
	session, err := t.factory.OpenSession(ctx, request)
	if err != nil {
		return err
	}
	if err := t.Attach(uid, session); err != nil {
		_ = session.Close()
		return err
	}
	return nil
}

func (t *ActionTransport) Attach(uid int, session shared.RobotSession) error {
	if t == nil || uid <= 0 || session == nil {
		return fmt.Errorf("S4A21 action transport requires uid and session")
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.sessions[uid]; exists {
		return fmt.Errorf("S4A21 session already attached for uid %d", uid)
	}
	t.sessions[uid] = session
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateRunning, State: 3}
	return nil
}

func (t *ActionTransport) Detach(uid int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.sessions, uid)
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
	t.mu.Unlock()
}

func (t *ActionTransport) Close(uid int) error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	session := t.sessions[uid]
	delete(t.sessions, uid)
	t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
	t.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.Close()
}

func (t *ActionTransport) CloseAll() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	sessions := make([]shared.RobotSession, 0, len(t.sessions))
	for uid, session := range t.sessions {
		delete(t.sessions, uid)
		t.status[uid] = shared.RuntimeStatus{UID: uid, StateName: shared.RuntimeStateStop}
		sessions = append(sessions, session)
	}
	t.mu.Unlock()
	var closeErr error
	for _, session := range sessions {
		if err := session.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func (t *ActionTransport) RuntimeStatusMap() map[int]shared.RuntimeStatus {
	if t == nil {
		return map[int]shared.RuntimeStatus{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[int]shared.RuntimeStatus, len(t.status))
	for uid, status := range t.status {
		out[uid] = status
	}
	return out
}

func (t *ActionTransport) session(uid int) (shared.RobotSession, error) {
	if t == nil {
		return nil, fmt.Errorf("S4A21 action transport is nil")
	}
	t.mu.RLock()
	session := t.sessions[uid]
	t.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("S4A21 session is not attached for uid %d", uid)
	}
	return session, nil
}

func (t *ActionTransport) MoveTown(ctx context.Context, command shared.RuntimeMoveCommand) error {
	session, err := t.session(command.UID)
	if err != nil {
		return err
	}
	if command.X < math.MinInt16 || command.X > math.MaxInt16 || command.Y < math.MinInt16 || command.Y > math.MaxInt16 {
		return fmt.Errorf("S4A21 town position out of range: %d,%d", command.X, command.Y)
	}
	return session.MoveTown(ctx, shared.TownMoveIntent{X: int16(command.X), Y: int16(command.Y), Direction: byte(command.MoveType), Motion: uint16(command.Speed)})
}

func (t *ActionTransport) ShoutLocal(ctx context.Context, command shared.RuntimeShoutCommand) error {
	session, err := t.session(command.UID)
	if err != nil {
		return err
	}
	return session.Shout(ctx, shared.ShoutIntent{Channel: shared.ShoutChannelArea, Message: command.Message})
}

var _ interface {
	MoveTown(context.Context, shared.RuntimeMoveCommand) error
	ShoutLocal(context.Context, shared.RuntimeShoutCommand) error
} = (*ActionTransport)(nil)
