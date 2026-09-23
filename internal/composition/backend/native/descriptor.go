package native

import "robot/internal/shared"

func Info() shared.BackendInfo {
	capabilities := shared.CapabilityMatrix(shared.CapabilityStatus{Enabled: true})
	capabilities[shared.CapabilityDungeonFollow] = shared.CapabilityStatus{Enabled: true, Mode: "account"}
	return shared.BackendInfo{
		ID: shared.BackendNative, DisplayName: "Native", SupportedOS: []string{"linux"}, Selectable: true,
		Capabilities: capabilities, MaxOnline: 600,
	}
}
