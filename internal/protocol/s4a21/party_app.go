package s4a21

// A21 party application frames are the TQOS type-2 bodies that carry realtime
// dungeon state between party peers. The layout below is verified against a
// live retail-client capture (stage 6, 2026-09-28, dungeon 144):
//
//	body[0]  = 0x01 application tag
//	body[1]  = subtype
//	body[2]  = 0x00
//	body[3:7]= CRC32(partyUDPCRCTable) over the plaintext body[7:]
//	body[7:] = plaintext obfuscated byte-wise with the peer codec
//
// Subtypes observed from the client:
//
//	0x07 dungeon position: u16 0x0211, u32 counter, then six u32 fields
//	     [tick, state, baseX, baseY, x, y]; state 0 with -1/-1 is a reset
//	     broadcast and carries no position.
//	0x38 town position: five u32 fields [1, 2, area, x, y].
//
// The remaining subtypes (0x04, 0x0B, ...) are action and status blobs that
// following does not need, so they are left undecoded.

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
)

const (
	partyAppTag                = 0x01
	partyAppSubDungeonPosition = 0x07
	partyAppSubTownPosition    = 0x38
	partyAppPositionPrefix     = 0x0211
	partyAppPositionFields     = 6
)

// PartyAppPosition is one decoded position broadcast carried by a party
// application frame. Slot is the TQOS sender slot: the leader occupies slot 0.
type PartyAppPosition struct {
	Sub     byte
	Slot    byte
	Counter uint32
	State   uint32
	// Flag/Town/Area belong to the 0x38 position shape: the retail client
	// broadcasts its return-anchor town and area next to the coordinates.
	Flag  uint32
	Town  uint32
	Area  uint32
	BaseX int32
	BaseY int32
	X     int32
	Y     int32
}

// Valid reports whether the frame carries an actionable position.
func (p PartyAppPosition) Valid() bool {
	if p.Sub != partyAppSubDungeonPosition {
		return true
	}
	if p.State == 0 || p.X == -1 || p.Y == -1 {
		return false
	}
	return p.X >= 0 && p.Y >= 0
}

// partyUDPObfuscateByte applies the per-peer codec to one plaintext byte.
func partyUDPObfuscateByte(value byte, codec partyUDPCodec) byte {
	return bits.RotateLeft8(value^codec.key, int(codec.rotate))
}

// partyUDPDeobfuscateByte reverses partyUDPObfuscateByte.
func partyUDPDeobfuscateByte(value byte, codec partyUDPCodec) byte {
	return bits.RotateLeft8(value, -int(codec.rotate)) ^ codec.key
}

// partyUDPObfuscate applies the per-peer codec to plaintext application bytes.
func partyUDPObfuscate(plain []byte, codec partyUDPCodec) []byte {
	out := make([]byte, len(plain))
	for i, value := range plain {
		out[i] = partyUDPObfuscateByte(value, codec)
	}
	return out
}

// partyUDPDeobfuscate reverses partyUDPObfuscate.
func partyUDPDeobfuscate(wire []byte, codec partyUDPCodec) []byte {
	out := make([]byte, len(wire))
	for i, value := range wire {
		out[i] = partyUDPDeobfuscateByte(value, codec)
	}
	return out
}

// parsePartyAppPosition decodes the verified position subtypes. The checksum
// must match so unrelated or corrupted frames are rejected.
func parsePartyAppPosition(body []byte, codec partyUDPCodec) (PartyAppPosition, bool) {
	if len(body) < 13 || body[0] != partyAppTag || body[2] != 0 {
		return PartyAppPosition{}, false
	}
	plain := partyUDPDeobfuscate(body[7:], codec)
	stored := binary.LittleEndian.Uint32(body[3:7])
	if crc32.Checksum(plain, partyUDPCRCTable) != stored {
		return PartyAppPosition{}, false
	}
	position := PartyAppPosition{Sub: body[1]}
	switch body[1] {
	case partyAppSubDungeonPosition:
		if len(plain) < 2+4+partyAppPositionFields*4 {
			return PartyAppPosition{}, false
		}
		if binary.LittleEndian.Uint16(plain[:2]) != partyAppPositionPrefix {
			return PartyAppPosition{}, false
		}
		position.Counter = binary.LittleEndian.Uint32(plain[2:6])
		fields := plain[6:]
		position.State = binary.LittleEndian.Uint32(fields[4:8])
		position.BaseX = int32(binary.LittleEndian.Uint32(fields[8:12]))
		position.BaseY = int32(binary.LittleEndian.Uint32(fields[12:16]))
		position.X = int32(binary.LittleEndian.Uint32(fields[16:20]))
		position.Y = int32(binary.LittleEndian.Uint32(fields[20:24]))
		return position, true
	case partyAppSubTownPosition:
		if len(plain) < 5*4 {
			return PartyAppPosition{}, false
		}
		position.Flag = binary.LittleEndian.Uint32(plain[0:4])
		position.Town = binary.LittleEndian.Uint32(plain[4:8])
		position.Area = binary.LittleEndian.Uint32(plain[8:12])
		position.X = int32(binary.LittleEndian.Uint32(plain[12:16]))
		position.Y = int32(binary.LittleEndian.Uint32(plain[16:20]))
		return position, true
	default:
		return PartyAppPosition{}, false
	}
}

