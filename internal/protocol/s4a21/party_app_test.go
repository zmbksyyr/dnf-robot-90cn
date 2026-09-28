package s4a21

import (
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"testing"
)

// Captured stage-6 samples from the retail A21 client (dungeon 144, codec
// key 0x59 rotate 0). They pin the verified application frame layout.
const (
	capturedDungeonPosition = "010700785D4CCD485B2C105859DF58595943595959545859596B5859591C5B595945585959"
	capturedDungeonReset    = "010700518E07EE485B2E105859DA5D595959595959A6A6A6A6A6A6A6A65959595959595959"
	capturedTownPosition    = "013800C1B1A8F7585959595B5959591C595959C6585959F9595959"
	// 0x38 frames broadcast inside dungeons: the return-anchor town/area plus
	// the character coordinates.
	capturedAnchorPosition = "0138000353DD92585959595B5959595959595983585959B3595959"
	capturedMovePosition   = "013800328C6BCE5B595959585959595B5959592C5B595929585959"
)

var capturedCodec = partyUDPCodec{key: 0x59, rotate: 0}

func TestParsePartyAppPositionCapturedDungeonFrame(t *testing.T) {
	body, err := hex.DecodeString(capturedDungeonPosition)
	if err != nil {
		t.Fatal(err)
	}
	position, ok := parsePartyAppPosition(body, capturedCodec)
	if !ok {
		t.Fatal("captured dungeon position frame did not decode")
	}
	if !position.Valid() {
		t.Fatalf("captured dungeon position frame is not actionable: %+v", position)
	}
	if position.Sub != partyAppSubDungeonPosition {
		t.Fatalf("subtype = %#x", position.Sub)
	}
	if position.Counter != 0x00014975 {
		t.Fatalf("counter = %d", position.Counter)
	}
	if position.State != partyAppPositionActiveState {
		t.Fatalf("state = %d", position.State)
	}
	if position.BaseX != 269 || position.BaseY != 306 {
		t.Fatalf("base = %d,%d", position.BaseX, position.BaseY)
	}
	if position.X != 581 || position.Y != 284 {
		t.Fatalf("position = %d,%d", position.X, position.Y)
	}
}

func TestParsePartyAppPositionCapturedResetFrame(t *testing.T) {
	body, err := hex.DecodeString(capturedDungeonReset)
	if err != nil {
		t.Fatal(err)
	}
	position, ok := parsePartyAppPosition(body, capturedCodec)
	if !ok {
		t.Fatal("captured reset frame did not decode")
	}
	if position.Valid() {
		t.Fatalf("reset frame must not be actionable: %+v", position)
	}
}

func TestParsePartyAppPositionCapturedTownFrame(t *testing.T) {
	body, err := hex.DecodeString(capturedTownPosition)
	if err != nil {
		t.Fatal(err)
	}
	position, ok := parsePartyAppPosition(body, capturedCodec)
	if !ok {
		t.Fatal("captured town position frame did not decode")
	}
	if position.Sub != partyAppSubTownPosition {
		t.Fatalf("subtype = %#x", position.Sub)
	}
	if position.Flag != 1 || position.Town != 2 || position.Area != 69 {
		t.Fatalf("anchor = flag=%d town=%d area=%d", position.Flag, position.Town, position.Area)
	}
	if position.X != 415 || position.Y != 160 {
		t.Fatalf("position = %d,%d", position.X, position.Y)
	}
}

func TestParsePartyAppPositionCapturedAnchorFrames(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		payload string
		flag    uint32
		town    uint32
		area    uint32
		x, y    int32
	}{
		{name: "dungeon-anchor", payload: capturedAnchorPosition, flag: 1, town: 2, area: 0, x: 474, y: 234},
		{name: "dungeon-move", payload: capturedMovePosition, flag: 2, town: 1, area: 2, x: 629, y: 368},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := hex.DecodeString(testCase.payload)
			if err != nil {
				t.Fatal(err)
			}
			position, ok := parsePartyAppPosition(body, capturedCodec)
			if !ok {
				t.Fatal("captured anchor frame did not decode")
			}
			if position.Flag != testCase.flag || position.Town != testCase.town || position.Area != testCase.area {
				t.Fatalf("anchor = flag=%d town=%d area=%d", position.Flag, position.Town, position.Area)
			}
			if position.X != testCase.x || position.Y != testCase.y {
				t.Fatalf("position = %d,%d", position.X, position.Y)
			}
		})
	}
}

func TestBuildPartyAppTownPositionBodyRoundTrip(t *testing.T) {
	body := buildPartyAppTownPositionBody(1, 2, 0, 474, 234, capturedCodec)
	want, err := hex.DecodeString(capturedAnchorPosition)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(body); got != hex.EncodeToString(want) {
		t.Fatalf("built town frame = %s\nwant %s", got, hex.EncodeToString(want))
	}
	position, ok := parsePartyAppPosition(body, capturedCodec)
	if !ok || position.X != 474 || position.Y != 234 {
		t.Fatalf("round trip = %+v ok=%t", position, ok)
	}
}

func TestParsePartyAppPositionRejectsCorruption(t *testing.T) {
	body, err := hex.DecodeString(capturedDungeonPosition)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), body...)
	corrupted[3] ^= 0xFF
	if _, ok := parsePartyAppPosition(corrupted, capturedCodec); ok {
		t.Fatal("frame with a broken checksum decoded")
	}
	if _, ok := parsePartyAppPosition(body, partyUDPCodec{key: 0x58, rotate: 0}); ok {
		t.Fatal("frame decoded with the wrong codec key")
	}
}

func TestBuildPartyAppPositionBodyRoundTrip(t *testing.T) {
	body := buildPartyAppPositionBody(0x00014975, 390, 269, 306, 581, 284, capturedCodec)
	want, err := hex.DecodeString(capturedDungeonPosition)
	if err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(body); got != hex.EncodeToString(want) {
		t.Fatalf("built frame = %s\nwant %s", got, hex.EncodeToString(want))
	}
	position, ok := parsePartyAppPosition(body, capturedCodec)
	if !ok {
		t.Fatal("built frame did not decode")
	}
	if position.X != 581 || position.Y != 284 || position.State != partyAppPositionActiveState {
		t.Fatalf("round trip = %+v", position)
	}
}

func TestPartyAppPositionChecksumUsesRobotCRCTable(t *testing.T) {
	body := buildPartyAppPositionBody(7, 7, 1, 2, 3, 4, capturedCodec)
	plain := partyUDPDeobfuscate(body[7:], capturedCodec)
	stored := binary.LittleEndian.Uint32(body[3:7])
	if crc32.Checksum(plain, partyUDPCRCTable) != stored {
		t.Fatal("built frame checksum does not cover the plaintext body")
	}
	if len(body) != 7+6+partyAppPositionFields*4 {
		t.Fatalf("frame length = %d", len(body))
	}
}
