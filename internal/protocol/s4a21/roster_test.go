package s4a21

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestDecodeCharacterRoster(t *testing.T) {
	var body bytes.Buffer
	body.Write([]byte{2})
	for _, value := range []uint16{12, 34} {
		_ = binary.Write(&body, binary.LittleEndian, value)
	}
	body.WriteByte(0)
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	_ = binary.Write(&body, binary.LittleEndian, uint16(0))
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	_ = binary.Write(&body, binary.LittleEndian, uint16(1))
	_ = binary.Write(&body, binary.LittleEndian, uint16(3))
	_ = binary.Write(&body, binary.LittleEndian, uint32(5))
	body.WriteString("abcde")
	body.Write([]byte{0, 0, 7, 2, 85, 0x0b, 1})
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))
	body.WriteByte(0)
	body.Write(make([]byte, 36))

	entries, err := DecodeCharacterRoster(body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Slot != 3 || entries[0].Name != "abcde" || entries[0].Job != 7 || entries[0].Grow != 2 || entries[0].Level != 85 {
		t.Fatalf("entries = %+v", entries)
	}
}
