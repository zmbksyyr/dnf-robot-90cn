package s4a21

import (
	"context"
	"fmt"
	"net"
	"testing"
)

func TestClientLoginWritesProtocolPacket(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
		if err == nil && (packet.Type != CmdLogin || string(packet.Body[4:9]) != "robot") {
			err = fmt.Errorf("unexpected packet: %+v", packet)
		}
		done <- err
	}()
	if err := client.Login(context.Background(), "robot", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientMethodsUseS4A21Widths(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 2; i++ {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if i == 0 && (packet.Type != CmdSelectCharacter || len(packet.Body) != 2) {
				done <- fmt.Errorf("select packet = %+v", packet)
				return
			}
			if i == 1 && (packet.Type != CmdMoveMap || len(packet.Body) != 64) {
				done <- fmt.Errorf("move packet = %+v", packet)
				return
			}
		}
		done <- nil
	}()
	if err := client.SelectCharacter(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := client.MoveMap(context.Background(), MoveMapRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientVerifiedDungeonPrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdEnterSelectDungeon, 4},
			{CmdSelectDungeon, 15},
			{CmdChangeTutorialFlag, 6},
			{CmdFinishLoading, 0},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("dungeon packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	if err := client.EnterSelectDungeon(context.Background(), 144); err != nil {
		t.Fatal(err)
	}
	if err := client.SelectDungeon(context.Background(), 144, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := client.ChangeTutorialFlag(context.Background(), 30, 1); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishLoading(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientVerifiedQuestPrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdAcceptQuest, 4},
			{CmdSetQuestTrigger, 6},
			{CmdFinishQuest, 10},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("quest packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	if err := client.AcceptQuest(context.Background(), 1016); err != nil {
		t.Fatal(err)
	}
	if err := client.SetQuestTrigger(context.Background(), 1016, 2, true); err != nil {
		t.Fatal(err)
	}
	if err := client.FinishQuest(context.Background(), 1016, -1, 1); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestClientPartyProbePrimitives(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := NewClient(clientConn)
	done := make(chan error, 1)
	go func() {
		want := []struct {
			typ uint16
			len int
		}{
			{CmdSetPartyInfo, 12},
			{CmdRequestPeer, 7},
			{CmdResponsePeer, 7},
			{CmdLeaveParty, 0},
			{CmdWalkoutPartyMember, 1},
		}
		for _, item := range want {
			packet, err := ReadRequestFrame(serverConn, DefaultMaxPacketLength)
			if err != nil {
				done <- err
				return
			}
			if packet.Type != item.typ || len(packet.Body) != item.len {
				done <- fmt.Errorf("party packet type=0x%04X body=%d want type=0x%04X body=%d", packet.Type, len(packet.Body), item.typ, item.len)
				return
			}
		}
		done <- nil
	}()
	settings := []byte{0, 0, 4, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	if err := client.SetPartyInfo(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	if err := client.RequestPeer(context.Background(), 12, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := client.AcceptPartyInvite(context.Background(), 12); err != nil {
		t.Fatal(err)
	}
	if err := client.LeaveParty(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := client.WalkoutPartyMember(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
