package backend

import (
	"fmt"
	"runtime"

	"robot/internal/shared"
)

// Available contains explicitly selectable backends. It does not detect the
// environment or start external services.
func Available() []shared.BackendInfo {
	return shared.KnownBackends()
}

func Select(id shared.BackendID, platform string) (shared.BackendInfo, error) {
	if platform == "" {
		platform = runtime.GOOS
	}
	for _, info := range Available() {
		if info.ID != id {
			continue
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