// buildPartyAppPositionBody builds a dungeon position application body with the
// verified shape so the retail client renders the robot's in-dungeon movement.
// The retail member keeps base at the room anchor and tick as a room-relative
// millisecond clock; both fields are what the client interpolates on.
func buildPartyAppPositionBody(counter, tick uint32, baseX, baseY, x, y int32, codec partyUDPCodec) []byte {
	plain := make([]byte, 6+partyAppPositionFields*4)
	binary.LittleEndian.PutUint16(plain[0:2], partyAppPositionPrefix)
	binary.LittleEndian.PutUint32(plain[2:6], counter)
	fields := plain[6:]
	binary.LittleEndian.PutUint32(fields[0:4], tick)
	binary.LittleEndian.PutUint32(fields[4:8], partyAppPositionActiveState)
	binary.LittleEndian.PutUint32(fields[8:12], uint32(baseX))
	binary.LittleEndian.PutUint32(fields[12:16], uint32(baseY))
	binary.LittleEndian.PutUint32(fields[16:20], uint32(x))
	binary.LittleEndian.PutUint32(fields[20:24], uint32(y))

	body := make([]byte, 7+len(plain))
	body[0] = partyAppTag
	body[1] = partyAppSubDungeonPosition
	body[2] = 0
	binary.LittleEndian.PutUint32(body[3:7], crc32.Checksum(plain, partyUDPCRCTable))
	copy(body[7:], partyUDPObfuscate(plain, codec))
	return body
}

// buildPartyAppTownPositionBody builds the 0x38 position body the retail client
// broadcasts in town and inside dungeons: [flag, town, area, x, y]. Dungeon
// entries keep the pre-dungeon town/area as the return anchor.
func buildPartyAppTownPositionBody(flag, town, area uint32, x, y int32, codec partyUDPCodec) []byte {
	plain := make([]byte, 5*4)
	binary.LittleEndian.PutUint32(plain[0:4], flag)
	binary.LittleEndian.PutUint32(plain[4:8], town)
	binary.LittleEndian.PutUint32(plain[8:12], area)
	binary.LittleEndian.PutUint32(plain[12:16], uint32(x))
	binary.LittleEndian.PutUint32(plain[16:20], uint32(y))

	body := make([]byte, 7+len(plain))
	body[0] = partyAppTag
	body[1] = partyAppSubTownPosition
	body[2] = 0
	binary.LittleEndian.PutUint32(body[3:7], crc32.Checksum(plain, partyUDPCRCTable))
	copy(body[7:], partyUDPObfuscate(plain, codec))
	return body
}

// partyAppPositionActiveState matches the state value the retail client uses
// for an actionable dungeon position.
const partyAppPositionActiveState = 26

// partyUDPApplicationFrames returns the application frames of a datagram with
// their TQOS sender byte.
func partyUDPApplicationFrames(payload []byte) []partyUDPApplicationFrame {
	frames, ok := splitPartyUDPFrames(payload)
	if !ok {
		return nil
	}
	var result []partyUDPApplicationFrame
	for _, frame := range frames {
		application := partyUDPApplicationPayload(frame)
		if application == nil {
			continue
		}
		sender := byte(0)
		if len(frame) >= 8 {
			sender = frame[7]
		}
		result = append(result, partyUDPApplicationFrame{Sender: sender, Body: application})
	}
	return result
}

// partyUDPApplicationFrame is one application body plus its TQOS sender slot.
type partyUDPApplicationFrame struct {
	Sender byte
	Body   []byte
}
