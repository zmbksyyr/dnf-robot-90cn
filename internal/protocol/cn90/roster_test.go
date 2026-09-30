package cn90

import (
	"encoding/binary"
	"testing"
)

// buildRosterEntry mirrors the server's writeNoPackRosterEntry layout.
func buildRosterEntry(slot uint16, name []byte, job, grow, level byte) []byte {
	out := make([]byte, 0, 128)
	out = append(out, byte(slot), byte(slot>>8))
	length := make([]byte, 4)
	binary.LittleEndian.PutUint32(length, uint32(len(name)))
	out = append(out, length...)
	out = append(out, name...)
	out = append(out, 0, 0) // reserved
	out = append(out, job, grow, level, 0, 0)
	out = append(out, make([]byte, 8)...) // two zero ids
	out = append(out, 0)                  // empty appearance summary
	out = append(out, make([]byte, 24)...)
	out = append(out, 3, 0, 0, 4)
	out = append(out, make([]byte, 6)...)
	out = append(out, 0)    // private pvp
	out = append(out, 0, 0) // reserved
	out = append(out, 1)    // ordinary pvp entry
	return out
}

func buildRosterBody(entries ...[]byte) []byte {
	prefix := make([]byte, 15)
	prefix[0] = 2
	prefix[1] = byte(len(entries))
	prefix[2] = 5
	binary.LittleEndian.PutUint16(prefix[3:5], 32)
	binary.LittleEndian.PutUint16(prefix[5:7], 32)
	binary.LittleEndian.PutUint16(prefix[7:9], uint16(len(entries)))
	binary.LittleEndian.PutUint16(prefix[13:15], uint16(len(entries)))
	body := append([]byte(nil), prefix...)
	for _, entry := range entries {
		body = append(body, entry...)
	}
	body = append(body, 1, 0)               // page count, flag
	body = append(body, make([]byte, 8)...) // trailer values
	return body
}

func TestDecodeCharacterRoster(t *testing.T) {
	first := buildRosterEntry(0, []byte{0xB5, 0xC4}, 1, 0, 70)
	second := buildRosterEntry(3, []byte("AB"), 2, 1, 85)
	body := buildRosterBody(first, second)

	entries, err := DecodeCharacterRoster(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].Slot != 0 || entries[0].Job != 1 || entries[0].Level != 70 || len(entries[0].NameRaw) != 2 {
		t.Fatalf("first entry = %+v", entries[0])
	}
	if entries[1].Slot != 3 || entries[1].Job != 2 || entries[1].Grow != 1 || entries[1].Level != 85 {
		t.Fatalf("second entry = %+v", entries[1])
	}
	if entries[1].Name != "AB" {
		t.Fatalf("second name = %q", entries[1].Name)
	}
}

func TestDecodeCharacterRosterRejectsTruncation(t *testing.T) {
	body := buildRosterBody(buildRosterEntry(0, []byte("AB"), 1, 0, 10))
	if _, err := DecodeCharacterRoster(body[:len(body)-10]); err == nil {
		t.Fatal("truncated roster was accepted")
	}
}
