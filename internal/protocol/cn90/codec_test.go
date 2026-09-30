package cn90

import (
	"encoding/binary"
	"testing"
)

func TestCreateCharacterBodyMatchesCN90Layout(t *testing.T) {
	body, err := CreateCharacterBody(7, []byte("robot01"))
	if err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(body[1:5]); got != 7 {
		t.Fatalf("name length = %d", got)
	}
	if string(body[5:]) != "robot01\x00" {
		t.Fatalf("body tail = %q", body[5:])
	}
}

func TestCheckCharacterNameBodyMatchesCN90Layout(t *testing.T) {
	body, err := CheckCharacterNameBody([]byte("robot01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 11 || binary.LittleEndian.Uint32(body[:4]) != 7 || string(body[4:]) != "robot01" {
		t.Fatalf("check name body = %X", body)
	}
	if _, err := CheckCharacterNameBody([]byte("x")); err == nil {
		t.Fatal("short check name unexpectedly accepted")
	}
}

func TestDeleteCharacterBodyMatchesCN90Layout(t *testing.T) {
	body, err := DeleteCharacterBody(0x0102, []byte("robot01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 13 || binary.LittleEndian.Uint16(body[0:2]) != 0x0102 ||
		binary.LittleEndian.Uint32(body[2:6]) != 7 || string(body[6:]) != "robot01" {
		t.Fatalf("delete body = %X", body)
	}
	if _, err := DeleteCharacterBody(0, nil); err == nil {
		t.Fatal("empty delete name unexpectedly accepted")
	}
	if _, err := DeleteCharacterBody(0, make([]byte, 31)); err == nil {
		t.Fatal("oversized delete name unexpectedly accepted")
	}
}

func TestCharacterRosterRequestUsesGetUserInfoModeTwo(t *testing.T) {
	body := CharacterRosterRequestBody()
	if len(body) != 3 || body[0] != 0 || body[1] != 0 || body[2] != 2 {
		t.Fatalf("roster request body = %X", body)
	}
}

func TestEncodeDecodeFrame(t *testing.T) {
	frame := make([]byte, ResponseHeaderSize+3)
	frame[0] = 1
	binary.LittleEndian.PutUint16(frame[1:3], CmdCreateCharacter)
	binary.LittleEndian.PutUint32(frame[3:7], uint32(len(frame)))
	copy(frame[ResponseHeaderSize:], []byte{7, 8, 9})
	pkt, err := DecodeFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if pkt.Type != CmdCreateCharacter || string(pkt.Body) != string([]byte{7, 8, 9}) {
		t.Fatalf("packet = %+v", pkt)
	}
}

func TestEncodeUsesFourteenByteClientHeader(t *testing.T) {
	frame := Encode(1, CmdLogin, []byte{7, 8, 9})
	if len(frame) != RequestHeaderSize+3 || frame[RequestHeaderSize] != 7 {
		t.Fatalf("frame length/body = %d/%v", len(frame), frame)
	}
}

func TestParseAcceptableQuestList(t *testing.T) {
	body := []byte{13, 2, 0, 0xF8, 0x03, 0x65, 0x00}
	list, err := ParseAcceptableQuestList(body)
	if err != nil {
		t.Fatal(err)
	}
	if list.Level != 13 || len(list.QuestIDs) != 2 || list.QuestIDs[0] != 1016 || list.QuestIDs[1] != 101 {
		t.Fatalf("list = %+v", list)
	}
	list.QuestIDs[0] = 0
	if body[3] != 0xF8 {
		t.Fatal("parser did not copy quest IDs")
	}
}

func TestParseAcceptableQuestListRejectsInvalidLength(t *testing.T) {
	if _, err := ParseAcceptableQuestList([]byte{1, 1, 0}); err == nil {
		t.Fatal("truncated quest list unexpectedly parsed")
	}
}

func TestQuestCommandBodiesUseVerifiedEchoAndSentinel(t *testing.T) {
	accept := AcceptQuestBody(1016)
	if len(accept) != 4 || binary.LittleEndian.Uint16(accept[2:]) != 1016 {
		t.Fatalf("accept body = %X", accept)
	}
	finish := FinishQuestBody(1016, -1, 0)
	if len(finish) != 10 || binary.LittleEndian.Uint16(finish[2:4]) != 1016 ||
		binary.LittleEndian.Uint16(finish[4:6]) != 0xFFFF ||
		binary.LittleEndian.Uint16(finish[8:10]) != 0xFFFF {
		t.Fatalf("finish body = %X", finish)
	}
}

func TestSetQuestTriggerBodyUsesVerifiedFields(t *testing.T) {
	body := SetQuestTriggerBody(1016, 2, true)
	if len(body) != 6 || binary.LittleEndian.Uint16(body[2:4]) != 1016 || body[4] != 2 || body[5] != 1 {
		t.Fatalf("set trigger body = %v", body)
	}
	body = SetQuestTriggerBody(1016, 2, false)
	if body[5] != 0 {
		t.Fatalf("non-increment trigger flag = %d", body[5])
	}
}

func TestSetUserAreaBodyUsesSixByteTownTransitionLayout(t *testing.T) {
	body := SetUserAreaBody(1, 2, 0x0123, 0x0045)
	if len(body) != 6 || body[0] != 1 || body[1] != 2 ||
		binary.LittleEndian.Uint16(body[2:4]) != 0x0123 ||
		binary.LittleEndian.Uint16(body[4:6]) != 0x0045 {
		t.Fatalf("set user area body = %X", body)
	}
}

func TestSelectCharacterBodyUsesTwoByteSlot(t *testing.T) {
	body := SelectCharacterBody(0x0102)
	if len(body) != 2 || binary.LittleEndian.Uint16(body) != 0x0102 {
		t.Fatalf("body = %v", body)
	}
}

func TestSelectCharacterUIDUsesVerifiedAckOffset(t *testing.T) {
	body := make([]byte, 11)
	body[0] = 1
	binary.LittleEndian.PutUint16(body[9:11], 0x1234)
	uid, err := SelectCharacterUID(body)
	if err != nil || uid != 0x1234 {
		t.Fatalf("uid=%d err=%v", uid, err)
	}
	for _, invalid := range [][]byte{body[:10], make([]byte, 11)} {
		if _, err := SelectCharacterUID(invalid); err == nil {
			t.Fatalf("invalid select response %X unexpectedly parsed", invalid)
		}
	}
}

func TestMoveMapBodyIsFixedWidth(t *testing.T) {
	body := MoveMapBody(MoveMapRequest{NextX: 2, NextY: 3, PathPositionX: 100, PathPositionY: 200})
	if len(body) != 64 || body[0] != 2 || body[1] != 3 {
		t.Fatalf("body length/content = %d/%v", len(body), body[:2])
	}
	if binary.LittleEndian.Uint32(body[2:6]) != 100 || binary.LittleEndian.Uint32(body[6:10]) != 200 {
		t.Fatalf("path positions = %v", body[2:10])
	}
}

func TestVerifiedDungeonRequestBodies(t *testing.T) {
	enter := EnterSelectDungeonBody(144)
	if len(enter) != 4 || binary.LittleEndian.Uint32(enter) != 144 {
		t.Fatalf("enter body = %X", enter)
	}
	selectBody := SelectDungeonBody(144, 0, 0, 0)
	if len(selectBody) != 15 || binary.LittleEndian.Uint32(selectBody[0:4]) != 144 ||
		binary.LittleEndian.Uint16(selectBody[7:9]) != 0xFFFF {
		t.Fatalf("select body = %X", selectBody)
	}
	change := ChangeTutorialFlagBody(30, 1)
	if len(change) != 6 || change[0] != 0 || binary.LittleEndian.Uint32(change[1:5]) != 30 || change[5] != 1 {
		t.Fatalf("tutorial body = %X", change)
	}
	if FinishLoadingBody() != nil {
		t.Fatal("finish loading body must be empty")
	}
}

func TestLoginAndMessageBodiesUseLittleEndianDStrings(t *testing.T) {
	login, err := LoginBody("robot", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(login[:4]) != 5 || binary.LittleEndian.Uint32(login[9:13]) != 4 {
		t.Fatalf("login body = %v", login)
	}
	message, err := SendMessageBody(3, 0, 0, []byte("hello"))
	if err != nil || binary.LittleEndian.Uint32(message[7:11]) != 5 {
		t.Fatalf("message body = %v err=%v", message, err)
	}
}

func TestPartyProbeBodiesUseVerifiedCN90Shapes(t *testing.T) {
	settings := []byte{0, 0, 4, 0, 0, 0, 0, 5, 0, 0, 0xFF, 0xFF}
	body, err := SetPartyInfoBody(settings)
	if err != nil || len(body) != 12 || string(body) != string(settings) {
		t.Fatalf("party settings body = %X err=%v", body, err)
	}
	body[0] = 9
	if settings[0] != 0 {
		t.Fatal("party settings builder did not copy input")
	}
	if _, err := SetPartyInfoBody(settings[:11]); err == nil {
		t.Fatal("short party settings unexpectedly accepted")
	}

	peer := RequestPeerBody(0x1234, 0, -7)
	if len(peer) != 7 || binary.LittleEndian.Uint16(peer[:2]) != 0x1234 ||
		peer[2] != 0 || int32(binary.LittleEndian.Uint32(peer[3:])) != -7 {
		t.Fatalf("request peer body = %X", peer)
	}
	accepted := ResponsePeerBody(0x1234, 0)
	if len(accepted) != 7 || binary.LittleEndian.Uint16(accepted[:2]) != 0x1234 || accepted[2] != 0 {
		t.Fatalf("response peer body = %X", accepted)
	}
	if LeavePartyBody() != nil {
		t.Fatal("leave party body must be empty")
	}
	if got := WalkoutPartyMemberBody(3); len(got) != 1 || got[0] != 3 {
		t.Fatalf("walkout body = %X", got)
	}
}

func TestGuildInviteUsesVerifiedCN90Shapes(t *testing.T) {
	body := make([]byte, 7+len("guild-leader"))
	binary.LittleEndian.PutUint32(body[0:4], 0x01020304)
	binary.LittleEndian.PutUint16(body[4:6], 0x1234)
	body[6] = byte(len("guild-leader"))
	copy(body[7:], "guild-leader")

	invite, err := ParseGuildInvite(body)
	if err != nil {
		t.Fatal(err)
	}
	if invite.InviterCharacterID != 0x01020304 || invite.InviterUID != 0x1234 || string(invite.InviterName) != "guild-leader" {
		t.Fatalf("invitation = %+v", invite)
	}
	invite.InviterName[0] = 'x'
	if body[7] != 'g' {
		t.Fatal("guild invitation parser did not copy inviter name")
	}
	if got := GuildInviteReplyBody(true); len(got) != 1 || got[0] != 1 {
		t.Fatalf("accept body = %X", got)
	}
	if got := GuildInviteReplyBody(false); len(got) != 1 || got[0] != 0 {
		t.Fatalf("decline body = %X", got)
	}
}

func TestParseGuildInviteRejectsMalformedIdentityAndLength(t *testing.T) {
	valid := make([]byte, 8)
	binary.LittleEndian.PutUint32(valid[0:4], 1)
	binary.LittleEndian.PutUint16(valid[4:6], 2)
	valid[6], valid[7] = 1, 'a'

	tests := [][]byte{
		nil,
		valid[:7],
		append(append([]byte(nil), valid...), 0),
		func() []byte { body := append([]byte(nil), valid...); body[0] = 0; return body }(),
		func() []byte { body := append([]byte(nil), valid...); body[4], body[5] = 0, 0; return body }(),
		func() []byte { body := append([]byte(nil), valid...); body[4], body[5] = 0xFF, 0xFF; return body }(),
		func() []byte { body := append([]byte(nil), valid...); body[6] = 0; return body }(),
		func() []byte { body := append([]byte(nil), valid...); body[6] = 30; return body }(),
	}
	for _, body := range tests {
		if _, err := ParseGuildInvite(body); err == nil {
			t.Fatalf("malformed guild invitation %X unexpectedly parsed", body)
		}
	}
}

func TestOrdinaryPartySettingsUsesServiceCapacity(t *testing.T) {
	settings := OrdinaryPartySettings()
	if len(settings) != 12 {
		t.Fatalf("party settings length = %d, want 12", len(settings))
	}
	// The service parses UserMax from index 2 of the create form.
	if settings[2] != 4 {
		t.Fatalf("party UserMax byte = %d, want 4", settings[2])
	}
	settings[2] = 9
	if OrdinaryPartySettings()[2] != 4 {
		t.Fatal("OrdinaryPartySettings returned shared state")
	}
	if _, err := SetPartyInfoBody(OrdinaryPartySettings()); err != nil {
		t.Fatal(err)
	}
}
