package cn90

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestLiveLoginAndCreateCharacter(t *testing.T) {
	address := os.Getenv("CN90_TEST_ADDR")
	if address == "" {
		t.Skip("CN90_TEST_ADDR is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	suffix := time.Now().UnixNano() % 100000000
	mID := fmt.Sprintf("robot%08d", suffix)
	name := []byte(fmt.Sprintf("rb%08d", suffix))
	if err := client.Login(ctx, mID, ""); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdLogin, 1); err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := client.CreateCharacter(ctx, 0, name); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdCreateCharacter, 1); err != nil {
		t.Fatalf("create character: %v", err)
	}
	if err := waitForPacket(ctx, client, NotiCharacterList, 0); err != nil {
		t.Fatalf("character list refresh: %v", err)
	}
}

func waitForPacket(ctx context.Context, client *Client, typ uint16, command byte) error {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return err
		}
		if packet.Type == typ && packet.Command == command {
			if command == 1 && (len(packet.Body) == 0 || packet.Body[0] != 1) {
				return fmt.Errorf("command 0x%04X rejected with body %v", typ, packet.Body)
			}
			return nil
		}
	}
}
