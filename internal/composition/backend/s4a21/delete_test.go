package s4a21

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/capability/robotstate"
	protocol "robot/internal/protocol/s4a21"
	"robot/internal/shared"
)

func TestCharacterDeleterResolvesCurrentSlotAndChecksAck(t *testing.T) {
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
		rosterRequest, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || rosterRequest.Type != protocol.CmdGetUserInfo || string(rosterRequest.Body) != string([]byte{0, 0, 2}) {
			done <- fmt.Errorf("roster request=%+v err=%v", rosterRequest, err)
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, testRosterBody(4, "robot01"))); err != nil {
			done <- err
			return
		}
		request, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || request.Type != protocol.CmdDeleteCharacter || binary.LittleEndian.Uint16(request.Body[:2]) != 4 || string(request.Body[6:]) != "robot01" {
			done <- fmt.Errorf("delete request=%+v err=%v", request, err)
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(1, protocol.CmdDeleteCharacter, []byte{1, 0, 4, 0})); err != nil {
			done <- err
			return
		}
		confirm, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || confirm.Type != protocol.CmdGetUserInfo {
			done <- fmt.Errorf("confirm roster request=%+v err=%v", confirm, err)
			return
		}
		_, err = conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, emptyRosterBody()))
		done <- err
	}()

	deleted, err := (CharacterDeleter{Address: listener.Addr().String(), Timeout: time.Second}).DeleteCharacter(context.Background(), robotstate.Identity{
		Backend: shared.BackendS4A21, Account: "robot1", CharacterName: "robot01",
	})
	if err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type recordingDeleteProtocol struct {
	identities  []robotstate.Identity
	err         error
	unconfirmed bool
}

func (p *recordingDeleteProtocol) DeleteCharacter(_ context.Context, identity robotstate.Identity) (bool, error) {
	p.identities = append(p.identities, identity)
	return p.err == nil && !p.unconfirmed, p.err
}

type recordingSessionCloser struct{ uids []int }

func (c *recordingSessionCloser) Close(uid int) error {
	c.uids = append(c.uids, uid)
	return nil
}

func TestRobotCleanerDeletesThroughProtocolThenRemovesRobotState(t *testing.T) {
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 7, Name: "robot07"}, {UID: 8, Name: "robot08"}})
	if err := state.RegisterIdentities(context.Background(), []robotstate.Identity{
		{Backend: shared.BackendS4A21, Account: "acct07", CharacterName: "robot07"},
		{Backend: shared.BackendS4A21, Account: "acct08", CharacterName: "robot08"},
	}); err != nil {
		t.Fatal(err)
	}
	protocolDelete := &recordingDeleteProtocol{}
	closer := &recordingSessionCloser{}
	cleaner := RobotCleaner{Protocol: protocolDelete, State: state, Sessions: closer}
	result, err := cleaner.CleanupRobots(context.Background(), robotcap.CleanupRequest{UIDs: []int{7}, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 1 || result.Skipped != 0 || len(protocolDelete.identities) != 1 || protocolDelete.identities[0].Account != "acct07" || len(closer.uids) != 1 || closer.uids[0] != 7 {
		t.Fatalf("result=%+v identities=%+v closed=%v", result, protocolDelete.identities, closer.uids)
	}
	robots, _ := state.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if len(robots) != 1 || robots[0].UID != 8 {
		t.Fatalf("remaining robots = %+v", robots)
	}
}

func TestRobotCleanerDryRunDoesNotSendDelete(t *testing.T) {
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 7, Name: "robot07"}})
	if err := state.RegisterIdentity(context.Background(), robotstate.Identity{Backend: shared.BackendS4A21, Account: "acct07", CharacterName: "robot07"}); err != nil {
		t.Fatal(err)
	}
	protocolDelete := &recordingDeleteProtocol{}
	result, err := (RobotCleaner{Protocol: protocolDelete, State: state}).CleanupRobots(context.Background(), robotcap.CleanupRequest{})
	if err != nil || !result.DryRun || result.Requested != 1 || len(protocolDelete.identities) != 0 {
		t.Fatalf("result=%+v calls=%v err=%v", result, protocolDelete.identities, err)
	}
}

