package cn90

import "encoding/binary"

// Server-notice protocol surface (90CN Server90CN):
//
//	USE_LOTTERY_ITEM (0x001B) body = {phase:u16, slot:i16}
//	UPGRADE_ITEM     (0x0050) body = {method:u16, targetSlot:i16,
//	                                 targetItemId:i32, materialSlot:i16,
//	                                 optionalTicketSlot:i16, nameLen:i32, name}
//	server notice    (0x0056) body = upgrade: {0x01, success, uid:u16,
//	                                 itemId:i32, level:u8, ...}
//	                                 lottery: {0x02, 0x01, uid:u16,
//	                                 itemId:i32, level:u8}
//
// Both request acks answer on the request type with result byte 0x01 accepted
// and 0x00 rejected.
const (
	CmdUseLotteryItem uint16 = 0x001B
	CmdUpgradeItem    uint16 = 0x0050
	NotiServerNotice  uint16 = 0x0056
)

// ItemUpgradeMethod mirrors the server enum. The robot only uses Reinforce.
const (
	UpgradeMethodReinforce         uint16 = 0
	UpgradeMethodAmplify           uint16 = 1
	UpgradeMethodAdvancedReinforce uint16 = 2
)

// Server notice discriminator bytes on NotiServerNotice.
const (
	ServerNoticeKindUpgrade byte = 0x01
	ServerNoticeKindLottery byte = 0x02
)

// ServerNoticeKindLotteryConfirm is the fixed second byte of a lottery notice.
const ServerNoticeKindLotteryConfirm byte = 0x01

// UseLotteryItemBody builds the lottery open request. Phase 0 opens the box
// directly; phase 1 is the client's fast-open confirmation.
func UseLotteryItemBody(phase uint16, slot int16) []byte {
	body := make([]byte, 4)
	binary.LittleEndian.PutUint16(body[0:2], phase)
	binary.LittleEndian.PutUint16(body[2:4], uint16(slot))
	return body
}

// UpgradeItemBody builds the reinforcement request. The optional ticket slot
// uses -1 when no protect ticket is supplied.
func UpgradeItemBody(method uint16, targetSlot int16, targetItemID int32, materialSlot, optionalTicketSlot int16, name []byte) []byte {
	body := make([]byte, 16+len(name))
	binary.LittleEndian.PutUint16(body[0:2], method)
	binary.LittleEndian.PutUint16(body[2:4], uint16(targetSlot))
	binary.LittleEndian.PutUint32(body[4:8], uint32(targetItemID))
	binary.LittleEndian.PutUint16(body[8:10], uint16(materialSlot))
	binary.LittleEndian.PutUint16(body[10:12], uint16(optionalTicketSlot))
	binary.LittleEndian.PutUint32(body[12:16], uint32(len(name)))
	copy(body[16:], name)
	return body
}

// ServerNotice is one parsed 0x0056 server-wide item notice.
type ServerNotice struct {
	Kind    string
	UID     int
	ItemID  int
	Level   int
	Success bool
}

// ParseServerNotice parses the upgrade and lottery notice bodies the server
// broadcasts through BroadcastToPortAsync.
func ParseServerNotice(body []byte) (ServerNotice, bool) {
	if len(body) < 9 {
		return ServerNotice{}, false
	}
	switch body[0] {
	case ServerNoticeKindUpgrade:
		return ServerNotice{
			Kind:    "upgrade",
			Success: body[1] == 0x01,
			UID:     int(binary.LittleEndian.Uint16(body[2:4])),
			ItemID:  int(int32(binary.LittleEndian.Uint32(body[4:8]))),
			Level:   int(body[8]),
		}, true
	case ServerNoticeKindLottery:
		if body[1] != ServerNoticeKindLotteryConfirm {
			return ServerNotice{}, false
		}
		return ServerNotice{
			Kind:   "lottery",
			UID:    int(binary.LittleEndian.Uint16(body[2:4])),
			ItemID: int(int32(binary.LittleEndian.Uint32(body[4:8]))),
			Level:  int(body[8]),
		}, true
	default:
		return ServerNotice{}, false
	}
}

// ParseServerNoticeAck reports whether a request ack accepted the action. The
// server answers both request types with result byte 0x01 or 0x00 followed by
// kind-specific fields.
func ParseServerNoticeAck(body []byte) (accepted bool, valid bool) {
	if len(body) < 1 {
		return false, false
	}
	switch body[0] {
	case 0x01:
		return true, true
	case 0x00:
		return false, true
	default:
		return false, false
	}
}
