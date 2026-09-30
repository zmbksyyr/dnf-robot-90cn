package cn90

import "robot/internal/shared"

const BackendID shared.BackendID = "sim_90cn"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "90CN protocol operation is not implemented yet"})
	capabilities[shared.CapabilityProvision] = shared.CapabilityStatus{Enabled: true}
	capabilities[shared.CapabilityTownMove] = shared.CapabilityStatus{Enabled: true, Reason: "coordinates and verified town-area transitions"}
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Enabled: true, Mode: "auto_accept", Reason: "accepts ordinary party invitations without filtering by inviter account"}
	capabilities[shared.CapabilityParty] = shared.CapabilityStatus{Enabled: true, Mode: "follower", Reason: "accepts leader invitations and mirrors the leader's town position and area projections; no active invite, party creation or dungeon room movement"}
	capabilities[shared.CapabilityGuildInvite] = shared.CapabilityStatus{Enabled: true, Mode: "auto_accept", Reason: "automatically accepts verified 90CN guild invitation notifications"}
	capabilities[shared.CapabilityShout] = shared.CapabilityStatus{Enabled: true, Reason: "area channel only; party requires the separate party capability"}
	capabilities[shared.CapabilityStore] = shared.CapabilityStatus{Enabled: true, Mode: "expert_job", Reason: "disassembler machine and enchanter stall via CREATE_EXPERT_JOB_STORE; the private item stall is not implemented"}
	capabilities[shared.CapabilityWorldShout] = shared.CapabilityStatus{Reason: "90CN SEND_MESSAGE has no generic world-recipient path"}
	capabilities[shared.CapabilityCleanup] = shared.CapabilityStatus{Enabled: true, Reason: "verified character deletion protocol and robot-state cleanup"}
	capabilities[shared.CapabilityDangerousDelete] = shared.CapabilityStatus{Enabled: true, Reason: "adapter-owned SQLite purge for invisible robot accounts and characters"}
	capabilities[shared.CapabilityPartyDebug] = shared.CapabilityStatus{Reason: "90CN party diagnostics are not implemented"}
	capabilities[shared.CapabilityMailNotification] = shared.CapabilityStatus{Reason: "90CN mail notification is not implemented"}
	capabilities[shared.CapabilityDatabase] = shared.CapabilityStatus{Enabled: true, Mode: "sqlite_health", Reason: "validates the configured SQLite file and required schema"}
	capabilities[shared.CapabilityDiagnostics] = shared.CapabilityStatus{Reason: "90CN diagnostics are not implemented"}
	capabilities[shared.CapabilitySystemAnnouncement] = shared.CapabilityStatus{Reason: "90CN system announcement transport is not implemented"}
	capabilities[shared.CapabilityServerNotice] = shared.CapabilityStatus{Enabled: true, Mode: "lottery_upgrade", Reason: "robot-side lottery box opens and +12 reinforcements drive the server's 0x0056 item notices; stock is written offline per robot"}
	capabilities[shared.CapabilityServiceControl] = shared.CapabilityStatus{Reason: "90CN service control is not implemented"}
	capabilities[shared.CapabilityDungeonMove] = shared.CapabilityStatus{Reason: "dungeon room movement and the in-dungeon position plane are not implemented"}
	capabilities[shared.CapabilityMarket] = shared.CapabilityStatus{Reason: "90CN exposes auction opcode enums only; no verified auction or gold-consignment handler/service is present"}
	return shared.BackendInfo{
		ID: BackendID, DisplayName: "90CN", SupportedOS: []string{"linux", "windows"}, Selectable: true,
		Capabilities: capabilities, MaxOnline: 10000,
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", LabelZH: "服务端目录", Hint: "DNF90 one-click project directory (containing runtime/) or the runtime directory itself; runtime/config/instance.json defines the channel, database and PVF paths.", HintZH: "DNF90 一键工程目录（包含 runtime/）或 runtime 目录本身；通道、数据库与 PVF 路径由 runtime/config/instance.json 定义。", InputType: "path", Required: true, RuntimeSource: "server_directory"},
			{Key: "server_host", Label: "Game host", LabelZH: "游戏地址", Hint: "Game channel host; the 90CN profile binds the channel to 127.0.0.1.", HintZH: "游戏通道地址；90CN 方案将通道绑定在 127.0.0.1。", InputType: "text", Required: true, Default: "127.0.0.1", RuntimeSource: "game_host"},
			{Key: "game_port", Label: "Port", LabelZH: "端口", Hint: "Optional channel port override; empty follows instance.json server.channelListen.", HintZH: "可选通道端口覆盖；留空则跟随 instance.json 的 server.channelListen。", InputType: "number", RuntimeSource: "game_port"},
			{Key: "database_path", Label: "Database", LabelZH: "数据库", Hint: "Optional SQLite override (absolute, or relative to the runtime directory); empty follows instance.json database.path.", HintZH: "可选 SQLite 覆盖（绝对路径，或相对 runtime 目录）；留空则跟随 instance.json 的 database.path。", InputType: "path", Placeholder: "runtime/data/dnf90.db (auto)"},
		},
	}
}
