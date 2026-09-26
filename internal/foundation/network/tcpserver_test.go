package network

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

func TestPendingLimitBoundsConnectionsAcrossIPs(t *testing.T) {
	server := NewTCPServer("")
	server.maxPending = 2
	server.maxPendingIP = 2

	server.addPendingLocked("10.0.0.1")
	server.addPendingLocked("10.0.0.2")
	if !server.pendingLimitReachedLocked("10.0.0.3") {
		t.Fatal("global pending limit did not reject a third source IP")
	}

	server.removePendingLocked("10.0.0.1")
	if server.pendingLimitReachedLocked("10.0.0.3") {
		t.Fatal("released pending connection did not free global capacity")
	}
	if server.pendingCount != 1 || server.pendingByIP["10.0.0.2"] != 1 {
		t.Fatalf("pending state was not balanced: total=%d by_ip=%v", server.pendingCount, server.pendingByIP)
	}
}

func TestPendingLimitBoundsSingleIP(t *testing.T) {
	server := NewTCPServer("")
	server.maxPending = 10
	server.maxPendingIP = 1
	server.addPendingLocked("10.0.0.1")
	if !server.pendingLimitReachedLocked("10.0.0.1") {
		t.Fatal("per-IP pending limit was not enforced")
	}
	if server.pendingLimitReachedLocked("10.0.0.2") {
		t.Fatal("per-IP pending limit rejected an unrelated IP")
	}
}

func TestReleaseClientDoesNotDeleteReusedClientID(t *testing.T) {
	server := NewTCPServer("")
	oldClient := &tcpClient{}
	newClient := &tcpClient{}
	const clientID = "127.0.0.1:50000"
	server.clients[clientID] = newClient
	server.connCount = 2

	server.releaseClientLocked(clientID, "127.0.0.1", oldClient, true, true)

	if server.clients[clientID] != newClient {
		t.Fatal("old handler removed the newer connection")
	}
	if server.connCount != 1 {
		t.Fatalf("registered connection count = %d, want 1", server.connCount)
	}
}

func TestOversizedCompletePacketIsRejectedBeforeDispatch(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	server := NewTCPServer("")
	called := make(chan struct{}, 1)
	server.OnMessage(func(string, []byte) { called <- struct{}{} })
	done := make(chan struct{})
	server.wg.Add(1)
	go func() {
		server.handleClient("test", "127.0.0.1", &tcpClient{conn: serverConn})
		close(done)
	}()

	packet := append([]byte("<tw>"), bytes.Repeat([]byte("x"), maxReceiveBufferSize)...)
	packet = append(packet, []byte("</tw>")...)
	if _, err := clientConn.Write(packet); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("server did not close an oversized complete packet")
	}
	select {
	case <-called:
		t.Fatal("oversized packet reached the message handler")
	default:
	}
}

func TestTCPServerContainsMessageHandlerPanic(t *testing.T) {
	server := NewTCPServer("")
	calls := 0
	server.OnMessage(func(string, []byte) {
		calls++
		if calls == 1 {
			panic("handler failed")
		}
	})
	server.dispatchMessage("client", []byte("<tw></tw>"))
	server.dispatchMessage("client", []byte("<tw></tw>"))
	if calls != 2 {
		t.Fatalf("message handler calls = %d, want 2", calls)
	}
}

func TestTCPServerRejectsDuplicateStartAndRestartAfterClose(t *testing.T) {
	server := NewTCPServer("127.0.0.1:0")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); !errors.Is(err, ErrTCPServerRunning) {
		t.Fatalf("duplicate start error = %v, want %v", err, ErrTCPServerRunning)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); !errors.Is(err, ErrTCPServerClosed) {
		t.Fatalf("restart error = %v, want %v", err, ErrTCPServerClosed)
	}
}

func TestTCPServerCloseIsBoundedWhenHandlerBlocks(t *testing.T) {
	server := NewTCPServer("127.0.0.1:0")
	server.closeWaitTimeout = 50 * time.Millisecond
	release := make(chan struct{})
	handlerStarted := make(chan struct{}, 1)
	server.OnMessage(func(string, []byte) {
		handlerStarted <- struct{}{}
		<-release
	})
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("tcp", server.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write([]byte("<tw><c>sys</c></tw>")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	start := time.Now()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Close waited %v for a blocked handler", elapsed)
	}
	close(release)
}

func TestTCPServerZeroValueCanStartAndClose(t *testing.T) {
	server := &TCPServer{addr: "127.0.0.1:0"}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}
