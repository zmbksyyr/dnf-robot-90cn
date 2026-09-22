package shared

import "fmt"

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
