package s4a21

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

func TestSessionTranslatesSharedIntents(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go serveSessionSequence(listener, done)
	session, err := (SessionFactory{Address: listener.Addr().String(), Timeout: time.Second}).OpenSession(context.Background(), shared.OpenSessionRequest{AccountName: "robot", CharacterSlot: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.MoveTown(context.Background(), shared.TownMoveIntent{X: 100, Y: 200}); err != nil {
		t.Fatal(err)
	}
	if err := session.Shout(context.Background(), shared.ShoutIntent{Channel: shared.ShoutChannelArea, Message: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := session.MoveDungeon(context.Background(), shared.DungeonMoveIntent{NextX: 1, NextY: 2}); err == nil {
		t.Fatal("dungeon movement unexpectedly succeeded")
	} else if _, ok := err.(shared.UnsupportedCapabilityError); !ok {
		t.Fatalf("dungeon movement error = %T %v", err, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveSessionTownMoveAndShout(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" {
		t.Skip("S4A21_TEST_ADDR is not set")
	}
	suffix := time.Now().UnixNano() % 100000000
	account := fmt.Sprintf("robot%08d", suffix)
	name := fmt.Sprintf("rb%08d", suffix)
	if _, err := (Provisioner{Address: address}).ProvisionCharacter(context.Background(), shared.ProvisionCharacterRequest{AccountName: account, CharacterName: name, Job: 2}); err != nil {
		t.Fatal(err)
	}
	session, err := (SessionFactory{Address: address}).OpenSession(context.Background(), shared.OpenSessionRequest{AccountName: account})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.MoveTown(context.Background(), shared.TownMoveIntent{X: 480, Y: 240, Direction: 5}); err != nil {
		t.Fatal(err)
	}
	if err := session.Shout(context.Background(), shared.ShoutIntent{Channel: shared.ShoutChannelArea, Message: "robot protocol test"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
}

func TestSessionFactoryEnablesPartyDungeonFollower(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	release := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer conn.Close()
		for _, typ := range []uint16{
			protocol.CmdLogin,
			protocol.CmdSelectCharacter,
			protocol.CmdCheckConnection,
			protocol.CmdChangeTutorialFlag,
		} {
			packet, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
			if readErr != nil {
				serverDone <- readErr
				return
			}
			if packet.Type != typ {
				serverDone <- fmt.Errorf("request type=0x%04X want=0x%04X", packet.Type, typ)
				return
			}
			body := []byte{1}
			if typ == protocol.CmdSelectCharacter {
				body = make([]byte, 11)
				body[0], body[9], body[10] = 1, 0x34, 0x12
			}
			if _, writeErr := conn.Write(protocol.EncodeResponse(1, typ, body)); writeErr != nil {
				serverDone <- writeErr
				return
			}
		}
		<-release
		serverDone <- nil
	}()

	session, err := (SessionFactory{Address: listener.Addr().String(), Timeout: time.Second}).OpenSession(
		context.Background(), shared.OpenSessionRequest{
			AccountName: "robot", EnablePartyDungeonFollower: true,
		})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestSessionReturnsUnsupportedWorldShout(t *testing.T) {
	session := &Session{client: protocol.NewClient(nil)}
	err := session.Shout(context.Background(), shared.ShoutIntent{Channel: shared.ShoutChannelWorld, Message: "hello"})
	unsupported, ok := err.(shared.UnsupportedCapabilityError)
	if !ok || unsupported.Operation != shared.CapabilityWorldShout {
		t.Fatalf("error = %T %v", err, err)
	}
}

func TestSessionReturnsUnsupportedPartyShout(t *testing.T) {
	session := &Session{client: protocol.NewClient(nil)}
	err := session.Shout(context.Background(), shared.ShoutIntent{Channel: shared.ShoutChannelParty, Message: "hello"})
	unsupported, ok := err.(shared.UnsupportedCapabilityError)
	if !ok || unsupported.Operation != shared.CapabilityShout || unsupported.Reason != "S4A21 party-recipient message mode is not verified" {
		t.Fatalf("error = %T %v", err, err)
	}
}

func serveSessionSequence(listener net.Listener, done chan<- error) {
	conn, err := listener.Accept()
	if err != nil {
		done <- err
		return
	}
	defer conn.Close()
	want := []uint16{protocol.CmdLogin, protocol.CmdSelectCharacter, protocol.CmdCheckConnection, protocol.CmdSetUserPosition, protocol.CmdSendMessage}
	for index, typ := range want {
		packet, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if readErr != nil {
			done <- readErr
			return
		}
		if packet.Type != typ {
			done <- fmt.Errorf("packet %d type=0x%04X want=0x%04X", index, packet.Type, typ)
			return
		}
		if index < 3 {
			if _, err = conn.Write(protocol.EncodeResponse(1, typ, []byte{1})); err != nil {
				done <- err
				return
			}
		}
	}
	done <- nil
}
