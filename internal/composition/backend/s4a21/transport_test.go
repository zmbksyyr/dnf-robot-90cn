package s4a21

import (
	"context"
	"testing"
	"time"

	"robot/internal/shared"
)

type actionTestSession struct {
	town   shared.TownMoveIntent
	shout  shared.ShoutIntent
	closed bool
	done   chan struct{}
}

func (s *actionTestSession) MoveTown(_ context.Context, intent shared.TownMoveIntent) error {
	s.town = intent
	return nil
}
func (s *actionTestSession) MoveDungeon(context.Context, shared.DungeonMoveIntent) error { return nil }
func (s *actionTestSession) Shout(_ context.Context, intent shared.ShoutIntent) error {
	s.shout = intent
	return nil
}
func (s *actionTestSession) Close() error          { s.closed = true; return nil }
func (s *actionTestSession) Done() <-chan struct{} { return s.done }

type actionTestFactory struct{ session *actionTestSession }

func (f actionTestFactory) OpenSession(context.Context, shared.OpenSessionRequest) (shared.RobotSession, error) {
	return f.session, nil
}

func TestActionTransportMapsVerifiedTownActions(t *testing.T) {
	session := &actionTestSession{}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 7, Village: 2, Area: 4, X: 120, Y: 240, MoveType: 1, Speed: 30}); err != nil {
		t.Fatal(err)
	}
	if session.town.X != 120 || session.town.Y != 240 || session.town.Direction != 1 || session.town.Motion != 30 {
		t.Fatalf("town intent = %+v", session.town)
	}
	if status := transport.RuntimeStatusMap()[7]; status.Village != 2 || status.Area != 4 || status.X != 120 || status.Y != 240 {
		t.Fatalf("runtime status = %+v", status)
	}
	if err := transport.ShoutLocal(context.Background(), shared.RuntimeShoutCommand{UID: 7, Message: "hello"}); err != nil {
		t.Fatal(err)
	}
	if session.shout.Channel != shared.ShoutChannelArea || session.shout.Message != "hello" {
		t.Fatalf("shout intent = %+v", session.shout)
	}
}

func TestActionTransportRejectsMissingSessionAndPositionOverflow(t *testing.T) {
	transport := NewActionTransport()
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 9}); err == nil {
		t.Fatal("missing session unexpectedly succeeded")
	}
	if err := transport.Attach(9, &actionTestSession{}); err != nil {
		t.Fatal(err)
	}
	if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 9, X: 100000}); err == nil {
		t.Fatal("overflow position unexpectedly succeeded")
	}
}

func TestActionTransportOwnsSessionLifecycle(t *testing.T) {
	session := &actionTestSession{}
	transport := NewActionTransport(actionTestFactory{session: session})
	if err := transport.Open(context.Background(), 7, shared.OpenSessionRequest{AccountName: "acct", CharacterSlot: 2}); err != nil {
		t.Fatal(err)
	}
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateRunning {
		t.Fatalf("status after open = %+v", status)
	}
	if err := transport.Close(7); err != nil || !session.closed {
		t.Fatalf("close err=%v closed=%v", err, session.closed)
	}
	if status := transport.RuntimeStatusMap()[7]; status.StateName != shared.RuntimeStateStop {
		t.Fatalf("status after close = %+v", status)
	}
}

func TestActionTransportReapsUnexpectedSessionEnd(t *testing.T) {
	session := &actionTestSession{done: make(chan struct{})}
	transport := NewActionTransport()
	if err := transport.Attach(7, session); err != nil {
		t.Fatal(err)
	}
	close(session.done)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if status := transport.RuntimeStatusMap()[7]; status.StateName == shared.RuntimeStateStop {
			if err := transport.MoveTown(context.Background(), shared.RuntimeMoveCommand{UID: 7}); err == nil {
				t.Fatal("move unexpectedly succeeded after session end")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session was not reaped: %+v", transport.RuntimeStatusMap())
}
