package backend

import (
	"fmt"
	"runtime"

	nativebackend "robot/internal/composition/backend/native"
	s4a21backend "robot/internal/composition/backend/s4a21"
	"robot/internal/shared"
)

// Available contains explicitly selectable backends. It does not detect the
// environment or start external services.
func Available() []shared.BackendInfo {
	return []shared.BackendInfo{nativebackend.Info(), s4a21backend.Info()}
}

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
