package s4a21

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

func TestProvisionCharacterFollowsProtocolSequence(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		login, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || login.Type != protocol.CmdLogin {
			done <- err
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(1, protocol.CmdLogin, []byte{1})); err != nil {
			done <- err
			return
		}
		create, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || create.Type != protocol.CmdCreateCharacter {
			done <- err
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(1, protocol.CmdCreateCharacter, []byte{1})); err == nil {
			_, err = conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, []byte{0}))
		}
		done <- err
	}()
	result, err := (Provisioner{Address: listener.Addr().String(), Timeout: time.Second}).ProvisionCharacter(context.Background(), shared.ProvisionCharacterRequest{AccountName: "robot1", CharacterName: "测试一", Job: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Backend != shared.BackendS4A21 {
		t.Fatalf("result = %+v", result)
	}
}

func TestProvisionCharactersStopsWithPartialResults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := (Provisioner{}).ProvisionCharacters(ctx, []shared.ProvisionCharacterRequest{{AccountName: "a", CharacterName: "ab"}})
	if !errors.Is(err, context.Canceled) || len(results) != 0 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

func TestLiveProvisionCharacter(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" {
		t.Skip("S4A21_TEST_ADDR is not set")
	}
	suffix := time.Now().UnixNano() % 100000000
	result, err := (Provisioner{Address: address}).ProvisionCharacter(context.Background(), shared.ProvisionCharacterRequest{
		AccountName: fmt.Sprintf("robot%08d", suffix), CharacterName: fmt.Sprintf("rb%08d", suffix), Job: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created {
		t.Fatalf("result = %+v", result)
	}
}
