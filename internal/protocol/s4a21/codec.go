package s4a21

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	RequestHeaderSize  = 14
	ResponseHeaderSize = 15
)

const (
	CmdLogin                uint16 = 0x0001
	CmdSelectCharacter      uint16 = 0x0004
	CmdCreateCharacter      uint16 = 0x0005
	CmdSendMessage          uint16 = 0x0011
	CmdAcceptQuest          uint16 = 0x001F
	CmdSetQuestTrigger      uint16 = 0x0021
	CmdFinishQuest          uint16 = 0x0022
	CmdEnterSelectDungeon   uint16 = 0x000F
	CmdSelectDungeon        uint16 = 0x0010
	CmdChangeTutorialFlag   uint16 = 0x008F
	CmdFinishLoading        uint16 = 0x0025
	CmdSetUserPosition      uint16 = 0x0023
	CmdMoveMap              uint16 = 0x002D
	CmdCheckConnection      uint16 = 0x04DD
	NotiCharacterList       uint16 = 0x0002
	NotiAcceptableQuestList uint16 = 0x0015
	NotiUserPosition        uint16 = 0x0016
	NotiEnterSelectDungeon  uint16 = 0x001B
	NotiDungeonInfo         uint16 = 0x001C
	NotiStartMap            uint16 = 0x001D
	NotiFinishLoading       uint16 = 0x001E
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

// AcceptableQuestList is the verified A21 selection projection: character
// level, followed by a uint16 count and that many uint16 quest IDs.
type AcceptableQuestList struct {
	Level    byte
	QuestIDs []uint16
}

func ParseAcceptableQuestList(body []byte) (AcceptableQuestList, error) {
	if len(body) < 3 {
		return AcceptableQuestList{}, fmt.Errorf("s4a21 acceptable quest list is truncated")
	}
	count := int(binary.LittleEndian.Uint16(body[1:3]))
	want := 3 + count*2
	if len(body) != want {
		return AcceptableQuestList{}, fmt.Errorf("s4a21 acceptable quest list length=%d want=%d", len(body), want)
	}
	ids := make([]uint16, count)
	for i := range ids {
		ids[i] = binary.LittleEndian.Uint16(body[3+i*2:])
	}
	return AcceptableQuestList{Level: body[0], QuestIDs: ids}, nil
}

// AcceptQuestBody is the A21 wire form: a two-byte echo prefix followed by
// the little-endian quest ID consumed by the server quest parser.
func AcceptQuestBody(questID uint16) []byte {
	body := make([]byte, 4)
	binary.LittleEndian.PutUint16(body[2:], questID)
	return body
}

// SetQuestTriggerBody is the verified A21 wire form. The first two bytes are
// the client echo/reserved prefix removed by the server before parsing; the
// remaining fields are quest id, trigger type and an increment flag.
func SetQuestTriggerBody(questID uint16, triggerType byte, increment bool) []byte {
	body := make([]byte, 6)
	binary.LittleEndian.PutUint16(body[2:4], questID)
	body[4] = triggerType
	if increment {
		body[5] = 1
	}
	return body
}

// FinishQuestBody is the A21 wire form. A reward selection of -1 means the
// client did not choose a reward branch and is encoded as 0xFFFF.
func FinishQuestBody(questID uint16, rewardSelection int16, completionCount uint16) []byte {
	body := make([]byte, 10)
	binary.LittleEndian.PutUint16(body[2:4], questID)
	if rewardSelection < 0 {
		binary.LittleEndian.PutUint16(body[4:6], 0xFFFF)
	} else {
		binary.LittleEndian.PutUint16(body[4:6], uint16(rewardSelection))
	}
	binary.LittleEndian.PutUint16(body[6:8], completionCount)
	binary.LittleEndian.PutUint16(body[8:10], 0xFFFF)
	return body
}

func Encode(command byte, typ uint16, body []byte) []byte {
	length := RequestHeaderSize + len(body)
	out := make([]byte, length)
	out[0] = command
	binary.LittleEndian.PutUint16(out[1:3], typ)
	binary.LittleEndian.PutUint32(out[3:7], uint32(length))
	copy(out[RequestHeaderSize:], body)
	return out
}

func EncodeResponse(command byte, typ uint16, body []byte) []byte {
	length := ResponseHeaderSize + len(body)
	out := make([]byte, length)
	out[0] = command
	binary.LittleEndian.PutUint16(out[1:3], typ)
	binary.LittleEndian.PutUint32(out[3:7], uint32(length))
	copy(out[ResponseHeaderSize:], body)
	return out
}

func DecodeFrame(frame []byte) (Packet, error) {
	if len(frame) < ResponseHeaderSize {
		return Packet{}, fmt.Errorf("s4a21 packet shorter than header: %d", len(frame))
	}
	length := int(binary.LittleEndian.Uint32(frame[3:7]))
	if length < ResponseHeaderSize || length != len(frame) {
		return Packet{}, fmt.Errorf("s4a21 packet length=%d frame=%d", length, len(frame))
	}
	body := append([]byte(nil), frame[ResponseHeaderSize:]...)
	return Packet{
		Command: frame[0], Type: binary.LittleEndian.Uint16(frame[1:3]),
		Length: uint32(length), Checksum: binary.LittleEndian.Uint32(frame[7:11]),
		Sequence: binary.LittleEndian.Uint16(frame[11:13]), Extra: frame[14], Body: body,
	}, nil
}

func ReadFrame(reader io.Reader, maxLength int) (Packet, error) {
	return readFrame(reader, ResponseHeaderSize, maxLength)
}

func ReadRequestFrame(reader io.Reader, maxLength int) (Packet, error) {
	return readFrame(reader, RequestHeaderSize, maxLength)
}

func readFrame(reader io.Reader, headerSize, maxLength int) (Packet, error) {
	if maxLength < headerSize {
		return Packet{}, fmt.Errorf("s4a21 maximum packet length is too small: %d", maxLength)
	}
	header := make([]byte, headerSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return Packet{}, err
	}
	length := int(binary.LittleEndian.Uint32(header[3:7]))
	if length < headerSize || length > maxLength {
		return Packet{}, fmt.Errorf("s4a21 packet length=%d outside %d..%d", length, headerSize, maxLength)
	}
	frame := make([]byte, length)
	copy(frame, header)
	if _, err := io.ReadFull(reader, frame[headerSize:]); err != nil {
		return Packet{}, err
	}
	if headerSize == ResponseHeaderSize {
		return DecodeFrame(frame)
	}
	body := append([]byte(nil), frame[RequestHeaderSize:]...)
	return Packet{
		Command: frame[0], Type: binary.LittleEndian.Uint16(frame[1:3]),
		Length: uint32(length), Checksum: binary.LittleEndian.Uint32(frame[7:11]),
		Sequence: binary.LittleEndian.Uint16(frame[11:13]), Extra: frame[13], Body: body,
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

// EnterSelectDungeonBody is the verified A21 four-byte dungeon selection
// request. Additional trailing bytes are not needed for the observed path.
func EnterSelectDungeonBody(dungeonID uint32) []byte {
	body := make([]byte, 4)
	binary.LittleEndian.PutUint32(body, dungeonID)
	return body
}

// SelectDungeonBody is the verified ordinary A21 15-byte request shape:
// dungeon id, difficulty/flags, 0xFFFF sentinel and six reserved bytes.
func SelectDungeonBody(dungeonID uint32, difficulty, flag1, flag2 byte) []byte {
	body := make([]byte, 15)
	binary.LittleEndian.PutUint32(body[0:4], dungeonID)
	body[4] = difficulty
	body[5] = flag1
	body[6] = flag2
	binary.LittleEndian.PutUint16(body[7:9], 0xFFFF)
	return body
}

// ChangeTutorialFlagBody is the compact six-byte A21 form observed on the
// live server: mode=0, uint32 flag index, reward flag.
func ChangeTutorialFlagBody(flagIndex uint32, rewardFlag byte) []byte {
	body := make([]byte, 6)
	binary.LittleEndian.PutUint32(body[1:5], flagIndex)
	body[5] = rewardFlag
	return body
}

func FinishLoadingBody() []byte { return nil }

type MoveMapRequest struct {
	NextX, NextY           byte
	PathPositionX          uint32
	PathPositionY          uint32
	MoveMode               byte
	TrapBits               uint16
	MemberMapClearValues   [8]uint16
	MemberMapElapsedValues [8]uint32
	ClientTimingToken      uint16
	ClientStateFlag        byte
}

func MoveMapBody(request MoveMapRequest) []byte {
	out := make([]byte, 64)
	offset := 0
	out[offset] = request.NextX
	offset++
	out[offset] = request.NextY
	offset++
	binary.LittleEndian.PutUint32(out[offset:offset+4], request.PathPositionX)
	offset += 4
	binary.LittleEndian.PutUint32(out[offset:offset+4], request.PathPositionY)
	offset += 4
	out[offset] = request.MoveMode
	offset++
	binary.LittleEndian.PutUint16(out[offset:offset+2], request.TrapBits)
	offset += 2
	for _, value := range request.MemberMapClearValues {
		binary.LittleEndian.PutUint16(out[offset:offset+2], value)
		offset += 2
	}
	for _, value := range request.MemberMapElapsedValues {
		binary.LittleEndian.PutUint32(out[offset:offset+4], value)
		offset += 4
	}
	binary.LittleEndian.PutUint16(out[offset:offset+2], request.ClientTimingToken)
	offset += 2
	out[offset] = request.ClientStateFlag
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
