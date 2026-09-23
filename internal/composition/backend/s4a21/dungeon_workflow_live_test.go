package s4a21

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"robot/internal/capability/robotstate"
	protocol "robot/internal/protocol/s4a21"
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
	next, err := session.moveSingleDungeon(ctx, 1, 3, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != uint8(dungeonPhaseReady) || next.RoomX != 1 || next.RoomY != 3 {
		t.Fatalf("live moved dungeon snapshot = %+v", next)
	}
}

func TestLiveS4A21ProductionSessionFollowsPartyDungeon(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if os.Getenv("S4A21_SESSION_FOLLOW_LIVE") != "1" || address == "" {
		t.Skip("S4A21_SESSION_FOLLOW_LIVE and S4A21_TEST_ADDR are required")
	}

	suffix := time.Now().UnixNano() % 100000000
	leaderAccount := fmt.Sprintf("pl%08d", suffix)
	leaderName := fmt.Sprintf("pl%08d", suffix)
	provisioner := Provisioner{Address: address, Timeout: 20 * time.Second}
	deleter := CharacterDeleter{Address: address, Timeout: 20 * time.Second}

	provisionLiveCharacter(t, provisioner, leaderAccount, leaderName)
	defer deleteLiveCharacter(t, deleter, leaderAccount, leaderName)
	followerAccounts := make([]string, 3)
	for i := range followerAccounts {
		account := fmt.Sprintf("p%d%08d", i+1, suffix)
		followerAccounts[i] = account
		provisionLiveCharacter(t, provisioner, account, account)
		defer deleteLiveCharacter(t, deleter, account, account)
	}

	factory := SessionFactory{Address: address, Timeout: 20 * time.Second}
	leader := openLiveSession(t, factory, leaderAccount, true)
	defer leader.Close()
	leader.DisableDungeonFollower()
	followers := make([]*Session, 0, len(followerAccounts))
	for _, account := range followerAccounts {
		follower := openLiveSession(t, factory, account, true)
		defer follower.Close()
		followers = append(followers, follower)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	createLiveSessionParty(t, ctx, leader, followers...)

	entered, err := leader.enterSingleDungeon(ctx, 144, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, follower := range followers {
		waitLiveFollowerSnapshot(t, ctx, follower, entered.RoomX, entered.RoomY)
	}

	nextX := entered.RoomX
	if nextX == 0 {
		nextX++
	} else {
		nextX--
	}
	moved, err := leader.moveSingleDungeon(ctx, nextX, entered.RoomY, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, follower := range followers {
		waitLiveFollowerSnapshot(t, ctx, follower, moved.RoomX, moved.RoomY)
	}
}

func provisionLiveCharacter(t *testing.T, provisioner Provisioner, account, name string) {
	t.Helper()
	result, err := provisioner.ProvisionCharacter(context.Background(), shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: name, Job: 2,
	})
	if err != nil || !result.Created {
		t.Fatalf("provision %s: result=%+v err=%v", account, result, err)
	}
}

func deleteLiveCharacter(t *testing.T, deleter CharacterDeleter, account, name string) {
	t.Helper()
	deleted, err := deleter.DeleteCharacter(context.Background(), robotstate.Identity{
		Backend: shared.BackendS4A21, Account: account, CharacterName: name,
	})
	if err != nil || !deleted {
		t.Errorf("delete %s: deleted=%t err=%v", account, deleted, err)
	}
}

func openLiveSession(t *testing.T, factory SessionFactory, account string, follower bool) *Session {
	t.Helper()
	value, err := factory.OpenSession(context.Background(), shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: 0, EnablePartyDungeonFollower: follower,
	})
	if err != nil {
		t.Fatal(err)
	}
	session, ok := value.(*Session)
	if !ok {
		_ = value.Close()
		t.Fatalf("session type = %T", value)
	}
	return session
}

func createLiveSessionParty(t *testing.T, ctx context.Context, leader *Session, followers ...*Session) {
	t.Helper()
	partyInfo := make(chan protocol.Packet, 8)
	cleanup := leader.setPacketObserver(func(packet protocol.Packet) {
		if packet.Type == protocol.NotiPartyInfo {
			select {
			case partyInfo <- packet:
			default:
			}
		}
	})
	defer cleanup()
	settings := []byte{0, 0, 1, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	if err := leader.client.SetPartyInfo(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := waitDungeonPacket(ctx, partyInfo, protocol.NotiPartyInfo); err != nil {
		t.Fatal(err)
	}
	for _, follower := range followers {
		followerPartyInfo := make(chan protocol.Packet, 8)
		cleanupFollower := follower.setPacketObserver(func(packet protocol.Packet) {
			if packet.Type == protocol.NotiPartyInfo {
				select {
				case followerPartyInfo <- packet:
				default:
				}
			}
		})
		if err := leader.client.RequestPeer(ctx, follower.selfUID, 0, 0); err != nil {
			cleanupFollower()
			t.Fatal(err)
		}
		var lastPartyInfo protocol.Packet
		for !follower.PartyActive() {
			select {
			case <-ctx.Done():
				cleanupFollower()
				partyID, cleared, ok := parsePartyInfoProjection(lastPartyInfo, follower.selfUID)
				t.Fatalf("party activation: %v; selfUID=%d last=%X parsedParty=%d cleared=%v parsed=%t", ctx.Err(), follower.selfUID, lastPartyInfo.Body, partyID, cleared, ok)
			case lastPartyInfo = <-followerPartyInfo:
			case <-time.After(10 * time.Millisecond):
			}
		}
		cleanupFollower()
	}
}

func waitLiveFollowerSnapshot(t *testing.T, ctx context.Context, follower *Session, roomX, roomY byte) {
	t.Helper()
	for {
		follower.dungeonStateGuard.Lock()
		state := follower.dungeonState
		var snapshot dungeonRunSnapshot
		if state != nil {
			snapshot = state.Snapshot()
		}
		follower.dungeonStateGuard.Unlock()
		if state != nil && snapshot.Phase == uint8(dungeonPhaseReady) && snapshot.RoomX == roomX && snapshot.RoomY == roomY {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("follower room (%d,%d) was not ready; last=%+v", roomX, roomY, snapshot)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
