package cn90

import (
	"testing"

	"robot/internal/foundation/charset"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

func TestExpertJobStoreCreateBody(t *testing.T) {
	name, err := charset.EncodeGBKString("分类机")
	if err != nil {
		t.Fatal(err)
	}
	body, err := protocol.ExpertJobStoreCreateBody(protocol.ExpertJobStoreDisjoint, name, 500, 450, 234, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 15+len(name) {
		t.Fatalf("body length = %d", len(body))
	}
	if body[0] != protocol.ExpertJobStoreDisjoint {
		t.Fatalf("kind = %d", body[0])
	}
	if got := int(body[1]) | int(body[2])<<8 | int(body[3])<<16 | int(body[4])<<24; got != len(name) {
		t.Fatalf("name length = %d", got)
	}
	if string(body[5:5+len(name)]) != string(name) {
		t.Fatalf("name bytes = % X", body[5:5+len(name)])
	}
	offset := 5 + len(name)
	if got := int(body[offset]) | int(body[offset+1])<<8; got != 500 {
		t.Fatalf("charge = %d", got)
	}
	if got := int(body[offset+4]) | int(body[offset+5])<<8; got != 450 {
		t.Fatalf("x = %d", got)
	}
	if got := int(body[offset+6]) | int(body[offset+7])<<8; got != 234 {
		t.Fatalf("y = %d", got)
	}
	if _, err := protocol.ExpertJobStoreCreateBody(9, []byte("x"), 0, 0, 0, 0); err == nil {
		t.Fatal("unknown kind was accepted")
	}
	if _, err := protocol.ExpertJobStoreCreateBody(protocol.ExpertJobStoreEnchant, nil, 0, 0, 0, 0); err == nil {
		t.Fatal("empty name was accepted")
	}
}

func TestExpertJobStoreAck(t *testing.T) {
	if err := protocol.ExpertJobStoreAck(598, []byte{1}); err != nil {
		t.Fatalf("success ack rejected: %v", err)
	}
	err := protocol.ExpertJobStoreAck(598, []byte{0, 19})
	if err == nil {
		t.Fatal("failure ack was accepted")
	}
	if got := err.Error(); got != "cn90 expert store op 598 rejected with code 19" {
		t.Fatalf("failure text = %q", got)
	}
}

func TestExpertJobWireKindMapping(t *testing.T) {
	kind, err := expertJobWireKind(shared.ExpertJobStoreDisjoint)
	if err != nil || kind != protocol.ExpertJobStoreDisjoint {
		t.Fatalf("disjoint kind = %d err=%v", kind, err)
	}
	kind, err = expertJobWireKind(shared.ExpertJobStoreEnchant)
	if err != nil || kind != protocol.ExpertJobStoreEnchant {
		t.Fatalf("enchant kind = %d err=%v", kind, err)
	}
	if _, err := expertJobWireKind(shared.ExpertJobStoreNone); err == nil {
		t.Fatal("none kind was accepted")
	}
}

func TestExpertJobStoreNameEncoding(t *testing.T) {
	name, err := expertJobStoreName(shared.ExpertJobStoreDisjoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(name) != 6 {
		t.Fatalf("disjoint name bytes = % X", name)
	}
	name, err = expertJobStoreName(shared.ExpertJobStoreEnchant)
	if err != nil {
		t.Fatal(err)
	}
	if len(name) != 6 {
		t.Fatalf("enchant name bytes = % X", name)
	}
	if _, err := expertJobStoreName(shared.ExpertJobStoreNone); err == nil {
		t.Fatal("none kind produced a name")
	}
}
