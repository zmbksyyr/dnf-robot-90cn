package s4a21

import (
	"encoding/binary"
	"testing"
)

func TestCreateCharacterBodyMatchesA21Layout(t *testing.T) {
	body, err := CreateCharacterBody(7, []byte("robot01"))
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(body[1:5]); got != 7 {
		t.Fatalf("name length = %d", got)
	}
	if string(body[5:]) != "robot01\x00" {
		t.Fatalf("body tail = %q", body[5:])
	}
}

func TestEncodeDecodeFrame(t *testing.T) {
	frame := Encode(1, CmdCreateCharacter, []byte{7, 8, 9})
	pkt, err := DecodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Type != CmdCreateCharacter || string(pkt.Body) != string([]byte{7, 8, 9}) {
		t.Fatalf("packet = %+v", pkt)
	}
}

func TestSelectCharacterBodyUsesTwoByteSlot(t *testing.T) {
	body := SelectCharacterBody(0x0102)
	if len(body) != 2 || binary.LittleEndian.Uint16(body) != 0x0102 {
		t.Fatalf("body = %v", body)
	}
}

func TestMoveMapBodyIsFixedWidth(t *testing.T) {
	body := MoveMapBody(MoveMapRequest{NextX: 2, NextY: 3, PathPositionX: 100, PathPositionY: 200})
	if len(body) != 64 || body[0] != 2 || body[1] != 3 {
		t.Fatalf("body length/content = %d/%v", len(body), body[:2])
	}
	if binary.LittleEndian.Uint32(body[2:6]) != 100 || binary.LittleEndian.Uint32(body[6:10]) != 200 {
		t.Fatalf("path positions = %v", body[2:10])
	}
}

func TestLoginAndMessageBodiesUseLittleEndianDStrings(t *testing.T) {
	login, err := LoginBody("robot", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(login[:4]) != 5 || binary.LittleEndian.Uint32(login[9:13]) != 4 {
		t.Fatalf("login body = %v", login)
	}
	message, err := SendMessageBody(3, 0, 0, []byte("hello"))
	if err != nil || binary.LittleEndian.Uint32(message[7:11]) != 5 {
		t.Fatalf("message body = %v err=%v", message, err)
	}
}
