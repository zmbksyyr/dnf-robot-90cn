package cn90

import (
	"encoding/binary"
	"fmt"
	"io"
)

// The DNF90 game port speaks two client-visible framings:
//
//   - A legacy game frame, which the current client still emits for many
//     commands: u8 cmd (always 1 for commands), u16 type, u32 total length,
//     u32 checksum (not validated by the server for client frames), u16
//     sequence, then the body.
//   - Server upper packets: u8 classification (0 = notice class, 1 = command
//     class), u16 msgID, u32 total length, u32 checksum, u16 sequence, and,
//     under the locked server16 profile, three reserved bytes before the body.
//
// The server's stream splitter reclassifies several legacy frames as upper
// requests by looking at cmd/type/body length alone, so the robot keeps one
// outbound encoder and one inbound parser.
const (
	legacyHeaderSize = 13
	legacyCommand    = 1

	// upperHeaderSize16 is the reserved-extended server header. The shipped
	// instance profile locks protocol.gameUpperHeader=server16, so every
	// response that concerns the robot carries this header.
	upperHeaderSize16 = 16
	upperHeaderSize13 = 13

	ClassNotice  byte = 0
	ClassCommand byte = 1

	DefaultMaxPacketSize = 1024 * 1024
)

// Packet is one inbound server packet.
type Packet struct {
	Class byte
	Type  uint16
	Body  []byte
}

// EncodeLegacy builds one client command frame with the given body.
func EncodeLegacy(typ uint16, body []byte, sequence uint16) []byte {
	frame := make([]byte, legacyHeaderSize+len(body))
	frame[0] = legacyCommand
	binary.LittleEndian.PutUint16(frame[1:3], typ)
	binary.LittleEndian.PutUint32(frame[3:7], uint32(len(frame)))
	binary.LittleEndian.PutUint16(frame[11:13], sequence)
	copy(frame[legacyHeaderSize:], body)
	return frame
}

// ReadPacket reads one server upper packet. headerSize selects the 16-byte
// server16 profile (the locked 90CN profile) or the historical 13-byte header.
func ReadPacket(reader io.Reader, headerSize, maxSize int) (Packet, error) {
	if reader == nil {
		return Packet{}, fmt.Errorf("cn90 reader is nil")
	}
	if headerSize != upperHeaderSize16 && headerSize != upperHeaderSize13 {
		return Packet{}, fmt.Errorf("cn90 upper header size %d is not supported", headerSize)
	}
	if maxSize <= 0 {
		maxSize = DefaultMaxPacketSize
	}
	if maxSize < headerSize {
		return Packet{}, fmt.Errorf("cn90 maximum packet size %d is below the header size", maxSize)
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Packet{}, err
	}
	length := int(binary.LittleEndian.Uint32(header[3:7]))
	if length < headerSize || length > maxSize {
		return Packet{}, fmt.Errorf("cn90 packet length %d outside %d..%d", length, headerSize, maxSize)
	}
	body := make([]byte, length-headerSize)
	if len(body) > 0 {
		if _, err := io.ReadFull(reader, body); err != nil {
			return Packet{}, err
		}
	}
	return Packet{
		Class: header[0],
		Type:  binary.LittleEndian.Uint16(header[1:3]),
		Body:  body,
	}, nil
}
