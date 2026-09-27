package s4a21

import "robot/internal/shared"

const BackendID shared.BackendID = "sim_a21"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "S4A21 protocol operation is not implemented yet"})
	capabilities[shared.CapabilityProvision] = shared.CapabilityStatus{Enabled: true}
	capabilities[shared.CapabilityTownMove] = shared.CapabilityStatus{Enabled: true, Reason: "coordinates and verified town-area transitions"}
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Enabled: true, Mode: "auto_accept", Reason: "accepts ordinary party invitations without filtering by inviter account"}
	capabilities[shared.CapabilityParty] = shared.CapabilityStatus{Enabled: true, Mode: "follower", Reason: "accepts leader invitations and follows server party projections; no active invite or party creation"}
	capabilities[shared.CapabilityGuildInvite] = shared.CapabilityStatus{Enabled: true, Mode: "auto_accept", Reason: "automatically accepts verified A21 guild invitation notifications"}
	capabilities[shared.CapabilityShout] = shared.CapabilityStatus{Enabled: true, Reason: "area channel only; party requires the separate party capability"}
	capabilities[shared.CapabilityWorldShout] = shared.CapabilityStatus{Reason: "S4A21 SEND_MESSAGE has no generic world-recipient path"}
	capabilities[shared.CapabilityCleanup] = shared.CapabilityStatus{Enabled: true, Reason: "verified character deletion protocol and robot-state cleanup"}
	capabilities[shared.CapabilityDangerousDelete] = shared.CapabilityStatus{Enabled: true, Reason: "adapter-owned SQLite purge for invisible robot accounts and characters"}
	capabilities[shared.CapabilityPartyDebug] = shared.CapabilityStatus{Reason: "S4A21 party diagnostics are not implemented"}
	capabilities[shared.CapabilityMailNotification] = shared.CapabilityStatus{Reason: "S4A21 mail notification is not implemented"}
	capabilities[shared.CapabilityDatabase] = shared.CapabilityStatus{Enabled: true, Mode: "sqlite_health", Reason: "validates the configured SQLite file and required schema"}
	capabilities[shared.CapabilityDiagnostics] = shared.CapabilityStatus{Reason: "S4A21 diagnostics are not implemented"}
	capabilities[shared.CapabilitySystemAnnouncement] = shared.CapabilityStatus{Reason: "S4A21 system announcement transport is not implemented"}
	capabilities[shared.CapabilityServiceControl] = shared.CapabilityStatus{Reason: "S4A21 service control is not implemented"}
	capabilities[shared.CapabilityDungeonMove] = shared.CapabilityStatus{Reason: "only server-directed party following is available; active dungeon movement is unsupported"}
	capabilities[shared.CapabilityMarket] = shared.CapabilityStatus{Reason: "S4A21 exposes auction opcode enums only; no verified auction or gold-consignment handler/service is present"}
	return shared.BackendInfo{
		ID: BackendID, DisplayName: "S4A21", SupportedOS: []string{"linux", "windows"}, Selectable: true,
		Capabilities: capabilities, MaxOnline: 10000,
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", LabelZH: "服务端目录", Hint: "Server directory containing the server executable and Data; Script.pvf is read from Data/Pvf/Script.pvf, honoring PVF_ARCHIVE_PATH.", HintZH: "包含服务端可执行文件和 Data 目录的服务端目录；Script.pvf 从 Data/Pvf/Script.pvf 读取，并遵循 PVF_ARCHIVE_PATH。", InputType: "path", Required: true, RuntimeSource: "server_directory"},
			{Key: "server_host", Label: "Game host", LabelZH: "游戏地址", Hint: "Game protocol address.", HintZH: "游戏协议地址。", InputType: "text", Required: true, Default: "127.0.0.1", RuntimeSource: "game_host"},
			{Key: "game_port", Label: "Port", LabelZH: "端口", Hint: "Game protocol port.", HintZH: "游戏协议端口。", InputType: "number", Required: true, Default: "10011", RuntimeSource: "game_port"},
			{Key: "database_path", Label: "Database", LabelZH: "数据库", Hint: `Optional override; defaults to INVENTORY_DATABASE_PATH or Data\inventory.db under the server directory.`, HintZH: `可选覆盖；默认使用 INVENTORY_DATABASE_PATH 或服务目录下的 Data\inventory.db。`, InputType: "path", Placeholder: `Data\inventory.db (auto)`, DerivedFrom: "server_directory", PathSuffix: []string{"Data", "inventory.db"}},
		},
	}
}
