package s4a21

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

func TestDungeonFollowerAcceptsInviteAndFollowsServerMap(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	session, stopDrain := testSessionWithDrain(clientConn)
	session.selfUID = 0x1234
	defer stopDrain()
	defer session.DisableDungeonFollower()

	serverDone := make(chan error, 1)
	go func() {
		packet, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil {
			serverDone <- err
			return
		}
		if packet.Type != protocol.CmdChangeTutorialFlag || len(packet.Body) != 6 ||
			binary.LittleEndian.Uint32(packet.Body[1:5]) != 31 || packet.Body[5] != 0 {
			serverDone <- fmt.Errorf("tutorial skip request = type 0x%04X body %X", packet.Type, packet.Body)
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(1, protocol.CmdChangeTutorialFlag, []byte{1, 0})); err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiRequestPeer, []byte{0x34, 0x12, 0, 0, 0, 0, 0, 0, 0, 0})); err != nil {
			serverDone <- err
			return
		}
		packet, err = protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil {
			serverDone <- err
			return
		}
		if packet.Type != protocol.CmdResponsePeer || len(packet.Body) != 7 ||
			binary.LittleEndian.Uint16(packet.Body[:2]) != 0x1234 || packet.Body[2] != 0 {
			serverDone <- fmt.Errorf("party acceptance = type 0x%04X body %X", packet.Type, packet.Body)
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiPartyInfo, followerPartyRosterBody(1, 0x5678, 0x1234))); err != nil {
			serverDone <- err
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiStartMap, []byte{2, 4})); err != nil {
			serverDone <- err
			return
		}
		packet, err = protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil {
			serverDone <- err
			return
		}
		if packet.Type != protocol.CmdFinishLoading || len(packet.Body) != 0 {
			serverDone <- fmt.Errorf("follower loading request = type 0x%04X body %X", packet.Type, packet.Body)
			return
		}
		if _, err = serverConn.Write(protocol.EncodeResponse(0, protocol.NotiFinishLoading, []byte{0, 0, 0, 0, 0})); err != nil {
			serverDone <- err
			return
		}
		if _, err = serverConn.Write(protocol.EncodeResponse(0, protocol.NotiStartMap, []byte{3, 4})); err != nil {
			serverDone <- err
			return
		}
		packet, err = protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil {
			serverDone <- err
			return
		}
		if packet.Type != protocol.CmdFinishLoading || len(packet.Body) != 0 {
			serverDone <- fmt.Errorf("second follower request = type 0x%04X body %X; follower must not send MOVE_MAP", packet.Type, packet.Body)
			return
		}
		_, err = serverConn.Write(protocol.EncodeResponse(0, protocol.NotiFinishLoading, []byte{0, 0, 0, 0, 0}))
		serverDone <- err
	}()

	if err := session.EnableDungeonFollower(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		session.dungeonStateGuard.Lock()
		state := session.dungeonState
		var snapshot dungeonRunSnapshot
		if state != nil {
			snapshot = state.Snapshot()
		}
		session.dungeonStateGuard.Unlock()
		if snapshot.Phase == uint8(dungeonPhaseReady) && snapshot.RoomX == 3 {
			if snapshot.RoomY != 4 {
				t.Fatalf("follower snapshot = %+v", snapshot)
			}
			if !session.PartyActive() {
				t.Fatal("party info did not activate follower party state")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("follower did not commit START_MAP after FINISH_LOADING")
}

func TestDungeonFollowerIsExplicitlyOptIn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session, stopDrain := testSessionWithDrain(clientConn)
	defer stopDrain()
	if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiRequestPeer, []byte{1, 0, 0, 0, 0, 0, 0})); err != nil {
		t.Fatal(err)
	}
	_ = serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength); err == nil {
		t.Fatal("unconfigured session sent a party response")
	}
	session.DisableDungeonFollower()
}

func TestDungeonFollowerIgnoresStartMapBeforeOwnPartyIsConfirmed(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	session := &Session{client: protocol.NewClient(clientConn), selfUID: 0x1234}

	session.handleFollowerPacket(context.Background(), protocol.Packet{
		Type: protocol.NotiStartMap, Body: []byte{2, 4},
	})
	if session.dungeonState != nil {
		t.Fatalf("unconfirmed START_MAP created dungeon state: %+v", session.dungeonState)
	}
	_ = serverConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	if packet, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength); err == nil {
		t.Fatalf("unconfirmed START_MAP produced request type=0x%04X", packet.Type)
	}
}

