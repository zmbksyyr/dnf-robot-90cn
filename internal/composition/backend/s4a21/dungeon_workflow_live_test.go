package s4a21

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"robot/internal/shared"
)

func TestLiveS4A21SingleDungeonWorkflow(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if os.Getenv("S4A21_DUNGEON_WORKFLOW_LIVE") != "1" || address == "" {
		t.Skip("S4A21_DUNGEON_WORKFLOW_LIVE and S4A21_TEST_ADDR are required")
	}
	suffix := time.Now().UnixNano() % 100000000
	account := fmt.Sprintf("wf%08d", suffix)
	name := fmt.Sprintf("wf%08d", suffix)
	if _, err := (Provisioner{Address: address, Timeout: 20 * time.Second}).ProvisionCharacter(
		context.Background(), shared.ProvisionCharacterRequest{AccountName: account, CharacterName: name, Job: 2}); err != nil {
		t.Fatal(err)
	}
	sessionValue, err := (SessionFactory{Address: address, Timeout: 20 * time.Second}).OpenSession(
		context.Background(), shared.OpenSessionRequest{AccountName: account, CharacterSlot: 0})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := sessionValue.(*Session)
	if !ok {
		_ = sessionValue.Close()
		t.Fatalf("session type = %T", sessionValue)
	}
	defer session.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	snapshot, err := session.enterSingleDungeon(ctx, 144, true)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Phase != uint8(dungeonPhaseReady) || snapshot.DungeonID != 144 {
		t.Fatalf("live dungeon snapshot = %+v", snapshot)
	}
}
