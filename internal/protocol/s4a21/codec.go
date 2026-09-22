package s4a21

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const HeaderSize = 15

const (
	CmdLogin           uint16 = 0x0001
	CmdSelectCharacter uint16 = 0x0004
	CmdCreateCharacter uint16 = 0x0005
	CmdSendMessage     uint16 = 0x0011
	CmdSetUserPosition uint16 = 0x0023
	CmdMoveMap         uint16 = 0x002D
	NotiCharacterList  uint16 = 0x0002
	NotiUserPosition   uint16 = 0x0016
)

type Packet struct {
	Command  byte
	Type     uint16
	Length   uint32
	Checksum uint32
	Sequence uint16
	Extra    byte
	Body     []byte
}

func Encode(command byte, typ uint16, body []byte) []byte {
	length := HeaderSize + len(body)
	out := make([]byte, length)
	out[0] = command
	binary.LittleEndian.PutUint16(out[1:3], typ)
	binary.LittleEndian.PutUint32(out[3:7], uint32(length))
	copy(out[15:], body)
	return out
}

func DecodeFrame(frame []byte) (Packet, error) {
	if len(frame) < HeaderSize {
		return Packet{}, fmt.Errorf("s4a21 packet shorter than header: %d", len(frame))
	}
	length := int(binary.LittleEndian.Uint32(frame[3:7]))
	if length < HeaderSize || length != len(frame) {
		return Packet{}, fmt.Errorf("s4a21 packet length=%d frame=%d", length, len(frame))
	}
	body := append([]byte(nil), frame[HeaderSize:]...)
	return Packet{
		Command: frame[0], Type: binary.LittleEndian.Uint16(frame[1:3]),
		Length: uint32(length), Checksum: binary.LittleEndian.Uint32(frame[7:11]),
		Sequence: binary.LittleEndian.Uint16(frame[11:13]), Extra: frame[14], Body: body,
	}, nil
}

func LoginBody(mID, passwordHash string) ([]byte, error) {
	var out bytes.Buffer
	if err := writeDString(&out, []byte(mID)); err != nil {
		return nil, err
	}
	if err := writeDString(&out, []byte(passwordHash)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func CreateCharacterBody(job byte, name []byte) ([]byte, error) {
	if len(name) < 2 || len(name) > 18 {
		return nil, fmt.Errorf("character name must be 2..18 bytes")
	}
	out := make([]byte, 6+len(name))
	out[0] = job
	binary.LittleEndian.PutUint32(out[1:5], uint32(len(name)))
	copy(out[5:], name)
	return out, nil
}

func SelectCharacterBody(slot uint16) []byte {
	out := make([]byte, 2)
	binary.LittleEndian.PutUint16(out, slot)
	return out
}

func SetUserPositionBody(x, y int16, direction byte, motion uint16) []byte {
	out := make([]byte, 7)
	binary.LittleEndian.PutUint16(out[0:2], uint16(x))
	binary.LittleEndian.PutUint16(out[2:4], uint16(y))
	out[4] = direction
	binary.LittleEndian.PutUint16(out[5:7], motion)
	return out
}

func SendMessageBody(mode byte, targetUID uint16, targetCharacterID uint32, message []byte) ([]byte, error) {
	if len(message) == 0 || len(message) > 256 {
		return nil, fmt.Errorf("message must be 1..256 bytes")
	}
	out := make([]byte, 11+len(message))
	out[0] = mode
	binary.LittleEndian.PutUint16(out[1:3], targetUID)
	binary.LittleEndian.PutUint32(out[3:7], targetCharacterID)
	binary.LittleEndian.PutUint32(out[7:11], uint32(len(message)))
	copy(out[11:], message)
	return out, nil
}

func writeDString(out *bytes.Buffer, value []byte) error {
	if len(value) > 256 {
		return fmt.Errorf("dstring exceeds 256 bytes")
	}
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = out.Write(length[:])
	_, _ = out.Write(value)
	return nil
}
