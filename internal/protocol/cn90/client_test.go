package cn90

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// TestCompleteHandshakeBoundSession drives the account-bound exchange: the
// server sends CHANNELINFO first, expects the 590-byte endpoint request, then
// answers with the endpoint success.
func TestCompleteHandshakeBoundSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)

	type observation struct {
		typ     uint16
		bodyLen int
	}
	observed := make(chan observation, 1)
	go func() {
		_, _ = serverConn.Write(buildServerUpper(ClassNotice, NotiChannelInfo, []byte{1, 2, 3, 4}, 0))
		header := make([]byte, legacyHeaderSize)
		if _, err := io.ReadFull(serverConn, header); err != nil {
			return
		}
		length := int(binary.LittleEndian.Uint32(header[3:7]))
		body := make([]byte, length-legacyHeaderSize)
		if _, err := io.ReadFull(serverConn, body); err != nil {
			return
		}
		observed <- observation{typ: binary.LittleEndian.Uint16(header[1:3]), bodyLen: len(body)}
		success := make([]byte, 6)
		success[0] = 1
		_, _ = serverConn.Write(buildServerUpper(ClassCommand, ResponseEndpoint, success, 1))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	packet, err := client.CompleteHandshake(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Type != ResponseEndpoint || !EndpointResultOK(packet.Body) {
		t.Fatalf("handshake packet = %+v", packet)
	}
	select {
	case got := <-observed:
		if got.typ != CmdEndpointRequest || got.bodyLen != EndpointRequestSize {
			t.Fatalf("endpoint request = type %d len %d", got.typ, got.bodyLen)
		}
	case <-ctx.Done():
		t.Fatal("server did not observe the endpoint request")
	}
}

// TestCompleteHandshakeUnboundSession covers the fallback-account path where
// the server answers immediately without a CHANNELINFO notice.
func TestCompleteHandshakeUnboundSession(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)

	go func() {
		success := []byte{1}
		_, _ = serverConn.Write(buildServerUpper(ClassCommand, ResponseEndpoint, success, 0))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.CompleteHandshake(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientMethodsWriteLegacyFrames(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)

	type exchange struct {
		typ  uint16
		body []byte
	}
	received := make(chan exchange, 8)
	go func() {
		for i := 0; i < 8; i++ {
			header := make([]byte, legacyHeaderSize)
			if _, err := io.ReadFull(serverConn, header); err != nil {
				return
			}
			length := int(binary.LittleEndian.Uint32(header[3:7]))
			body := make([]byte, length-legacyHeaderSize)
			if _, err := io.ReadFull(serverConn, body); err != nil {
				return
			}
			received <- exchange{typ: binary.LittleEndian.Uint16(header[1:3]), body: body}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.RequestRoster(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.SelectCharacter(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := client.ProgressInitialTown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.SetUserArea(ctx, 38, 1, 100, 200, 5); err != nil {
		t.Fatal(err)
	}
	if err := client.SetUserPosition(ctx, 100, 200, 5, 100); err != nil {
		t.Fatal(err)
	}
	if err := client.CheckConnection(ctx); err != nil {
		t.Fatal(err)
	}
	if err := client.CreateCharacter(ctx, 3, []byte{0xB5, 0xC4}); err != nil {
		t.Fatal(err)
	}
	if err := client.Exit(ctx); err != nil {
		t.Fatal(err)
	}

	want := []uint16{CmdGetUserInfo, CmdSelectCharacter, CmdChangeTutorial, CmdSetUserArea, CmdSetUserPosition, CmdCheckConnection, CmdCreateCharacter, CmdExit}
	for i, typ := range want {
		select {
		case got := <-received:
			if got.typ != typ {
				t.Fatalf("frame %d type = %d want %d", i, got.typ, typ)
			}
			if i == 1 {
				if len(got.body) != 16 || binary.LittleEndian.Uint32(got.body) != 2 {
					t.Fatalf("select body = %X", got.body)
				}
			}
			if i == 3 {
				if len(got.body) != 16 || got.body[0] != 38 || got.body[1] != 1 {
					t.Fatalf("area body = %X", got.body)
				}
			}
		case <-ctx.Done():
			t.Fatalf("frame %d was not received", i)
		}
	}
}

func TestReadPacketRejectsBrokenHeader(t *testing.T) {
	frame := buildServerUpper(ClassCommand, ResponseSelect, []byte{1}, 1)
	binary.LittleEndian.PutUint32(frame[3:7], 4)
	if _, err := ReadPacket(bytes.NewReader(frame), upperHeaderSize16, DefaultMaxPacketSize); err == nil {
		t.Fatal("short declared length was accepted")
	}
}
