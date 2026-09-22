package s4a21

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLivePartyProbe is an opt-in wire probe. It creates two disposable
// sessions, creates a single-member party, and sends one ordinary invite. It
// intentionally stops before accepting the invite: the response body must be
// captured from the target client before it is promoted to a verified codec.
func TestLivePartyProbe(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" || os.Getenv("S4A21_PARTY_LIVE") != "1" {
		t.Skip("set S4A21_TEST_ADDR and S4A21_PARTY_LIVE=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	left, leftCID := livePartyProbeSession(t, ctx, address, "party-a")
	defer left.Close()
	right, rightCID := livePartyProbeSession(t, ctx, address, "party-b")
	defer right.Close()
	t.Logf("party probe sessions: leftCID=%d rightCID=%d", leftCID, rightCID)

	settings := []byte{0, 0, 1, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	if err := left.SetPartyInfo(ctx, settings); err != nil {
		t.Fatal(err)
	}
	drainPartyProbe(t, left, 500*time.Millisecond, "create")

	if err := left.RequestPeer(ctx, uint16(rightCID), 0, 0); err != nil {
		t.Fatal(err)
	}
	drainPartyProbe(t, left, 300*time.Millisecond, "invite-sender")
	invite, err := waitPartyProbePacket(right, NotiRequestPeer, 1200*time.Millisecond, "invite-target")
	if err != nil || len(invite.Body) < 3 {
		t.Fatalf("did not observe party invite notification: type=0x%04X body=%X", invite.Type, invite.Body)
	}
	if err := right.AcceptPartyInvite(ctx, leftCID); err != nil {
		t.Fatal(err)
	}
	senderTypes := collectPartyProbeTypes(t, left, 1200*time.Millisecond, "accepted-sender")
	for _, typ := range []uint16{0x0009, 0x0008, 0x000B, 0x0099} {
		if !senderTypes[typ] {
			t.Fatalf("accepted sender did not receive party packet type=0x%04X; got=%v", typ, senderTypes)
		}
	}
	drainPartyProbe(t, right, 1200*time.Millisecond, "accepted-target")
}

func TestLivePartyMembershipCleanup(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" || os.Getenv("S4A21_PARTY_LIVE") != "1" {
		t.Skip("set S4A21_TEST_ADDR and S4A21_PARTY_LIVE=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	t.Run("leader-kicks-member", func(t *testing.T) {
		leader, member, _, _ := establishLiveParty(t, ctx, address, "kick")
		defer leader.Close()
		defer member.Close()
		if err := leader.WalkoutPartyMember(ctx, 1); err != nil {
			t.Fatal(err)
		}
		types := collectPartyProbeTypes(t, leader, 1200*time.Millisecond, "kick-leader")
		if !types[NotiPartyInfo] {
			t.Fatalf("leader did not receive PARTY_INFO after kick: %v", types)
		}
	})

	t.Run("member-leaves", func(t *testing.T) {
		leader, member, _, _ := establishLiveParty(t, ctx, address, "leave")
		defer leader.Close()
		defer member.Close()
		if err := member.LeaveParty(ctx); err != nil {
			t.Fatal(err)
		}
		types := collectPartyProbeTypes(t, leader, 1200*time.Millisecond, "leave-leader")
		if !types[NotiPartyInfo] {
			t.Fatalf("leader did not receive PARTY_INFO after leave: %v", types)
		}
	})

	t.Run("leader-disconnects", func(t *testing.T) {
		leader, member, _, _ := establishLiveParty(t, ctx, address, "drop")
		defer member.Close()
		if err := leader.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := waitPartyProbePacket(member, NotiPartyInfo, 1800*time.Millisecond, "disconnect-member"); err != nil {
			t.Fatalf("member did not receive PARTY_INFO after leader disconnect: %v", err)
		}
	})
}

func TestLivePartyDungeonSelectionProbe(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" || os.Getenv("S4A21_PARTY_DUNGEON_SELECTION_LIVE") != "1" {
		t.Skip("set S4A21_TEST_ADDR and S4A21_PARTY_DUNGEON_SELECTION_LIVE=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	leader, member, _, _ := establishLiveParty(t, ctx, address, "selection")
	defer leader.Close()
	defer member.Close()

	if err := leader.EnterSelectDungeon(ctx, 144); err != nil {
		t.Fatal(err)
	}
	ack, err := waitPartyProbePacket(leader, CmdEnterSelectDungeon, 5*time.Second, "selection-leader-ack")
	if err != nil {
		t.Fatal(err)
	}
	if ack.Command != 1 || len(ack.Body) == 0 || ack.Body[0] != 1 {
		t.Fatalf("leader ENTER_SELECT_DUNGEON rejected: command=%d body=%X", ack.Command, ack.Body)
	}
	leaderTypes := collectPartyProbeTypes(t, leader, 1200*time.Millisecond, "selection-leader")
	memberTypes := collectPartyProbeTypes(t, member, 1200*time.Millisecond, "selection-member")
	// The first-tutorial path intentionally defers NOTI 0x001B until the
	// tutorial flag is changed. The probe must therefore stop here: observing
	// a START_MAP/FINISH_LOADING would mean the test accidentally crossed into
	// an unverified dungeon run.
	if leaderTypes[NotiStartMap] || leaderTypes[NotiFinishLoading] ||
		memberTypes[NotiStartMap] || memberTypes[NotiFinishLoading] {
		t.Fatalf("selection probe unexpectedly entered a dungeon: leader=%v member=%v", leaderTypes, memberTypes)
	}
	t.Logf("party selection remains partial: leader=%v member=%v", leaderTypes, memberTypes)
}

func establishLiveParty(t *testing.T, ctx context.Context, address, prefix string) (*Client, *Client, uint16, uint16) {
	t.Helper()
	leader, leaderCID := livePartyProbeSession(t, ctx, address, prefix+"-a")
	member, memberCID := livePartyProbeSession(t, ctx, address, prefix+"-b")
	settings := []byte{0, 0, 1, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	if err := leader.SetPartyInfo(ctx, settings); err != nil {
		leader.Close()
		member.Close()
		t.Fatal(err)
	}
	drainPartyProbe(t, leader, 400*time.Millisecond, prefix+"-create")
	if err := leader.RequestPeer(ctx, memberCID, 0, 0); err != nil {
		leader.Close()
		member.Close()
		t.Fatal(err)
	}
	if _, err := waitPartyProbePacket(member, NotiRequestPeer, 1200*time.Millisecond, prefix+"-invite"); err != nil {
		leader.Close()
		member.Close()
		t.Fatal(err)
	}
	if err := member.AcceptPartyInvite(ctx, leaderCID); err != nil {
		leader.Close()
		member.Close()
		t.Fatal(err)
	}
	types := collectPartyProbeTypes(t, leader, 1000*time.Millisecond, prefix+"-accept")
	if !types[NotiPartyInfo] {
		leader.Close()
		member.Close()
		t.Fatalf("party setup did not publish PARTY_INFO: %v", types)
	}
	return leader, member, leaderCID, memberCID
}

func livePartyProbeSession(t *testing.T, ctx context.Context, address, prefix string) (*Client, uint16) {
	t.Helper()
	suffix := time.Now().UnixNano() % 100000000
	client, err := Dial(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	account := fmt.Sprintf("%s%08d", prefix, suffix)
	name := []byte(fmt.Sprintf("%s%06d", prefix[:3], suffix%1000000))
	if err := client.Login(ctx, account, ""); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdLogin, 1); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := client.CreateCharacter(ctx, 0, name); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdCreateCharacter, 1); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if _, err := waitPacketOfType(ctx, client, NotiCharacterList, 0); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := client.SelectCharacter(ctx, 0); err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := waitForPacket(ctx, client, CmdSelectCharacter, 1); err != nil {
		client.Close()
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		packet, err := client.Read(ctx)
		if err != nil {
			client.Close()
			t.Fatal(err)
		}
		if packet.Type != 0x0002 || len(packet.Body) < 5 {
			continue
		}
		// USERINFO subtype 6: subtype:u8, version:u16, owner CID:u16.
		if packet.Body[0] == 6 {
			return client, binary.LittleEndian.Uint16(packet.Body[3:5])
		}
	}
	client.Close()
	t.Fatal("did not observe USERINFO subtype 6 owner CID")
	return nil, 0
}

func drainPartyProbe(t *testing.T, client *Client, duration time.Duration, label string) Packet {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	var last Packet
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return last
		}
		last = packet
		t.Logf("party probe %s packet type=0x%04X command=%d body=%d %X", label, packet.Type, packet.Command, len(packet.Body), packet.Body)
	}
}

func waitPartyProbePacket(client *Client, typ uint16, duration time.Duration, label string) (Packet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return Packet{}, err
		}
		if packet.Type == typ {
			return packet, nil
		}
		_ = label
	}
}

func collectPartyProbeTypes(t *testing.T, client *Client, duration time.Duration, label string) map[uint16]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	types := make(map[uint16]bool)
	for {
		packet, err := client.Read(ctx)
		if err != nil {
			return types
		}
		types[packet.Type] = true
		t.Logf("party probe %s packet type=0x%04X command=%d body=%d %X", label, packet.Type, packet.Command, len(packet.Body), packet.Body)
	}
}