func TestRobotCleanerKeepsStateWhenProtocolDoesNotConfirmDelete(t *testing.T) {
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 7, Name: "robot07"}})
	if err := state.RegisterIdentity(context.Background(), robotstate.Identity{Backend: shared.BackendS4A21, Account: "acct07", CharacterName: "robot07"}); err != nil {
		t.Fatal(err)
	}
	result, err := (RobotCleaner{Protocol: &recordingDeleteProtocol{unconfirmed: true}, State: state}).CleanupRobots(
		context.Background(), robotcap.CleanupRequest{UIDs: []int{7}, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Deleted != 0 || result.Skipped != 1 || !result.Candidates[0].Protected {
		t.Fatalf("result=%+v", result)
	}
	robots, _ := state.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if len(robots) != 1 || robots[0].UID != 7 {
		t.Fatalf("robot state must remain after unconfirmed delete: %+v", robots)
	}
}

func TestCharacterDeleterClearsRosterFromHighestSlot(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.Close()
		steps := []struct {
			wantType     uint16
			wantSlot     uint16
			responseType uint16
			response     []byte
		}{
			{wantType: protocol.CmdLogin, responseType: protocol.CmdLogin, response: []byte{1}},
			{wantType: protocol.CmdGetUserInfo, responseType: protocol.NotiCharacterList, response: testRosterBodies(
				protocol.CharacterRosterEntry{Slot: 1, Name: "robot01", Job: 1, Level: 1},
				protocol.CharacterRosterEntry{Slot: 4, Name: "stale04", Job: 2, Level: 70},
			)},
			{wantType: protocol.CmdDeleteCharacter, wantSlot: 4, responseType: protocol.CmdDeleteCharacter, response: []byte{1, 0, 4, 0}},
			{wantType: protocol.CmdDeleteCharacter, wantSlot: 1, responseType: protocol.CmdDeleteCharacter, response: []byte{1, 0, 1, 0}},
			{wantType: protocol.CmdGetUserInfo, responseType: protocol.NotiCharacterList, response: emptyRosterBody()},
		}
		for _, step := range steps {
			request, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
			if readErr != nil || request.Type != step.wantType {
				done <- fmt.Errorf("request type=0x%04X want=0x%04X err=%v", request.Type, step.wantType, readErr)
				return
			}
			if step.wantType == protocol.CmdDeleteCharacter && binary.LittleEndian.Uint16(request.Body[:2]) != step.wantSlot {
				done <- fmt.Errorf("delete slot=%d want=%d", binary.LittleEndian.Uint16(request.Body[:2]), step.wantSlot)
				return
			}
			command := byte(1)
			if step.responseType == protocol.NotiCharacterList {
				command = 0
			}
			if _, writeErr := conn.Write(protocol.EncodeResponse(command, step.responseType, step.response)); writeErr != nil {
				done <- writeErr
				return
			}
		}
		done <- nil
	}()

	deleted, err := (CharacterDeleter{Address: listener.Addr().String(), Timeout: time.Second}).DeleteCharacter(
		context.Background(), robotstate.Identity{Backend: shared.BackendS4A21, Account: "robot1", CharacterName: "robot01"})
	if err != nil || !deleted {
		t.Fatalf("deleted=%t err=%v", deleted, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLiveDeleteCharacterThroughProtocol(t *testing.T) {
	address := os.Getenv("S4A21_TEST_ADDR")
	if address == "" {
		t.Skip("S4A21_TEST_ADDR is not set")
	}
	suffix := strconv.FormatInt(time.Now().UnixNano()%100000000, 10)
	account, name := "del"+suffix, "d"+suffix
	created, err := (Provisioner{Address: address}).ProvisionCharacter(context.Background(), shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: name, Job: 1,
	})
	if err != nil || !created.Created {
		t.Fatalf("create result=%+v err=%v", created, err)
	}
	deleter := CharacterDeleter{Address: address}
	identity := robotstate.Identity{Backend: shared.BackendS4A21, Account: account, CharacterName: name}
	state := robotstate.NewMemoryStore([]robotcap.Info{{UID: 99000001, Name: name}})
	if err := state.RegisterIdentity(context.Background(), identity); err != nil {
		t.Fatal(err)
	}
	result, err := (RobotCleaner{Protocol: deleter, State: state}).CleanupRobots(context.Background(), robotcap.CleanupRequest{UIDs: []int{99000001}, Force: true})
	if err != nil || result.Deleted != 1 || result.Skipped != 0 {
		t.Fatalf("cleanup result=%+v err=%v", result, err)
	}
	robots, _ := state.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if len(robots) != 0 {
		t.Fatalf("robot state was not removed: %+v", robots)
	}
	deleted, err := deleter.DeleteCharacter(context.Background(), identity)
	if err != nil || !deleted {
		t.Fatalf("second delete must confirm the account is already empty: deleted=%t err=%v", deleted, err)
	}
}

func testRosterBody(slot uint16, name string) []byte {
	return testRosterBodies(protocol.CharacterRosterEntry{Slot: slot, Name: name, Job: 1, Level: 1})
}

func testRosterBodies(entries ...protocol.CharacterRosterEntry) []byte {
	var body bytes.Buffer
	body.Write(make([]byte, 16))
	_ = binary.Write(&body, binary.LittleEndian, uint16(len(entries)))
	for _, entry := range entries {
		_ = binary.Write(&body, binary.LittleEndian, entry.Slot)
		_ = binary.Write(&body, binary.LittleEndian, uint32(len(entry.Name)))
		body.WriteString(entry.Name)
		body.Write([]byte{0, 0, entry.Job, entry.Grow, entry.Level, 0, 0})
		body.Write(make([]byte, 8))
		body.WriteByte(0)
		body.Write(make([]byte, 36))
	}
	return body.Bytes()
}
