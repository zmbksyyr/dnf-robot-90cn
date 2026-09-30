package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestUseLotteryItemBody(t *testing.T) {
	body := UseLotteryItemBody(0, 65)
	if len(body) != 4 {
		t.Fatalf("body length = %d, want 4", len(body))
	}
	if binary.LittleEndian.Uint16(body[0:2]) != 0 {
		t.Fatalf("phase = %d, want 0", binary.LittleEndian.Uint16(body[0:2]))
	}
	if int16(binary.LittleEndian.Uint16(body[2:4])) != 65 {
		t.Fatalf("slot = %d, want 65", int16(binary.LittleEndian.Uint16(body[2:4])))
	}
}

func TestUpgradeItemBody(t *testing.T) {
	body := UpgradeItemBody(UpgradeMethodReinforce, 12, 1001, -1, -1, nil)
	if len(body) != 16 {
		t.Fatalf("body length = %d, want 16", len(body))
	}
	if binary.LittleEndian.Uint16(body[0:2]) != UpgradeMethodReinforce {
		t.Fatalf("method = %d", binary.LittleEndian.Uint16(body[0:2]))
	}
	if int16(binary.LittleEndian.Uint16(body[2:4])) != 12 {
		t.Fatalf("target slot = %d", int16(binary.LittleEndian.Uint16(body[2:4])))
	}
	if int32(binary.LittleEndian.Uint32(body[4:8])) != 1001 {
		t.Fatalf("item id = %d", int32(binary.LittleEndian.Uint32(body[4:8])))
	}
	if int16(binary.LittleEndian.Uint16(body[8:10])) != -1 {
		t.Fatalf("material slot = %d", int16(binary.LittleEndian.Uint16(body[8:10])))
	}
	if int16(binary.LittleEndian.Uint16(body[10:12])) != -1 {
		t.Fatalf("ticket slot = %d", int16(binary.LittleEndian.Uint16(body[10:12])))
	}
	if binary.LittleEndian.Uint32(body[12:16]) != 0 {
		t.Fatalf("name length = %d", binary.LittleEndian.Uint32(body[12:16]))
	}

	named := UpgradeItemBody(UpgradeMethodReinforce, 9, 2002, 358, 3, []byte("ab"))
	if !bytes.Equal(named[16:], []byte("ab")) || binary.LittleEndian.Uint32(named[12:16]) != 2 {
		t.Fatalf("named body = %v", named)
	}
}

func TestParseServerNoticeLottery(t *testing.T) {
	body := make([]byte, 9)
	body[0] = 0x02
	body[1] = 0x01
	binary.LittleEndian.PutUint16(body[2:4], 777)
	binary.LittleEndian.PutUint32(body[4:8], 12345)
	body[8] = 5
	notice, ok := ParseServerNotice(body)
	if !ok {
		t.Fatal("lottery notice not parsed")
	}
	if notice.Kind != "lottery" || notice.UID != 777 || notice.ItemID != 12345 || notice.Level != 5 || notice.Success {
		t.Fatalf("notice = %+v", notice)
	}
}

func TestParseServerNoticeUpgrade(t *testing.T) {
	body := make([]byte, 12)
	body[0] = 0x01
	body[1] = 0x01
	binary.LittleEndian.PutUint16(body[2:4], 42)
	binary.LittleEndian.PutUint32(body[4:8], 999)
	body[8] = 13
	notice, ok := ParseServerNotice(body)
	if !ok {
		t.Fatal("upgrade notice not parsed")
	}
	if notice.Kind != "upgrade" || !notice.Success || notice.UID != 42 || notice.ItemID != 999 || notice.Level != 13 {
		t.Fatalf("notice = %+v", notice)
	}

	body[1] = 0x00
	notice, ok = ParseServerNotice(body)
	if !ok || notice.Success || notice.Level != 13 {
		t.Fatalf("failure notice = %+v ok=%v", notice, ok)
	}
}

func TestParseServerNoticeRejectsUnknown(t *testing.T) {
	if _, ok := ParseServerNotice([]byte{0x03, 0x01, 0, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("unknown notice kind accepted")
	}
	if _, ok := ParseServerNotice([]byte{0x02, 0x02, 0, 0, 0, 0, 0, 0, 0}); ok {
		t.Fatal("malformed lottery notice accepted")
	}
	if _, ok := ParseServerNotice(nil); ok {
		t.Fatal("empty notice accepted")
	}
}

func TestParseServerNoticeAck(t *testing.T) {
	if accepted, valid := ParseServerNoticeAck([]byte{0x01, 0x00, 0x00}); !accepted || !valid {
		t.Fatalf("accepted ack = %v/%v", accepted, valid)
	}
	if accepted, valid := ParseServerNoticeAck([]byte{0x00, 0x09}); accepted || !valid {
		t.Fatalf("rejected ack = %v/%v", accepted, valid)
	}
	if _, valid := ParseServerNoticeAck([]byte{0x07}); valid {
		t.Fatal("unknown ack accepted")
	}
	if _, valid := ParseServerNoticeAck(nil); valid {
		t.Fatal("empty ack accepted")
	}
}
