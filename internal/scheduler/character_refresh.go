package scheduler

import (
	"fmt"
	"time"

	robotcap "robot/internal/capability/robot"
)

type characterRefreshRuntime interface {
	ReturnToCharacterSelect(uid int) bool
	AtCharacterSelect(uid int) bool
	CharacterSelectRejected(uid int) bool
	ReselectCharacter(uid int) bool
}

// refreshCharacterForWrite enters the game's character-selection
// boundary. A successful command-7 response means df_game has completed the
// outgoing character save and unloaded its live character object, so the
// caller may safely update character-owned database rows without an arbitrary
// logout delay.
func (m *RobotManager) refreshCharacterForWrite(uid int, shouldStop func() bool) (bool, error) {
	runtime, ok := m.doll.(characterRefreshRuntime)
	if !ok {
		return false, fmt.Errorf("runtime does not support character refresh")
	}
	if !runtime.ReturnToCharacterSelect(uid) {
		return false, fmt.Errorf("return to character select send failed uid=%d", uid)
	}
	cancelled := false
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			// Once command 7 has been sent, keep consuming its ACK so callers can
			// always close the refresh cycle by re-selecting the character.
			cancelled = true
		}
		if runtime.CharacterSelectRejected(uid) {
			return false, fmt.Errorf("return to character select rejected uid=%d", uid)
		}
		if runtime.AtCharacterSelect(uid) {
			return cancelled, nil
		}
		if st, exists := m.runtimeStatusMapFresh()[uid]; !exists || st.StateName == robotcap.RuntimeStateStop || st.DisconnectReason != 0 {
			return false, fmt.Errorf("runtime stopped before character select uid=%d", uid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, fmt.Errorf("return to character select timeout uid=%d", uid)
}

func (m *RobotManager) resumeCharacterAfterWrite(uid int, shouldStop func() bool) (bool, error) {
	runtime, ok := m.doll.(characterRefreshRuntime)
	if !ok {
		return false, fmt.Errorf("runtime does not support character refresh")
	}
	if !runtime.ReselectCharacter(uid) {
		return false, fmt.Errorf("character reselect send failed uid=%d", uid)
	}
	return m.waitForCharacterRunning(uid, shouldStop, "character reselect")
}

// waitCharacterRunning is the stronger post-login barrier used by store
// preparation. Session confirmation accepts any active socket state (including
// init), while store packets are valid only after town entry reaches running.
func (m *RobotManager) waitCharacterRunning(uid int, shouldStop func() bool) (bool, error) {
	return m.waitForCharacterRunning(uid, shouldStop, "full login")
}

func (m *RobotManager) waitForCharacterRunning(uid int, shouldStop func() bool, phase string) (bool, error) {
	cancelled := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			// Entry is already in flight. Reach a stable state before reporting the
			// cancellation so cleanup can use the normal save/logout boundary.
			cancelled = true
		}
		st, exists := m.runtimeStatusMapFresh()[uid]
		if exists && st.StateName == robotcap.RuntimeStateRunning && st.DisconnectReason == 0 {
			return cancelled, nil
		}
		if !exists || st.StateName == robotcap.RuntimeStateStop || st.DisconnectReason != 0 {
			return false, fmt.Errorf("runtime stopped during %s uid=%d", phase, uid)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false, fmt.Errorf("%s timeout uid=%d", phase, uid)
}

// offlineCharacterForWrite combines the character-save barrier with a full
// account logout. CMD 7 guarantees that character-owned data has been saved
// and unloaded; the following logout releases account-level caches such as
// IsPermissionPrivateStore before database preparation begins.
func (m *RobotManager) offlineCharacterForWrite(uid int, shouldStop func() bool) (bool, error) {
	st, exists := m.runtimeStatusMapFresh()[uid]
	cancelled := false
	if exists {
		switch st.StateName {
		case robotcap.RuntimeStateRunning:
			var err error
			cancelled, err = m.refreshCharacterForWrite(uid, shouldStop)
			if err != nil {
				return false, err
			}
		case robotcap.RuntimeStateSelect:
			// The character-save barrier was already completed by this session.
		case robotcap.RuntimeStateStop:
			exists = false
		default:
			// Init/login/connecting states do not yet own a running character that
			// needs CMD 7. Full Logout still releases their account-level cache.
		}
	}
	if exists {
		result, err := m.sessionService().Logout(robotcap.CommandRequest{UIDs: []int{uid}})
		if err != nil || result.Accepted != 1 {
			return false, fmt.Errorf("account logout after character save failed uid=%d accepted=%d confirmed=%d err=%v", uid, result.Accepted, result.Confirmed, err)
		}
	}
	// Once Logout has been accepted, do not let scheduler cancellation bypass
	// the account-cache release barrier. Callers may cancel the following work,
	// but database writes and cleanup must never run against a half-open session.
	offlineCancelled, err := m.waitAccountOffline(uid, nil)
	if err != nil {
		return false, err
	}
	return cancelled || offlineCancelled, nil
}
