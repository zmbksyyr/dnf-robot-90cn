package s4a21

import (
	"context"
	"encoding/binary"
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
		if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiPartyInfo, followerPartyInfoBody(1))); err != nil {
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

func TestDungeonFollowerCloseStopsWorker(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		client: protocol.NewClient(clientConn),
		cancel: cancel,
		done:   make(chan struct{}),
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

func TestParsePartyInfoActiveHandlesRosterAndClearBlocks(t *testing.T) {
	active, ok := parsePartyInfoActive(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: followerPartyRosterBody(7, 11, 12),
	})
	if !ok || !active {
		t.Fatalf("party roster parsed as active=%t ok=%t", active, ok)
	}
	active, ok = parsePartyInfoActive(protocol.Packet{
		Type: protocol.NotiPartyInfo, Body: []byte{1, 0, 7, 0, 3},
	})
	if !ok || active {
		t.Fatalf("party clear parsed as active=%t ok=%t", active, ok)
	}
}

func followerPartyInfoBody(partyID uint16) []byte {
	body := make([]byte, 18)
	binary.LittleEndian.PutUint16(body[:2], 1)
	binary.LittleEndian.PutUint16(body[2:4], partyID)
	body[4] = 1
	body[5] = 1
	body[17] = 0
	return body
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
