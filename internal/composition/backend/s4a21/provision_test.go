package s4a21

import (
	"bytes"
	"context"
	"encoding/binary"
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
		rosterRequest, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || rosterRequest.Type != protocol.CmdGetUserInfo {
			done <- fmt.Errorf("roster request type=0x%04X err=%v", rosterRequest.Type, err)
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, emptyRosterBody())); err != nil {
			done <- err
			return
		}
		checkName, err := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if err != nil || checkName.Type != protocol.CmdCheckCharacterName {
			done <- fmt.Errorf("check name type=0x%04X err=%v", checkName.Type, err)
			return
		}
		if _, err = conn.Write(protocol.EncodeResponse(1, protocol.CmdCheckCharacterName, []byte{1})); err != nil {
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

func TestProvisionCharacterChecksGlobalNameAndUsesStableFallback(t *testing.T) {
	for _, rejectionCode := range []byte{24, 159} {
		t.Run(fmt.Sprintf("code_%d", rejectionCode), func(t *testing.T) {
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
				for _, step := range []struct {
					wantType uint16
					response []byte
				}{
					{protocol.CmdLogin, []byte{1}},
					{protocol.CmdGetUserInfo, emptyRosterBody()},
					{protocol.CmdCheckCharacterName, []byte{0, rejectionCode}},
					{protocol.CmdCheckCharacterName, []byte{1}},
					{protocol.CmdCreateCharacter, []byte{1}},
				} {
					request, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
					if readErr != nil || request.Type != step.wantType {
						done <- fmt.Errorf("request type=0x%04X want=0x%04X err=%v", request.Type, step.wantType, readErr)
						return
					}
					responseType := step.wantType
					command := byte(1)
					if step.wantType == protocol.CmdGetUserInfo {
						responseType = protocol.NotiCharacterList
						command = 0
					}
					if _, writeErr := conn.Write(protocol.EncodeResponse(command, responseType, step.response)); writeErr != nil {
						done <- writeErr
						return
					}
				}
				_, writeErr := conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, []byte{0}))
				done <- writeErr
			}()

			result, err := (Provisioner{Address: listener.Addr().String(), Timeout: time.Second}).ProvisionCharacter(
				context.Background(), shared.ProvisionCharacterRequest{
					AccountName: "robot42", CharacterName: "bad name", RobotUID: 42, Job: 1,
				})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !result.Created || result.CharacterName != "rb42" || result.RobotUID != 42 {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestProvisionCharacterAdoptsExistingRosterIdentity(t *testing.T) {
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
		login, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if readErr != nil || login.Type != protocol.CmdLogin {
			done <- fmt.Errorf("login type=0x%04X err=%v", login.Type, readErr)
			return
		}
		if _, writeErr := conn.Write(protocol.EncodeResponse(1, protocol.CmdLogin, []byte{1})); writeErr != nil {
			done <- writeErr
			return
		}
		request, readErr := protocol.ReadRequestFrame(conn, protocol.DefaultMaxPacketLength)
		if readErr != nil || request.Type != protocol.CmdGetUserInfo {
			done <- fmt.Errorf("roster request type=0x%04X err=%v", request.Type, readErr)
			return
		}
		_, writeErr := conn.Write(protocol.EncodeResponse(0, protocol.NotiCharacterList, rosterBody(3, "robot01", 2)))
		done <- writeErr
	}()
	result, err := (Provisioner{Address: listener.Addr().String(), Timeout: time.Second}).ProvisionCharacter(
		context.Background(), shared.ProvisionCharacterRequest{AccountName: "robot1", CharacterName: "robot01", Job: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.BackendSlot == nil || *result.BackendSlot != 3 {
		t.Fatalf("adopted result = %+v", result)
	}
}

func emptyRosterBody() []byte {
	body := make([]byte, 18)
	body[0] = 2
	return body
}

func rosterBody(slot uint16, name string, job byte) []byte {
	var body bytes.Buffer
	body.Write(make([]byte, 16))
	body.Bytes()[0] = 2
	_ = binary.Write(&body, binary.LittleEndian, uint16(1))
	_ = binary.Write(&body, binary.LittleEndian, slot)
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(name)))
	body.WriteString(name)
	body.Write([]byte{0, 0, job, 0, 1, 0, 0})
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	body.WriteByte(0)
	body.Write(make([]byte, 36))
	return body.Bytes()
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
