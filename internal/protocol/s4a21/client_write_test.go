package s4a21

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestSendTimesOutWithoutContextDeadline(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	client := NewClient(local)
	client.writeTimeout = 50 * time.Millisecond

	start := time.Now()
	err := client.send(context.Background(), []byte("frame"))
	if err == nil {
		t.Fatal("send succeeded against a non-reading peer")
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("send error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("send blocked for %v", elapsed)
	}
}

func TestSendRejectsClosedClient(t *testing.T) {
	var client *Client
	if err := client.send(context.Background(), nil); err == nil {
		t.Fatal("nil client send succeeded")
	}
	if err := NewClient(nil).send(context.Background(), nil); err == nil {
		t.Fatal("client without connection send succeeded")
	}
	if err := NewClient(nil).Run(context.Background(), nil); err == nil {
		t.Fatal("client without connection Run succeeded")
	}
	var nilClient *Client
	if err := nilClient.Run(context.Background(), nil); err == nil {
		t.Fatal("nil client Run succeeded")
	}
}
