package backend

import (
	"fmt"
	"runtime"

	cn90backend "robot/internal/composition/backend/cn90"
	"robot/internal/shared"
)

// Available returns the only backend implemented by this build. It does not
// detect the environment or start external services.
func Available() []shared.BackendInfo {
	return []shared.BackendInfo{cn90backend.Info()}
}

func DefaultID() shared.BackendID { return cn90backend.BackendID }

func Select(id shared.BackendID, platform string) (shared.BackendInfo, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	for _, info := range Available() {
		if info.ID != id {
			continue
		}
		if !info.Selectable {
			if info.Reason == "" {
				info.Reason = "backend is not ready"
			}
			return shared.BackendInfo{}, fmt.Errorf("backend %s is unavailable: %s", id, info.Reason)
		}
		for _, supported := range info.SupportedOS {
			if supported == platform {
				return info, nil
			}
		}
		return shared.BackendInfo{}, fmt.Errorf("backend %s does not support %s", id, platform)
	}
	return shared.BackendInfo{}, fmt.Errorf("unknown backend %q", id)
}
