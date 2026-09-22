package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"time"
)

type BackendID string

const BackendNative BackendID = "native"
const BackendS4A21 BackendID = "sim_a21"

type BackendCapability string

const (
	CapabilityProvision   BackendCapability = "provision"
	CapabilityTownMove    BackendCapability = "town_move"
	CapabilityDungeonMove BackendCapability = "dungeon_move"
	CapabilityShout       BackendCapability = "shout"
	CapabilityWorldShout  BackendCapability = "world_shout"
	CapabilityStore       BackendCapability = "store"
	CapabilityParty       BackendCapability = "party"
	CapabilitySkill       BackendCapability = "skill"
	CapabilityMarket      BackendCapability = "market"
	CapabilityCleanup     BackendCapability = "cleanup"
)

type CapabilityStatus struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
}

type BackendInfo struct {
	ID           BackendID                              `json:"id"`
	DisplayName  string                                 `json:"display_name"`
	SupportedOS  []string                               `json:"supported_os"`
	Selectable   bool                                   `json:"selectable"`
	Reason       string                                 `json:"reason,omitempty"`
	Capabilities map[BackendCapability]CapabilityStatus `json:"capabilities"`
}

type BackendSelection struct {
	BackendID        BackendID `json:"backend_id"`
	ConfigGeneration uint64    `json:"config_generation"`
	SelectedAt       time.Time `json:"selected_at"`
}

type ProvisionCharacterRequest struct {
	AccountName   string
	PasswordHash  string
	CharacterName string
	Job           int
	// RobotUID is robot-owned metadata and is not serialized into a backend
	// protocol packet. It keeps a successful protocol result linked to the
	// scheduler's local robot record.
	RobotUID int
}

type ProvisionCharacterResult struct {
	Backend       BackendID
	CharacterName string
	Created       bool
	RobotUID      int
	// BackendSlot is meaningful for backends whose roster is slot-based.
	// UID/CID remain unset when the backend does not expose native IDs.
	BackendSlot *uint16
}

type CharacterProvisioner interface {
	ProvisionCharacter(context.Context, ProvisionCharacterRequest) (ProvisionCharacterResult, error)
}

// BatchCharacterProvisioner is optional. Callers can use it when a backend
// can provision several characters through its own protocol workflow while
// preserving partial results on interruption.
type BatchCharacterProvisioner interface {
	ProvisionCharacters(context.Context, []ProvisionCharacterRequest) ([]ProvisionCharacterResult, error)
}

func DecodeBackendSelection(data []byte) (BackendSelection, error) {
	selection := BackendSelection{BackendID: BackendNative}
	if len(data) == 0 {
		return selection, nil
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		return BackendSelection{}, fmt.Errorf("invalid backend selection: %w", err)
	}
	if selection.BackendID == "" {
		return BackendSelection{}, fmt.Errorf("backend selection has empty backend_id")
	}
	return selection, nil
}

// KnownBackends is the explicit catalog exposed to composition and entry
// layers. It only contains adapters whose protocol bundle is implemented.
func KnownBackends() []BackendInfo {
	capabilities := make(map[BackendCapability]CapabilityStatus)
	for _, operation := range []BackendCapability{
		CapabilityProvision, CapabilityTownMove, CapabilityDungeonMove,
		CapabilityShout, CapabilityWorldShout, CapabilityStore, CapabilityParty, CapabilitySkill,
		CapabilityMarket, CapabilityCleanup,
	} {
		capabilities[operation] = CapabilityStatus{Enabled: true}
	}
	return []BackendInfo{{
		ID: BackendNative, DisplayName: "Native DNF", SupportedOS: []string{"linux"}, Selectable: true,
		Capabilities: capabilities,
	}, {
		ID: BackendS4A21, DisplayName: "S4A21 Simulator", SupportedOS: []string{"linux", "windows"},
		Selectable:   true,
		Capabilities: s4a21Capabilities(),
	}}
}

func s4a21Capabilities() map[BackendCapability]CapabilityStatus {
	capabilities := unavailableCapabilities("S4A21 protocol operation is not implemented yet")
	capabilities[CapabilityProvision] = CapabilityStatus{Enabled: true}
	capabilities[CapabilityTownMove] = CapabilityStatus{Enabled: true}
	capabilities[CapabilityShout] = CapabilityStatus{Enabled: true, Reason: "area and party channels only; world channel is unverified"}
	capabilities[CapabilityWorldShout] = CapabilityStatus{Reason: "S4A21 world-shout protocol is not verified"}
	capabilities[CapabilityCleanup] = CapabilityStatus{Reason: "S4A21 character deletion protocol is not integrated"}
	capabilities[CapabilityDungeonMove] = CapabilityStatus{Reason: "dungeon entry workflow is not integrated yet"}
	return capabilities
}

func unavailableCapabilities(reason string) map[BackendCapability]CapabilityStatus {
	capabilities := make(map[BackendCapability]CapabilityStatus)
	for _, operation := range []BackendCapability{
		CapabilityProvision, CapabilityTownMove, CapabilityDungeonMove,
		CapabilityShout, CapabilityWorldShout, CapabilityStore, CapabilityParty, CapabilitySkill,
		CapabilityMarket, CapabilityCleanup,
	} {
		capabilities[operation] = CapabilityStatus{Reason: reason}
	}
	return capabilities
}

func SelectBackend(id BackendID, platform string) (BackendInfo, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	for _, info := range KnownBackends() {
		if info.ID != id {
			continue
		}
		if !info.Selectable {
			if info.Reason == "" {
				info.Reason = "backend is not ready"
			}
			return BackendInfo{}, fmt.Errorf("backend %s is unavailable: %s", id, info.Reason)
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
