package s4a21

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	foundationlog "robot/internal/foundation/log"
)

// TestLiveS4A21PartyUDPPositionFollow proves the in-dungeon UDP position path
// against a real S4A21 server: the leader broadcasts retail-shaped position
// frames on the party data plane and the follower mirrors them through a TCP
// move plus its own UDP echo. The leader's TCP projection would also reach the
// follower, so the assertion is on the UDP trace line specifically.
func TestLiveS4A21PartyUDPPositionFollow(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if os.Getenv("S4A21_UDP_FOLLOW_LIVE") != "1" || address == "" {
		t.Skip("S4A21_UDP_FOLLOW_LIVE and S4A21_TEST_ADDR are required")
	}
	t.Setenv("S4A21_PARTY_FOLLOW_TRACE", "1")

	traces := make(chan string, 1024)
	foundationlog.SetRobotSink(func(message string) {
		select {
		case traces <- strings.TrimSpace(message):
		default:
		}
	})
	defer foundationlog.SetRobotSink(nil)

	suffix := time.Now().UnixNano() % 100000000
	leaderAccount := fmt.Sprintf("ul%08d", suffix)
	followerAccount := fmt.Sprintf("uf%08d", suffix)
	provisioner := Provisioner{Address: address, Timeout: 20 * time.Second}
	deleter := CharacterDeleter{Address: address, Timeout: 20 * time.Second}
	provisionLiveCharacter(t, provisioner, leaderAccount, leaderAccount)
	defer deleteLiveCharacter(t, deleter, leaderAccount, leaderAccount)
	provisionLiveCharacter(t, provisioner, followerAccount, followerAccount)
	defer deleteLiveCharacter(t, deleter, followerAccount, followerAccount)

	factory := SessionFactory{Address: address, Timeout: 20 * time.Second}
	leader := openLiveSession(t, factory, leaderAccount, true)
	defer leader.Close()
	leader.DisableDungeonFollower()
	follower := openLiveSession(t, factory, followerAccount, true)
	defer follower.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	createLiveSessionParty(t, ctx, leader, follower)

	entered, err := leader.enterSingleDungeon(ctx, 144, false)
	if err != nil {
		t.Fatal(err)
	}
	waitLiveFollowerSnapshot(t, ctx, follower, entered.RoomX, entered.RoomY)
	waitLivePartyUDPReady(t, ctx, leader)
	waitLivePartyUDPReady(t, ctx, follower)

	path := []struct{ x, y int16 }{
		{320, 240},
		{368, 262},
		{416, 284},
	}
	for _, step := range path {
		if err := leader.client.SetUserPosition(ctx, step.x, step.y, 5, 0); err != nil {
			t.Fatal(err)
		}
		if err := leader.client.SendPartyPosition(step.x, step.y); err != nil {
			t.Fatalf("leader UDP position broadcast: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}

	last := path[len(path)-1]
	deadline := time.Now().Add(10 * time.Second)
	var seen []string
	for time.Now().Before(deadline) {
		select {
		case trace := <-traces:
			seen = append(seen, trace)
			if strings.Contains(trace, "S4A21_FOLLOW_TRACE_UDP_POS") &&
				strings.Contains(trace, fmt.Sprintf("x=%d y=%d", last.x, last.y)) {
				return
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatalf("follower never mirrored the last UDP position %d,%d; traces=%v", last.x, last.y, seen)
}

func waitLivePartyUDPReady(t *testing.T, ctx context.Context, session *Session) {
	t.Helper()
	for {
		if session.client.PartyUDPReady() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("party UDP endpoint was not ready: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
