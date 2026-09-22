package backend

import "robot/internal/shared"

// Available contains explicitly selectable backends. It does not detect the
// environment or start external services.
func Available() []shared.BackendInfo {
	return shared.KnownBackends()
}

func Select(id shared.BackendID, platform string) (shared.BackendInfo, error) {
	return shared.SelectBackend(id, platform)
}
