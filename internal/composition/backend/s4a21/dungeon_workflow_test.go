package s4a21

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

func TestEnterSingleDungeonUsesVerifiedOrdinaryPacketSequence(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session, drainCancel := testSessionWithDrain(clientConn)
	defer drainCancel()
	done := make(chan error, 1)
	go func() {
		if err := expectDungeonRequest(serverConn, protocol.CmdEnterSelectDungeon, 4); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(1, protocol.CmdEnterSelectDungeon, []byte{1})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdSelectDungeon, 15); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiStartMap, []byte{0, 3})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdFinishLoading, 0); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiFinishLoading, []byte{0, 0, 0, 0, 0})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdMoveMap, 64); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiStartMap, []byte{1, 3})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdFinishLoading, 0); err != nil {
			done <- err
			return
		}
		_, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiFinishLoading, []byte{0, 0, 0, 0, 0}))
		done <- err
	}()

	snapshot, err := session.enterSingleDungeon(context.Background(), 144, false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != uint8(dungeonPhaseReady) || snapshot.DungeonID != 144 || snapshot.RoomX != 0 || snapshot.RoomY != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	moved, err := session.moveSingleDungeon(context.Background(), 1, 3, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Phase != uint8(dungeonPhaseReady) || moved.RoomX != 1 || moved.RoomY != 3 {
		t.Fatalf("moved snapshot = %+v", moved)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEnterSingleDungeonAddsTutorialFlagOnlyWhenRequested(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session, drainCancel := testSessionWithDrain(clientConn)
	defer drainCancel()
	done := make(chan error, 1)
	go func() {
		if err := expectDungeonRequest(serverConn, protocol.CmdEnterSelectDungeon, 4); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(1, protocol.CmdEnterSelectDungeon, []byte{1})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdSelectDungeon, 15); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdChangeTutorialFlag, 6); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiStartMap, []byte{0, 3})); err != nil {
			done <- err
			return
		}
		if err := expectDungeonRequest(serverConn, protocol.CmdFinishLoading, 0); err != nil {
			done <- err
			return
		}
		_, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiFinishLoading, []byte{0, 0, 0, 0, 0}))
		done <- err
	}()

	if _, err := session.enterSingleDungeon(context.Background(), 144, true); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestEnterSingleDungeonReturnsRejectedAckAndTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session, drainCancel := testSessionWithDrain(clientConn)
	defer drainCancel()
	go func() {
		_, _ = protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		_, _ = serverConn.Write(protocol.EncodeResponse(1, protocol.CmdEnterSelectDungeon, []byte{0}))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := session.enterSingleDungeon(ctx, 144, false); err == nil {
		t.Fatal("rejected dungeon ACK unexpectedly succeeded")
	}
}

func TestMoveSingleDungeonTimeoutPreservesReadyRoom(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session, drainCancel := testSessionWithDrain(clientConn)
	defer drainCancel()

	state := &dungeonRunState{}
	if err := state.BeginSelection(144); err != nil {
		t.Fatal(err)
	}
	if err := state.BeginEntry(); err != nil {
		t.Fatal(err)
	}
	if err := state.StartLoading(2, 3); err != nil {
		t.Fatal(err)
	}
	if err := state.FinishLoading(); err != nil {
		t.Fatal(err)
	}
	session.dungeonState = state

	requestRead := make(chan error, 1)
	go func() {
		_, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		requestRead <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := session.moveSingleDungeon(ctx, 1, 3, 0, 0); err == nil {
		t.Fatal("timed out dungeon move unexpectedly succeeded")
	}
	if err := <-requestRead; err != nil {
		t.Fatal(err)
	}
	snapshot := state.Snapshot()
	if snapshot.Phase != uint8(dungeonPhaseReady) || snapshot.RoomX != 2 || snapshot.RoomY != 3 {
		t.Fatalf("state changed after timed out move: %+v", snapshot)
	}
}

func testSessionWithDrain(clientConn net.Conn) (*Session, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{})}
	go session.drain(ctx)
	return session, func() {
		cancel()
		_ = clientConn.Close()
		<-session.done
	}
}

func expectDungeonRequest(conn net.Conn, typ uint16, bodyLength int) error {
	packet, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
	if err != nil {
		return err
	}
	if packet.Type != typ || len(packet.Body) != bodyLength {
		return fmt.Errorf("request type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), typ, bodyLength)
	}
	return nil
}
