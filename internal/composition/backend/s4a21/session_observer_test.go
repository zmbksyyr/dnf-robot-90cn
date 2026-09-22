package s4a21

import (
	"context"
	"net"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
)

func TestSessionPacketObserverReceivesDrainPackets(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close(); _ = serverConn.Close() })
	session := &Session{
		client: protocol.NewClient(clientConn),
		done:   make(chan struct{}),
	}
	packets := make(chan protocol.Packet, 1)
	cleanup := session.setPacketObserver(func(packet protocol.Packet) {
		packets <- packet
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.drain(ctx)
	if _, err := serverConn.Write(protocol.EncodeResponse(0, 0x001D, []byte{0, 3})); err != nil {
		t.Fatal(err)
	}
	select {
	case packet := <-packets:
		if packet.Type != 0x001D || len(packet.Body) != 2 || packet.Body[1] != 3 {
			t.Fatalf("observed packet = %+v", packet)
		}
	case <-time.After(time.Second):
		t.Fatal("packet observer did not receive drain packet")
	}
	cleanup()
	_ = clientConn.Close()
	_ = serverConn.Close()
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session drain did not stop")
	}
}

func TestSessionStalePacketObserverCleanupDoesNotClearReplacement(t *testing.T) {
	session := &Session{}
	first := make(chan protocol.Packet, 1)
	second := make(chan protocol.Packet, 1)
	cleanupFirst := session.setPacketObserver(func(packet protocol.Packet) { first <- packet })
	cleanupSecond := session.setPacketObserver(func(packet protocol.Packet) { second <- packet })
	cleanupFirst()
	session.dispatchPacket(protocol.Packet{Type: 0x001E})
	select {
	case <-first:
		t.Fatal("stale observer received packet")
	default:
	}
	select {
	case packet := <-second:
		if packet.Type != 0x001E {
			t.Fatalf("replacement observer packet = %+v", packet)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement observer was cleared by stale cleanup")
	}
	cleanupSecond()
}