func TestDungeonFollowerCloseStopsWorker(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		client:  protocol.NewClient(clientConn),
		cancel:  cancel,
		done:    make(chan struct{}),
		selfUID: 0x1234,
	}
	go session.drain(ctx)
	go func() {
		packet, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil || packet.Type != protocol.CmdChangeTutorialFlag {
			return
		}
		_, _ = serverConn.Write(protocol.EncodeResponse(1, protocol.CmdChangeTutorialFlag, []byte{1}))
	}()
	if err := session.EnableDungeonFollower(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		_ = session.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("closing follower session did not stop worker")
	}
}

func TestDungeonFollowerStopsWhenSessionDrainEnds(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	session, stopDrain := testSessionWithDrain(clientConn)
	session.selfUID = 0x1234
	defer stopDrain()
	go func() {
		packet, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err == nil && packet.Type == protocol.CmdChangeTutorialFlag {
			_, _ = serverConn.Write(protocol.EncodeResponse(1, protocol.CmdChangeTutorialFlag, []byte{1}))
		}
	}()
	if err := session.EnableDungeonFollower(context.Background()); err != nil {
		t.Fatal(err)
	}
	session.followerGuard.Lock()
	followerDone := session.followerDone
	session.followerGuard.Unlock()
	_ = serverConn.Close()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("session drain did not stop")
	}
	select {
	case <-followerDone:
	case <-time.After(time.Second):
		t.Fatal("follower worker survived session drain")
	}
}

func TestDungeonFollowerWriteFailureStopsSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	wrapped := &failWriteConn{Conn: clientConn, failAt: 2}
	session, stopDrain := testSessionWithDrain(wrapped)
	session.selfUID = 0x1234
	defer stopDrain()
	defer serverConn.Close()

	serverDone := make(chan error, 1)
	go func() {
		packet, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
		if err != nil {
			serverDone <- err
			return
		}
		if packet.Type != protocol.CmdChangeTutorialFlag {
			serverDone <- fmt.Errorf("prepare type=0x%04X", packet.Type)
			return
		}
		if _, err := serverConn.Write(protocol.EncodeResponse(1, protocol.CmdChangeTutorialFlag, []byte{1})); err != nil {
			serverDone <- err
			return
		}
		_, err = serverConn.Write(protocol.EncodeResponse(0, protocol.NotiRequestPeer, []byte{0x34, 0x12, 0}))
		serverDone <- err
	}()

	if err := session.EnableDungeonFollower(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("party acceptance write failure did not terminate session")
	}
}

func TestDungeonFollowerEventOverflowStopsSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	tracked := &closeTrackingConn{Conn: clientConn}
	cancelled := false
	session := &Session{
		client:         protocol.NewClient(tracked),
		cancel:         func() { cancelled = true },
		followerEvents: make(chan protocol.Packet, 1),
	}
	packet := protocol.Packet{Type: protocol.NotiPartyInfo}
	session.dispatchPacket(packet)
	session.dispatchPacket(packet)

	if !cancelled || tracked.closes != 1 {
		t.Fatalf("overflow did not abort session: cancel=%t closes=%d", cancelled, tracked.closes)
	}
}

func TestStaleFollowerEventOverflowKeepsDisabledTownSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	tracked := &closeTrackingConn{Conn: clientConn}
	cancelled := false
	events := make(chan protocol.Packet, 1)
	session := &Session{
		client:         protocol.NewClient(tracked),
		cancel:         func() { cancelled = true },
		followerEvents: events,
	}

	session.DisableDungeonFollower()
	session.abortFollowerQueue(events)
	if cancelled || tracked.closes != 0 {
		t.Fatalf("stale overflow aborted town session: cancel=%t closes=%d", cancelled, tracked.closes)
	}
}

func TestDungeonFollowerCancellationDoesNotAbortTownSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	tracked := &closeTrackingConn{Conn: clientConn}
	cancelled := false
	session := &Session{client: protocol.NewClient(tracked), cancel: func() { cancelled = true }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	session.abortFollowerSession(ctx)

	if cancelled || tracked.closes != 0 {
		t.Fatalf("follower cancellation aborted town session: cancel=%t closes=%d", cancelled, tracked.closes)
	}
}

type failWriteConn struct {
	net.Conn
	writes int
	failAt int
}

type closeTrackingConn struct {
	net.Conn
	closes int
}

func (c *closeTrackingConn) Close() error {
	c.closes++
	return c.Conn.Close()
}

func (c *failWriteConn) Write(data []byte) (int, error) {
	c.writes++
	if c.writes == c.failAt {
		return 0, errors.New("injected follower write failure")
	}
	return c.Conn.Write(data)
}

