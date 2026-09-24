package s4a21

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

func TestSessionAutomaticallyAcceptsGuildInvitation(t *testing.T) {
	session, serverConn, stop := startGuildTestSession(t)
	defer stop()

	body := make([]byte, 7+len("leader"))
	binary.LittleEndian.PutUint32(body[0:4], 123)
	binary.LittleEndian.PutUint16(body[4:6], 45)
	body[6] = byte(len("leader"))
	copy(body[7:], "leader")
	if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiGuildInvite, body)); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reply, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength)
	if err != nil {
		t.Fatal(err)
	}
	if reply.Type != protocol.CmdReplyGuildInvite || len(reply.Body) != 1 || reply.Body[0] != 1 {
		t.Fatalf("guild invitation reply = %+v", reply)
	}
	if len(session.guildInviteEvents) != 0 {
		t.Fatal("guild invitation remained queued after reply")
	}
}

func TestSessionIgnoresMalformedGuildInvitation(t *testing.T) {
	_, serverConn, stop := startGuildTestSession(t)
	defer stop()

	if _, err := serverConn.Write(protocol.EncodeResponse(0, protocol.NotiGuildInvite, []byte{1, 0, 0})); err != nil {
		t.Fatal(err)
	}
	if err := serverConn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.ReadRequestFrame(serverConn, protocol.DefaultMaxPacketLength); err == nil {
		t.Fatal("malformed guild invitation produced a reply")
	}
}

func startGuildTestSession(t *testing.T) (*Session, net.Conn, func()) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	session := &Session{
		client: protocol.NewClient(clientConn), cancel: cancel,
		done: make(chan struct{}), keepaliveDone: make(chan struct{}),
		guildInviteEvents: make(chan protocol.GuildInvite, guildInviteQueueSize),
	}
	go session.drain(ctx)
	go session.keepalive(ctx)
	return session, serverConn, func() {
		cancel()
		_ = clientConn.Close()
		_ = serverConn.Close()
		select {
		case <-session.done:
		case <-time.After(time.Second):
			t.Fatal("session drain did not stop")
		}
		select {
		case <-session.keepaliveDone:
		case <-time.After(time.Second):
			t.Fatal("session keepalive did not stop")
		}
	}
}
