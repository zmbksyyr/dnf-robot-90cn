package shared

import (
	"fmt"
	"runtime"
)

type BackendID string

const BackendNative BackendID = "native"

type BackendCapability string

const (
	CapabilityProvision   BackendCapability = "provision"
	CapabilityTownMove    BackendCapability = "town_move"
	CapabilityDungeonMove BackendCapability = "dungeon_move"
	CapabilityShout       BackendCapability = "shout"
	CapabilityStore       BackendCapability = "store"
	CapabilityParty       BackendCapability = "party"
	CapabilitySkill       BackendCapability = "skill"
	CapabilityMarket      BackendCapability = "market"
)

type CapabilityStatus struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

type BackendInfo struct {
	ID           BackendID                              `json:"id"`
	DisplayName  string                                 `json:"display_name"`
	SupportedOS  []string                               `json:"supported_os"`
	Capabilities map[BackendCapability]CapabilityStatus `json:"capabilities"`
}

// KnownBackends is the explicit catalog exposed to composition and entry
// layers. It only contains adapters whose protocol bundle is implemented.
func KnownBackends() []BackendInfo {
	capabilities := make(map[BackendCapability]CapabilityStatus)
	for _, operation := range []BackendCapability{
		CapabilityProvision, CapabilityTownMove, CapabilityDungeonMove,
		CapabilityShout, CapabilityStore, CapabilityParty, CapabilitySkill,
		CapabilityMarket,
	} {
		capabilities[operation] = CapabilityStatus{Enabled: true}
	}
	return []BackendInfo{{
		ID: BackendNative, DisplayName: "Native DNF", SupportedOS: []string{"linux"},
		Capabilities: capabilities,
	}}
}

func SelectBackend(id BackendID, platform string) (BackendInfo, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	for _, info := range KnownBackends() {
		if info.ID != id {
			continue
		}
		for _, supported := range info.SupportedOS {
			if supported == platform {
				return info, nil
			}
		}
		return BackendInfo{}, fmt.Errorf("backend %s does not support %s", id, platform)
	}
	return BackendInfo{}, fmt.Errorf("unknown backend %q", id)
}

func (b BackendInfo) Supports(capability BackendCapability) bool {
	return b.Capabilities[capability].Enabled
}

const CodeBackendCapabilityUnsupported = "backend_capability_unsupported"

type UnsupportedCapabilityError struct {
	Backend   BackendID
	Operation BackendCapability
	Reason    string
}

func (e UnsupportedCapabilityError) Error() string {
	return fmt.Sprintf("%s: backend=%s operation=%s: %s", CodeBackendCapabilityUnsupported, e.Backend, e.Operation, e.Reason)
}

func (b BackendInfo) Require(capability BackendCapability) error {
	if b.Supports(capability) {
		return nil
	}
	status := b.Capabilities[capability]
	if status.Reason == "" {
		status.Reason = "operation is not implemented by this backend"
	}
	return UnsupportedCapabilityError{Backend: b.ID, Operation: capability, Reason: status.Reason}
}
