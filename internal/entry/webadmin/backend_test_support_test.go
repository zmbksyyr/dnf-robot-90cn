package webadmin

import (
	"robot/internal/shared"
)

func testBackendCatalog() []shared.BackendInfo {
	simulatorCapabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Reason: "unsupported in test backend"})
	for _, capability := range []shared.BackendCapability{
		shared.CapabilityProvision, shared.CapabilityTownMove, shared.CapabilityDungeonFollow,
		shared.CapabilityShout, shared.CapabilityCleanup, shared.CapabilityDatabase,
	} {
		simulatorCapabilities[capability] = shared.CapabilityStatus{Enabled: true}
	}
	simulatorCapabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Enabled: true, Mode: "toggle"}
	simulatorCapabilities[shared.CapabilityDatabase] = shared.CapabilityStatus{Enabled: true, Mode: "sqlite_health"}
	return []shared.BackendInfo{
		{ID: shared.BackendID("test"), DisplayName: "S4A21", SupportedOS: []string{"linux", "windows"}, Selectable: true, Capabilities: simulatorCapabilities, Settings: []shared.BackendSetting{
			{Key: "server_directory", Label: "Server directory", InputType: "path", Required: true},
			{Key: "server_host", Label: "Host", InputType: "text", Required: true, Default: "127.0.0.1"},
			{Key: "game_port", Label: "Port", InputType: "number", Required: true, Default: "10011"},
			{Key: "database_path", Label: "Database", InputType: "path"},
		}},
	}
}
