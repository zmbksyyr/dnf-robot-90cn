package scheduler

import (
	"fmt"
	"time"
)

func (m *RobotManager) invalidateCharacterCache(uid int) error {
	_, err := m.invalidateCharacterCacheResult(uid)
	return err
}

// invalidateCharacterCacheResult also reports whether the adapter positively
// released the server-side character snapshot. The session-close boundary
// cannot confirm that, so callers keep the timed relogin window before offline
// database writes.
func (m *RobotManager) invalidateCharacterCacheResult(uid int) (confirmed bool, err error) {
	if m == nil || uid <= 0 {
		return false, fmt.Errorf("invalid cache uid %d", uid)
	}
	if m.characterCacheInvalidate != nil {
		started := time.Now()
		if err := m.characterCacheInvalidate(uid); err != nil {
			return false, err
		}
		robotLogf("[CharacterCache] uid=%d adapter_nocache_sent=1 elapsed_ms=%d\n", uid, time.Since(started).Milliseconds())
		return true, nil
	}
	if boundary, ok := m.sessions.(interface{ CharacterCacheBoundaryIsSessionClose() bool }); ok &&
		boundary.CharacterCacheBoundaryIsSessionClose() {
		robotLogf("[CharacterCache] uid=%d session_close_boundary=1\n", uid)
		return false, nil
	}
	return false, fmt.Errorf("character cache invalidator is not configured")
}

func (m *RobotManager) invalidateClosedCharacterCache(uid int) error {
	confirmed, err := m.invalidateCharacterCacheResult(uid)
	if err != nil {
		return err
	}
	if confirmed {
		// Only a positively confirmed release may end the relogin window.
		m.clearSessionLogout(uid)
	}
	return nil
}
