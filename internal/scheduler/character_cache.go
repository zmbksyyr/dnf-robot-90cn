package scheduler

import (
	"fmt"
	"time"
)

func (m *RobotManager) invalidateCharacterCache(uid int) error {
	if m == nil || uid <= 0 {
		return fmt.Errorf("invalid cache uid %d", uid)
	}
	if m.characterCacheInvalidate != nil {
		started := time.Now()
		if err := m.characterCacheInvalidate(uid); err != nil {
			return err
		}
		robotLogf("[CharacterCache] uid=%d adapter_nocache_sent=1 elapsed_ms=%d\n", uid, time.Since(started).Milliseconds())
		return nil
	}
	return fmt.Errorf("character cache invalidator is not configured")
}

func (m *RobotManager) invalidateClosedCharacterCache(uid int) error {
	if err := m.invalidateCharacterCache(uid); err != nil {
		return err
	}
	m.clearSessionLogout(uid)
	return nil
}
