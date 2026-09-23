package s4a21

import "robot/internal/shared"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "S4A21 protocol operation is not implemented yet"})
	capabilities[shared.CapabilityProvision] = shared.CapabilityStatus{Enabled: true}
	capabilities[shared.CapabilityTownMove] = shared.CapabilityStatus{Enabled: true, Reason: "coordinates and verified town-area transitions"}
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Enabled: true, Mode: "toggle", Reason: "accepts ordinary party invitations without filtering by inviter account"}
	capabilities[shared.CapabilityShout] = shared.CapabilityStatus{Enabled: true, Reason: "area channel only; party requires the separate party capability"}
	capabilities[shared.CapabilityWorldShout] = shared.CapabilityStatus{Reason: "S4A21 SEND_MESSAGE has no generic world-recipient path"}
	capabilities[shared.CapabilityCleanup] = shared.CapabilityStatus{Enabled: true, Reason: "verified character deletion protocol and robot-state cleanup"}
	capabilities[shared.CapabilityDangerousDelete] = shared.CapabilityStatus{Reason: "S4A21 supports protected protocol cleanup only"}
	capabilities[shared.CapabilityCompatibility] = shared.CapabilityStatus{Reason: "native memory compatibility patches are not applicable to S4A21"}
	capabilities[shared.CapabilityKeypair] = shared.CapabilityStatus{Reason: "native RSA keypair is not applicable to S4A21"}
	capabilities[shared.CapabilityDatabase] = shared.CapabilityStatus{Enabled: true, Mode: "sqlite_health", Reason: "validates the configured SQLite file and required schema"}
	capabilities[shared.CapabilityDiagnostics] = shared.CapabilityStatus{Reason: "native runtime diagnostics are not available for S4A21"}
	capabilities[shared.CapabilitySystemAnnouncement] = shared.CapabilityStatus{Reason: "S4A21 system announcement transport is not implemented"}
	capabilities[shared.CapabilityServiceControl] = shared.CapabilityStatus{Reason: "native service scripts and process discovery are not applicable to S4A21"}
	capabilities[shared.CapabilityDungeonMove] = shared.CapabilityStatus{Reason: "only server-directed party following is available; active dungeon movement is unsupported"}
	return shared.BackendInfo{
		ID: shared.BackendS4A21, DisplayName: "S4A21", SupportedOS: []string{"linux", "windows"}, Selectable: true,
		Capabilities: capabilities,
		Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", InputType: "path", Required: true, RuntimeSource: "server_directory"},
			{Key: "server_host", Label: "Host", InputType: "text", Required: true, Default: "127.0.0.1", RuntimeSource: "game_host"},
			{Key: "game_port", Label: "Port", InputType: "number", Required: true, Default: "10011", RuntimeSource: "game_port"},
			{Key: "database_path", Label: "Database", InputType: "path", Placeholder: `Data\inventory.db (auto)`, DerivedFrom: "server_directory", PathSuffix: []string{"Data", "inventory.db"}},
		},
	}
}
