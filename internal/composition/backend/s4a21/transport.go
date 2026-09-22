package s4a21

import (
	"context"
	"fmt"
	"math"

	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

// ActionTransport adapts already-open S4A21 sessions to scheduler actions.
// Session creation and lifecycle remain outside this adapter.
type ActionTransport struct {
	mu       lockhub.RWLocker
	sessions map[int]shared.RobotSession
}

func NewActionTransport() *ActionTransport {
	return &ActionTransport{sessions: make(map[int]shared.RobotSession)}
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
	return nil
}

func (t *ActionTransport) Detach(uid int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	delete(t.sessions, uid)
	t.mu.Unlock()
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
