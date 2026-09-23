package s4a21

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

type partialWriteConn struct {
	net.Conn
	buffer bytes.Buffer
	limit  int
}

func (c *partialWriteConn) Write(data []byte) (int, error) {
	if len(data) > c.limit {
		data = data[:c.limit]
	}
	return c.buffer.Write(data)
}

func (c *partialWriteConn) SetWriteDeadline(time.Time) error { return nil }

type observedConn struct {
	net.Conn
	readStarted  chan struct{}
	writeStarted chan struct{}
}

func (c *observedConn) Read(data []byte) (int, error) {
	if c.readStarted != nil {
		select {
		case c.readStarted <- struct{}{}:
		default:
		}
	}
	return c.Conn.Read(data)
}

func (c *observedConn) Write(data []byte) (int, error) {
	if c.writeStarted != nil {
		select {
		case c.writeStarted <- struct{}{}:
		default:
		}
	}
	return c.Conn.Write(data)
}

func TestClientSendWritesCompleteFrameAfterShortWrites(t *testing.T) {
	conn := &partialWriteConn{limit: 3}
	client := NewClient(conn)
	want := Encode(1, CmdCheckConnection, nil)
	if err := client.CheckConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(conn.buffer.Bytes(), want) {
		t.Fatalf("written frame = %x, want %x", conn.buffer.Bytes(), want)
	}
}

func TestClientCanceledWriteDoesNotPoisonNextSend(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	writeStarted := make(chan struct{}, 1)
	client := NewClient(&observedConn{Conn: clientConn, writeStarted: writeStarted})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- client.CheckConnection(ctx) }()
	<-writeStarted
	cancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("canceled send error = %v, want %v", err, context.Canceled)
	}

	read := make(chan error, 1)
	go func() {
		packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
		if err == nil && packet.Type != CmdCheckConnection {
			err = fmt.Errorf("packet type = 0x%04X", packet.Type)
		}
		read <- err
	}()
	if err := client.CheckConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-read; err != nil {
		t.Fatal(err)
	}
}

func TestClientCanceledReadDoesNotPoisonNextRead(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	readStarted := make(chan struct{}, 1)
	client := NewClient(&observedConn{Conn: clientConn, readStarted: readStarted})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Read(ctx)
		result <- err
	}()
	<-readStarted
	cancel()
	if err := <-result; err != context.Canceled {
		t.Fatalf("canceled read error = %v, want %v", err, context.Canceled)
	}

	written := make(chan error, 1)
	go func() {
		_, err := serverConn.Write(EncodeResponse(0, NotiUserArea, nil))
		written <- err
	}()
	packet, err := client.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if packet.Type != NotiUserArea {
		t.Fatalf("packet type = 0x%04X", packet.Type)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
}

func TestClientLoginWritesProtocolPacket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
		if err == nil && (packet.Type != CmdLogin || string(packet.Body[4:9]) != "robot") {
			err = fmt.Errorf("unexpected packet: %+v", packet)
		}
		done <- err
	}()
	if err := client.Login(context.Background(), "robot", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientMethodsUseS4A21Widths(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if i == 0 && (packet.Type != CmdSelectCharacter || len(packet.Body) != 2) {
				done <- fmt.Errorf("select packet = %+v", packet)
				return
			}
			if i == 1 && (packet.Type != CmdMoveMap || len(packet.Body) != 64) {
				done <- fmt.Errorf("move packet = %+v", packet)
				return
			}
		}
		done <- nil
	}()
	if err := client.SelectCharacter(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := client.MoveMap(context.Background(), MoveMapRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientDeleteCharacterWritesVerifiedPacket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
		if err == nil && (packet.Type != CmdDeleteCharacter || len(packet.Body) != 13 || binary.LittleEndian.Uint16(packet.Body[:2]) != 2 || string(packet.Body[6:]) != "robot01") {
			err = fmt.Errorf("delete packet = %+v", packet)
		}
		done <- err
	}()
	if err := client.DeleteCharacter(context.Background(), 2, []byte("robot01")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientCharacterRosterRequestWritesGetUserInfoModeTwo(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
		if err == nil && (packet.Type != CmdGetUserInfo || string(packet.Body) != string([]byte{0, 0, 2})) {
			err = fmt.Errorf("roster request packet = %+v", packet)
		}
		done <- err
	}()
	if err := client.RequestCharacterRoster(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientVerifiedDungeonPrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdEnterSelectDungeon, 4},
			{CmdSelectDungeon, 15},
			{CmdChangeTutorialFlag, 6},
			{CmdFinishLoading, 0},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("dungeon packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	if err := client.EnterSelectDungeon(context.Background(), 144); err != nil {
		t.Fatal(err)
	}
	if err := client.SelectDungeon(context.Background(), 144, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := client.ChangeTutorialFlag(context.Background(), 30, 1); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishLoading(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientVerifiedQuestPrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdAcceptQuest, 4},
			{CmdSetQuestTrigger, 6},
			{CmdFinishQuest, 10},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("quest packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	if err := client.AcceptQuest(context.Background(), 1016); err != nil {
		t.Fatal(err)
	}
	if err := client.SetQuestTrigger(context.Background(), 1016, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishQuest(context.Background(), 1016, -1, 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientPartyProbePrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdSetPartyInfo, 12},
			{CmdRequestPeer, 7},
			{CmdResponsePeer, 7},
			{CmdLeaveParty, 0},
			{CmdWalkoutPartyMember, 1},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("party packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	settings := []byte{0, 0, 4, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	if err := client.SetPartyInfo(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := client.RequestPeer(context.Background(), 12, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := client.AcceptPartyInvite(context.Background(), 12); err != nil {
		t.Fatal(err)
	}
	if err := client.LeaveParty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.WalkoutPartyMember(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
