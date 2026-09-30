package cn90

import (
	"encoding/binary"
	"fmt"

	"robot/internal/foundation/charset"
)

// CharacterRosterEntry is the identity exposed by 90CN's character-list
// notification. 90CN has no persistent UID/CID pair in this packet.
type CharacterRosterEntry struct {
	Slot    uint16
	Name    string
	NameRaw []byte
	Job     byte
	Grow    byte
	Level   byte
}

// DecodeCharacterRoster parses the 90CN type=2 character-list body. The tail
// after each appearance list is fixed by the 90CN builder and is intentionally
// skipped; appearance data is not robot identity.
func DecodeCharacterRoster(body []byte) ([]CharacterRosterEntry, error) {
	r := rosterReader{body: body}
	if r.skip(1+2+2+1+4+2+4) != nil {
		return nil, fmt.Errorf("character roster header: %w", r.err)
	}
	count := r.u16()
	if r.err != nil {
		return nil, fmt.Errorf("character roster count: %w", r.err)
	}
	entries := make([]CharacterRosterEntry, 0, count)
	for i := 0; i < int(count); i++ {
		entry := CharacterRosterEntry{Slot: r.u16()}
		name := r.dstr()
		if r.skip(2) != nil { // gender and reserved byte
			return nil, fmt.Errorf("character roster entry %d prefix: %w", i, r.err)
		}
		entry.Job = r.byte()
		entry.Grow = r.byte()
		entry.Level = r.byte()
		if r.skip(2+4+4) != nil { // reserved flags and two zero IDs
			return nil, fmt.Errorf("character roster entry %d: %w", i, r.err)
		}
		entry.NameRaw = append([]byte(nil), name...)
		entry.Name = charset.DecodeWireName(name)
		appearanceCount := int(r.byte())
		if r.err != nil {
			return nil, fmt.Errorf("character roster entry %d appearance count: %w", i, r.err)
		}
		if r.skip(appearanceCount*23) != nil {
			return nil, fmt.Errorf("character roster entry %d appearances: %w", i, r.err)
		}
		if r.skip(36) != nil {
			return nil, fmt.Errorf("character roster entry %d tail: %w", i, r.err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

type rosterReader struct {
	body   []byte
	offset int
	err    error
}

func (r *rosterReader) skip(n int) error {
	if r.err != nil {
		return r.err
	}
	if n < 0 || r.offset+n > len(r.body) {
		r.err = fmt.Errorf("short body at offset %d", r.offset)
		return r.err
	}
	r.offset += n
	return nil
}
func (r *rosterReader) byte() byte {
	if r.skip(1) != nil {
		return 0
	}
	return r.body[r.offset-1]
}
func (r *rosterReader) u16() uint16 {
	if r.skip(2) != nil {
		return 0
	}
	return binary.LittleEndian.Uint16(r.body[r.offset-2 : r.offset])
}
func (r *rosterReader) dstr() []byte {
	length := r.u32()
	if r.err != nil {
		return nil
	}
	if length > uint32(len(r.body)-r.offset) {
		r.err = fmt.Errorf("invalid string length %d", length)
		return nil
	}
	start := r.offset
	r.offset += int(length)
	return r.body[start:r.offset]
}
func (r *rosterReader) u32() uint32 {
	if r.skip(4) != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(r.body[r.offset-4 : r.offset])
}