func TestParsePartyInfoProjectionRequiresOwnRosterMembership(t *testing.T) {
	partyID, cleared, ok := parsePartyInfoProjection(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerPartyRosterBody(7, 11, 12),
	}, 12)
	if !ok || partyID != 7 || len(cleared) != 0 {
		t.Fatalf("own roster projection = party=%d cleared=%v ok=%t", partyID, cleared, ok)
	}

	partyID, cleared, ok = parsePartyInfoProjection(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerNamedPartyRosterBody(9, 31, 32, "party-name"),
	}, 32)
	if !ok || partyID != 9 || len(cleared) != 0 {
		t.Fatalf("named roster projection = party=%d cleared=%v ok=%t", partyID, cleared, ok)
	}

	partyID, cleared, ok = parsePartyInfoProjection(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerShortNamedPartyRosterBody(10, 41, 42, "old-party"),
	}, 42)
	if !ok || partyID != 10 || len(cleared) != 0 {
		t.Fatalf("short named roster projection = party=%d cleared=%v ok=%t", partyID, cleared, ok)
	}

	partyID, cleared, ok = parsePartyInfoProjection(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerPartyRosterBody(8, 21, 22),
	}, 12)
	if !ok || partyID != 0 || len(cleared) != 0 {
		t.Fatalf("public roster projection = party=%d cleared=%v ok=%t", partyID, cleared, ok)
	}

	partyID, cleared, ok = parsePartyInfoProjection(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: []byte{1, 0, 7, 0, 3},
	}, 12)
	if !ok || partyID != 0 || len(cleared) != 1 || cleared[0] != 7 {
		t.Fatalf("party clear projection = party=%d cleared=%v ok=%t", partyID, cleared, ok)
	}
}

func TestDungeonFollowerIgnoresPublicPartyRosterAndUnrelatedClear(t *testing.T) {
	session := &Session{selfUID: 12, partyID: 7, partyActive: true, dungeonState: &dungeonRunState{phase: dungeonPhaseReady}}
	session.handleFollowerPacket(context.Background(), protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerPartyRosterBody(8, 21, 22),
	})
	if !session.PartyActive() || session.partyID != 7 || session.dungeonState == nil {
		t.Fatalf("public roster changed follower state: active=%t party=%d state=%v", session.PartyActive(), session.partyID, session.dungeonState)
	}
	session.handleFollowerPacket(context.Background(), protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: []byte{1, 0, 8, 0, 3},
	})
	if !session.PartyActive() || session.partyID != 7 || session.dungeonState == nil {
		t.Fatalf("unrelated clear changed follower state: active=%t party=%d state=%v", session.PartyActive(), session.partyID, session.dungeonState)
	}
	session.handleFollowerPacket(context.Background(), protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: []byte{1, 0, 7, 0, 3},
	})
	if session.PartyActive() || session.partyID != 0 || session.dungeonState != nil {
		t.Fatalf("matching clear did not reset follower: active=%t party=%d state=%v", session.PartyActive(), session.partyID, session.dungeonState)
	}
}

func followerPartyRosterBody(partyID, leaderUID, memberUID uint16) []byte {
	body := make([]byte, 0, 65)
	body = binary.LittleEndian.AppendUint16(body, 1)
	body = binary.LittleEndian.AppendUint16(body, partyID)
	body = append(body, 0, 0)
	body = binary.LittleEndian.AppendUint32(body, 0)
	body = append(body, 0, 4, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF)
	for slot := 0; slot < 8; slot++ {
		uid := uint16(0xFFFF)
		if slot == 0 {
			uid = leaderUID
		} else if slot == 1 {
			uid = memberUID
		}
		body = binary.LittleEndian.AppendUint16(body, uid)
		body = append(body, 0, 0, 0)
	}
	body = append(body, 0, 0, 0, 0)
	return body
}

func followerNamedPartyRosterBody(partyID, leaderUID, memberUID uint16, name string) []byte {
	body := followerPartyRosterBody(partyID, leaderUID, memberUID)
	nameBytes := []byte(name)
	named := make([]byte, 0, len(body)+len(nameBytes))
	named = append(named, body[:6]...)
	named = binary.LittleEndian.AppendUint32(named, uint32(len(nameBytes)))
	named = append(named, nameBytes...)
	named = append(named, body[10:]...)
	return named
}

func followerShortNamedPartyRosterBody(partyID, leaderUID, memberUID uint16, name string) []byte {
	body := followerPartyRosterBody(partyID, leaderUID, memberUID)
	nameBytes := []byte(name)
	named := make([]byte, 0, len(body)+len(nameBytes)-5)
	named = append(named, body[:6]...)
	named = binary.LittleEndian.AppendUint32(named, uint32(len(nameBytes)))
	named = append(named, nameBytes...)
	named = append(named, body[10:16]...)
	named = append(named, body[21:]...)
	return named
}
