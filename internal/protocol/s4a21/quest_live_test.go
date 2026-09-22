package s4a21

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLiveAdvertisedQuest1016 is a disposable, server-owned protocol probe.
// It is opt-in because it creates a real account/character and consumes the
// advertised quest on the target server. The workflow is intentionally kept
// in the protocol package; it is not a scheduler or backend capability.
func TestLiveAdvertisedQuest1016(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" || os.Getenv("S4A21_QUEST_LIVE") != "1" {
		t.Skip("set S4A21_TEST_ADDR and S4A21_QUEST_LIVE=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano() % 100000000
	mID := fmt.Sprintf("quest%08d", suffix)
	name := []byte(fmt.Sprintf("qst%08d", suffix))

	client, err := Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
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
	if _, err := waitPacketOfType(ctx, client, NotiCharacterList, 0); err != nil {
		t.Fatalf("character list refresh: %v", err)
	}
	if err := client.SelectCharacter(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdSelectCharacter, 1); err != nil {
		t.Fatalf("select character: %v", err)
	}
	acceptable, err := waitPacketOfType(ctx, client, NotiAcceptableQuestList, 0)
	if err != nil {
		t.Fatalf("acceptable quest list: %v", err)
	}
	list, err := ParseAcceptableQuestList(acceptable.Body)
	if err != nil {
		t.Fatalf("parse acceptable quest list: %v", err)
	}
	if !containsQuest(list.QuestIDs, 1016) {
		t.Fatalf("server did not advertise quest 1016: %+v", list)
	}

	if err := client.AcceptQuest(ctx, 1016); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdAcceptQuest, 1); err != nil {
		t.Fatalf("accept quest: %v", err)
	}
	if err := client.SetQuestTrigger(ctx, 1016, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdSetQuestTrigger, 1); err != nil {
		t.Fatalf("set quest trigger: %v", err)
	}
	if err := client.FinishQuest(ctx, 1016, -1, 1); err != nil {
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdFinishQuest, 1); err != nil {
		t.Fatalf("finish quest: %v", err)
	}
	updated, err := waitPacketOfType(ctx, client, NotiAcceptableQuestList, 0)
	if err != nil {
		t.Fatalf("updated acceptable quest list: %v", err)
	}
	updatedList, err := ParseAcceptableQuestList(updated.Body)
	if err != nil {
		t.Fatalf("parse updated acceptable quest list: %v", err)
	}
	if containsQuest(updatedList.QuestIDs, 1016) {
		t.Fatalf("completed quest remains advertised: %+v", updatedList)
	}
	// FINISH_QUEST projects more than one notification (for example the clear
	// list and daily snapshot). Drain the short tail before closing so the
	// disposable probe does not turn a successful workflow into a server-side
	// broken-pipe diagnostic.
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer drainCancel()
	for {
		if _, err := client.Read(drainCtx); err != nil {
			break
		}
	}
}

func containsQuest(ids []uint16, want uint16) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func waitPacketOfType(ctx context.Context, client *Client, typ uint16, command byte) (Packet, error) {
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return Packet{}, err
		}
		if packet.Type != typ || packet.Command != command {
			continue
		}
		if command == 1 && (len(packet.Body) == 0 || packet.Body[0] != 1) {
			return Packet{}, fmt.Errorf("command 0x%04X rejected with body %v", typ, packet.Body)
		}
		return packet, nil
	}
}
