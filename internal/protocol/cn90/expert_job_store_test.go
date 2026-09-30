package cn90

import (
	"encoding/binary"
	"testing"
)

func TestCreateExpertJobStoreBodyMatchesCN90Layout(t *testing.T) {
	body, err := CreateExpertJobStoreBody(ExpertJobStoreKindDisjointMachine, []byte("robot-shop"), 500, 1234, 567, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 15+len("robot-shop") {
		t.Fatalf("body length = %d", len(body))
	}
	if body[0] != ExpertJobStoreKindDisjointMachine {
		t.Fatalf("kind = %d", body[0])
	}
	if got := binary.LittleEndian.Uint32(body[1:5]); got != uint32(len("robot-shop")) {
		t.Fatalf("name length = %d", got)
	}
	if string(body[5:15]) != "robot-shop" {
		t.Fatalf("name = %q", body[5:15])
	}
	offset := 15
	if got := int32(binary.LittleEndian.Uint32(body[offset : offset+4])); got != 500 {
		t.Fatalf("cost = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[offset+4 : offset+6])); got != 1234 {
		t.Fatalf("x = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[offset+6 : offset+8])); got != 567 {
		t.Fatalf("y = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[offset+8 : offset+10])); got != 2 {
		t.Fatalf("direction = %d", got)
	}
}

func TestCreateExpertJobStoreBodyAcceptsEmptyName(t *testing.T) {
	body, err := CreateExpertJobStoreBody(ExpertJobStoreKindDisjointMachine, nil, 0, -1, -2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 15 {
		t.Fatalf("body length = %d", len(body))
	}
	offset := 5
	if got := int32(binary.LittleEndian.Uint32(body[offset : offset+4])); got != 0 {
		t.Fatalf("cost = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[offset+4 : offset+6])); got != -1 {
		t.Fatalf("x = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[offset+6 : offset+8])); got != -2 {
		t.Fatalf("y = %d", got)
	}
}

func TestCreateExpertJobStoreBodyRejectsInvalidInput(t *testing.T) {
	if _, err := CreateExpertJobStoreBody(ExpertJobStoreKindDisjointMachine, nil, -1, 0, 0, 0); err == nil {
		t.Fatal("negative cost unexpectedly accepted")
	}
	if _, err := CreateExpertJobStoreBody(ExpertJobStoreKindDisjointMachine, make([]byte, 256), 0, 0, 0, 0); err == nil {
		t.Fatal("oversized name unexpectedly accepted")
	}
}

func TestParseExpertJobStoreAck(t *testing.T) {
	ok, errCode, valid := ParseExpertJobStoreAck([]byte{1})
	if !ok || errCode != 0 || !valid {
		t.Fatalf("success ack = %t/%d/%t", ok, errCode, valid)
	}
	ok, errCode, valid = ParseExpertJobStoreAck([]byte{0, 82})
	if ok || errCode != 82 || !valid {
		t.Fatalf("error ack = %t/%d/%t", ok, errCode, valid)
	}
	if _, _, valid := ParseExpertJobStoreAck(nil); valid {
		t.Fatal("empty ack unexpectedly valid")
	}
	if _, _, valid := ParseExpertJobStoreAck([]byte{2, 0}); valid {
		t.Fatal("unknown ack unexpectedly valid")
	}
}

func TestParseExpertJobStoreOwners(t *testing.T) {
	create := make([]byte, 3)
	create[0] = ExpertJobStoreKindDisjointMachine
	binary.LittleEndian.PutUint16(create[1:3], 0x1234)
	uid, err := ParseExpertJobStoreCreateOwner(create)
	if err != nil || uid != 0x1234 {
		t.Fatalf("create owner = %d/%v", uid, err)
	}
	closeBody := make([]byte, 4)
	binary.LittleEndian.PutUint16(closeBody[0:2], 0x4321)
	uid, err = ParseExpertJobStoreCloseOwner(closeBody)
	if err != nil || uid != 0x4321 {
		t.Fatalf("close owner = %d/%v", uid, err)
	}
	if _, err := ParseExpertJobStoreCreateOwner([]byte{0}); err == nil {
		t.Fatal("truncated create notification unexpectedly accepted")
	}
	if _, err := ParseExpertJobStoreCloseOwner([]byte{0}); err == nil {
		t.Fatal("truncated close notification unexpectedly accepted")
	}
}
