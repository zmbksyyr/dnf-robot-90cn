package cn90

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Expert-job store kinds on the wire. The server accepts only these two for a
// create request and matches them against the character's expert_job_type.
const (
	ExpertJobStoreDisjoint byte = 0
	ExpertJobStoreEnchant  byte = 3
)

// ExpertJobStoreCreateBody builds the class1/op598 create request:
// u8 kind, u32 name length, name bytes, u32 charge, i16 x, i16 y, u16 link.
// The server validates the body length as exactly 15+len(name) and rejects an
// empty name or an unknown kind.
func ExpertJobStoreCreateBody(kind byte, name []byte, charge uint32, x, y int16, link uint16) ([]byte, error) {
	if kind != ExpertJobStoreDisjoint && kind != ExpertJobStoreEnchant {
		return nil, fmt.Errorf("cn90 expert store kind %d is not supported", kind)
	}
	if len(name) == 0 || len(name) > 255 {
		return nil, fmt.Errorf("cn90 expert store name length %d outside 1..255", len(name))
	}
	body := make([]byte, 15+len(name))
	body[0] = kind
	binary.LittleEndian.PutUint32(body[1:5], uint32(len(name)))
	copy(body[5:], name)
	offset := 5 + len(name)
	binary.LittleEndian.PutUint32(body[offset:offset+4], charge)
	binary.LittleEndian.PutUint16(body[offset+4:offset+6], uint16(x))
	binary.LittleEndian.PutUint16(body[offset+6:offset+8], uint16(y))
	binary.LittleEndian.PutUint16(body[offset+8:offset+10], link)
	return body, nil
}

// ExpertJobStoreAck interprets a create/close response body: {1} on success or
// {0, code} on failure.
func ExpertJobStoreAck(opcode uint16, body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("cn90 expert store op %d returned an empty body", opcode)
	}
	if body[0] == 1 {
		return nil
	}
	if len(body) >= 2 {
		return fmt.Errorf("cn90 expert store op %d rejected with code %d", opcode, body[1])
	}
	return errors.New("cn90 expert store request was rejected")
}
