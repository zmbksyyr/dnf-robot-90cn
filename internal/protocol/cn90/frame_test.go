package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildServerUpper mirrors the server's 16-byte upper envelope for tests.
func buildServerUpper(class byte, typ uint16, body []byte, seq uint16) []byte {
	frame := make([]byte, upperHeaderSize16+len(body))
	frame[0] = class
	binary.LittleEndian.PutUint16(frame[1:3], typ)
	binary.LittleEndian.PutUint32(frame[3:7], uint32(len(frame)))
	binary.LittleEndian.PutUint16(frame[11:13], seq)
	copy(frame[upperHeaderSize16:], body)
	return frame
}

func TestEncodeLegacyFrameLayout(t *testing.T) {
	frame := EncodeLegacy(CmdSetUserPosition, SetUserPositionBody(300, 400, 5, 100), 7)
	if len(frame) != legacyHeaderSize+7 {
		t.Fatalf("frame length = %d", len(frame))
	}
	if frame[0] != legacyCommand {
		t.Fatalf("command byte = %d", frame[0])
	}
	if got := binary.LittleEndian.Uint16(frame[1:3]); got != CmdSetUserPosition {
		t.Fatalf("type = %d", got)
	}
	if got := binary.LittleEndian.Uint32(frame[3:7]); got != uint32(len(frame)) {
		t.Fatalf("length = %d want %d", got, len(frame))
	}
	if got := binary.LittleEndian.Uint16(frame[11:13]); got != 7 {
		t.Fatalf("sequence = %d", got)
	}
	if got := binary.LittleEndian.Uint16(frame[13:15]); got != 300 {
		t.Fatalf("body x = %d", got)
	}
}

func TestReadPacketParsesServer16Envelope(t *testing.T) {
	body := []byte{1, 2, 3}
	frame := buildServerUpper(ClassCommand, ResponseSelect, body, 9)
	packet, err := ReadPacket(bytes.NewReader(frame), upperHeaderSize16, DefaultMaxPacketSize)
	if err != nil {
		t.Fatal(err)
	}
	if packet.Class != ClassCommand || packet.Type != ResponseSelect {
		t.Fatalf("packet = %+v", packet)
	}
	if !bytes.Equal(packet.Body, body) {
		t.Fatalf("body = %X", packet.Body)
	}
}

func TestReadPacketRejectsOversizedLength(t *testing.T) {
	frame := buildServerUpper(ClassCommand, ResponseSelect, []byte{1}, 1)
	binary.LittleEndian.PutUint32(frame[3:7], 1<<20)
	if _, err := ReadPacket(bytes.NewReader(frame), upperHeaderSize16, 64*1024); err == nil {
		t.Fatal("oversized packet was accepted")
	}
}

func TestBodyBuildersMatchServerBoundaries(t *testing.T) {
	if got := len(EndpointRequestBody()); got != EndpointRequestSize {
		t.Fatalf("endpoint body = %d", got)
	}
	if got := RosterRequestBody(); !bytes.Equal(got, []byte{0, 0, 2}) {
		t.Fatalf("roster body = %v", got)
	}
	if got := SelectCharacterBody(3); len(got) != 16 || binary.LittleEndian.Uint32(got) != 3 {
		t.Fatalf("select body = %X", got)
	}
	create, err := CreateCharacterBody(2, []byte{0xB5, 0xC4})
	if err != nil {
		t.Fatal(err)
	}
	if len(create) != 5+2+createCharacterOptionBytes || create[0] != 2 {
		t.Fatalf("create body = %X", create)
	}
	deleteBody, err := DeleteCharacterBody(4, []byte{0xB5, 0xC4})
	if err != nil {
		t.Fatal(err)
	}
	if len(deleteBody) != 6+2 || binary.LittleEndian.Uint16(deleteBody) != 4 {
		t.Fatalf("delete body = %X", deleteBody)
	}
	check, err := CheckCharacterNameBody([]byte{0xB5, 0xC4})
	if err != nil {
		t.Fatal(err)
	}
	if len(check) != 4+2 || binary.LittleEndian.Uint32(check) != 2 {
		t.Fatalf("check name body = %X", check)
	}
	tutorial := TutorialProgressBody(InitialTownProgress)
	if len(tutorial) != 6 || binary.LittleEndian.Uint32(tutorial[1:5]) != 36 || tutorial[5] != 1 {
		t.Fatalf("tutorial body = %X", tutorial)
	}
	area := SetUserAreaBody(38, 1, 100, 200, 5)
	if len(area) != 16 || area[0] != 38 || area[1] != 1 || area[6] != 5 {
		t.Fatalf("area body = %X", area)
	}
	if got := len(ExitBody()); got != 1 {
		t.Fatalf("exit body = %d", got)
	}
}

func TestResultParsers(t *testing.T) {
	if !EndpointResultOK([]byte{1, 0}) || EndpointResultOK([]byte{0, 1}) {
		t.Fatal("endpoint result parsing is wrong")
	}
	port, ok := EndpointChannelPort(func() []byte {
		var writer []byte
		writer = append(writer, 1, 1, 4, 1)
		token := make([]byte, 4)
		writer = append(writer, token...)
		ip := []byte("127.0.0.1")
		length := make([]byte, 4)
		binary.LittleEndian.PutUint32(length, uint32(len(ip)))
		writer = append(writer, length...)
		writer = append(writer, ip...)
		channelPort := make([]byte, 4)
		binary.LittleEndian.PutUint32(channelPort, 10011)
		writer = append(writer, channelPort...)
		return writer
	}())
	if !ok || port != 10011 {
		t.Fatalf("endpoint port = %d, %v", port, ok)
	}

	selectOK := make([]byte, 11)
	selectOK[0] = 1
	binary.LittleEndian.PutUint16(selectOK[9:11], 42)
	characterID, err := SelectResultCharacterID(selectOK)
	if err != nil || characterID != 42 {
		t.Fatalf("select result = %d, %v", characterID, err)
	}
	if _, err := SelectResultCharacterID(append([]byte{0}, selectOK[1:]...)); err == nil {
		t.Fatal("failed select was accepted")
	}

	createOK := append([]byte{1}, binaryLE16(77)...)
	createdID, code, err := CreateResultCharacterID(createOK)
	if err != nil || createdID != 77 || code != 0 {
		t.Fatalf("create result = %d/%d, %v", createdID, code, err)
	}
	if _, code, err := CreateResultCharacterID([]byte{0, 0x14}); err != nil || code != 0x14 {
		t.Fatalf("create failure = %d, %v", code, err)
	}

	if ok, _ := DeleteResult([]byte{1, 0, 2, 0}); !ok {
		t.Fatal("delete success was rejected")
	}
	if ok, code := DeleteResult([]byte{0, 0x15}); ok || code != 0x15 {
		t.Fatalf("delete failure = %v/%d", ok, code)
	}
	if ok, _ := CheckNameResult([]byte{1}); !ok {
		t.Fatal("check-name success was rejected")
	}
	if ok, code := CheckNameResult([]byte{0, 0x14}); ok || code != 0x14 {
		t.Fatalf("check-name failure = %v/%d", ok, code)
	}
}
