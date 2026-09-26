package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	robotaction "robot/internal/capability/robotaction"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/shared"
)

const (
	sessionWriteSafetyMargin        = 2 * time.Second
	minSessionLogoutCleanupInterval = time.Second
	maxSessionLogoutCleanupInterval = 30 * time.Second
)

func (m *RobotManager) waitAccountOffline(uid int, shouldStop func() bool) (bool, error) {
	if uid <= 0 {
		return false, fmt.Errorf("invalid account uid=%d", uid)
	}
	deadline := time.Now().Add(30 * time.Second)
	offlineStarted := time.Time{}
	for time.Now().Before(deadline) {
		if shouldStop != nil && shouldStop() {
			return true, nil
		}
		online, err := m.accountOnline(uid)
		if err != nil {
			return false, err
		}
		if online {
			offlineStarted = time.Time{}
		} else if offlineStarted.IsZero() {
			offlineStarted = time.Now()
			// Logout performs this invalidation when the runtime disappears within
			// its first confirmation sample. Under load that close can take longer,
			// so complete the same boundary as soon as the account is observably
			// offline. A failed NoCache call keeps the existing timed safety window.
			if m.sessionReleaseRemaining(uid) > 0 {
				if err := m.invalidateClosedCharacterCache(uid); err != nil {
					robotLogf("[CharacterCache] uid=%d offline_nocache_failed fallback_ms=%d err=%v\n",
						uid, m.sessionReleaseRemaining(uid).Milliseconds(), err)
				}
			}
		} else if time.Since(offlineStarted) >= time.Second && m.sessionReleaseRemaining(uid) <= 0 {
			return false, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false, fmt.Errorf("account offline timeout uid=%d", uid)
}

func (m *RobotManager) sessionReleaseRemaining(uid int) time.Duration {
	if m == nil || uid <= 0 {
		return 0
	}
	delay := m.sessionReloginDelay
	if delay <= 0 {
		delay = 15 * time.Second
	}
	// Database writes must happen after the old game-session snapshot is fully
	// released, not exactly on the relogin boundary where a final save can race
	// and overwrite inventory or profession changes.
	delay += sessionWriteSafetyMargin
	m.sessionMu.Lock()
	lastLogout := m.sessionLastLogout[uid]
	if lastLogout.IsZero() {
		m.sessionMu.Unlock()
		return 0
	}
	remaining := delay - time.Since(lastLogout)
	if remaining > 0 {
		m.sessionMu.Unlock()
		return remaining
	}
	delete(m.sessionLastLogout, uid)
	m.sessionMu.Unlock()
	return 0
}

func (m *RobotManager) sessionService() robotaction.SessionService {
	return robotaction.SessionService{Env: sessionActionEnv{manager: m}}
}

type sessionActionEnv struct {
	manager *RobotManager
}

func (e sessionActionEnv) CountRuntimeRunning() int {
	return e.manager.countRuntimeRunning()
}

func (e sessionActionEnv) EnsureWorldHornByCID(cid int) error {
	return e.manager.sessions.EnsureWorldHorn(cid)
}

func (e sessionActionEnv) InvalidateCharacterCache(uid int) error {
	return e.manager.invalidateClosedCharacterCache(uid)
}

func (e sessionActionEnv) RobotConnectIP() string {
	return e.manager.robotConnectIP()
}

func (e sessionActionEnv) RobotInnerIP() string {
	if e.manager == nil || e.manager.cfg == nil {
		return ""
	}
	return strings.TrimSpace(e.manager.cfg.RobotInnerIP)
}

func (e sessionActionEnv) RobotGamePort() int {
	if e.manager == nil || e.manager.cfg == nil {
		return 0
	}
	return e.manager.cfg.RobotGamePort
}

func (e sessionActionEnv) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMap()
}

func (e sessionActionEnv) RuntimeStatusMapFresh() map[int]robotcap.RuntimeStatus {
	return e.manager.runtimeStatusMapFresh()
}

func (e sessionActionEnv) SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	return e.manager.selectRobots(req)
}

func (e sessionActionEnv) PrepareOnlineRobot(info robotcap.Info, rc robotconfig.RuntimeConfig) (robotcap.Info, error) {
	info = e.manager.sessions.PrepareOnline(info, rc)
	if shared.GenericAreaAllowed(info.GuildID, info.Village) {
		return info, nil
	}
	if st, ok := e.manager.runtimeStatus(info.UID); ok && robotcap.ActiveRuntimeStatus(st) {
		return info, fmt.Errorf("cannot relocate active guild member uid=%d from unsafe generic area %d/%d", info.UID, info.Village, info.Area)
	}
	robotLogf("[AreaPolicy] uid=%d cid=%d guild_id=%d relocate_before_online village=%d area=%d\n",
		info.UID, info.CID, info.GuildID, info.Village, info.Area)
	return e.manager.storeMaintenance().RestoreAutoNormalPosition(info, rc, "guild_agit_generic_guard")
}

func (e sessionActionEnv) SendLogout(uid int) error {
	return e.manager.sessions.SendLogout(uid)
}

func (e sessionActionEnv) SendOnline(userinfos []shared.RuntimeOnlineUser) error {
	return e.manager.sessions.SendOnline(userinfos)
}

func (m *RobotManager) populateBackendSessionIdentities(users []shared.RuntimeOnlineUser) error {
	if len(users) == 0 {
		return nil
	}
	directory, ok := m.robotState.(robotstate.IdentityDirectory)
	if !ok {
		return nil
	}
	identities, err := directory.Identities(context.Background(), m.backendInfo.ID)
	if err != nil {
		return err
	}
	byName := make(map[string]robotstate.Identity, len(identities))
	for _, identity := range identities {
		byName[identity.CharacterName] = identity
	}
	uids := make([]int, 0, len(users))
	for _, user := range users {
		if user.AccountName == "" {
			uids = append(uids, user.UID)
		}
	}
	if len(uids) == 0 {
		return nil
	}
	robots, err := m.robotState.SelectRobots(context.Background(), robotcap.CommandRequest{UIDs: uids})
	if err != nil {
		return err
	}
	byUID := make(map[int]robotcap.Info, len(robots))
	for _, robot := range robots {
		byUID[robot.UID] = robot
	}
	for index := range users {
		if users[index].AccountName != "" {
			continue
		}
		robot, exists := byUID[users[index].UID]
		if !exists {
			continue
		}
		identity, exists := byName[robot.Name]
		if !exists {
			continue
		}
		users[index].AccountName = identity.Account
		if identity.Slot != nil {
			users[index].CharacterSlot = int(*identity.Slot)
		}
	}
	return nil
}

func gateAreaForVillage(maps []shared.MapCatalogItem, village int) (int, bool) {
	for _, mp := range maps {
		if mp.Use && mp.Gate && mp.Village == village {
			return mp.Area, true
		}
	}
	return 0, false
}

func (m *RobotManager) markSessionLogout(uid int, at time.Time) {
	if m == nil || uid <= 0 {
		return
	}
	now := time.Now()
	retention := m.sessionLogoutRetention()
	m.sessionMu.Lock()
	if m.sessionLastLogout == nil {
		m.sessionLastLogout = make(map[int]time.Time)
	}
	if m.sessionLogoutCleanupAt.IsZero() || !now.Before(m.sessionLogoutCleanupAt) {
		cutoff := now.Add(-retention)
		for existingUID, lastLogout := range m.sessionLastLogout {
			if !lastLogout.After(cutoff) {
				delete(m.sessionLastLogout, existingUID)
			}
		}
		m.sessionLogoutCleanupAt = now.Add(sessionLogoutCleanupInterval(retention))
	}
	m.sessionLastLogout[uid] = at
	m.sessionMu.Unlock()
}

func (m *RobotManager) sessionLogoutRetention() time.Duration {
	retention := m.sessionReloginDelay + sessionWriteSafetyMargin
	if retention <= sessionWriteSafetyMargin {
		retention = 15*time.Second + sessionWriteSafetyMargin
	}
	return retention
}

func sessionLogoutCleanupInterval(retention time.Duration) time.Duration {
	interval := retention / 2
	if interval < minSessionLogoutCleanupInterval {
		return minSessionLogoutCleanupInterval
	}
	if interval > maxSessionLogoutCleanupInterval {
		return maxSessionLogoutCleanupInterval
	}
	return interval
}

func (m *RobotManager) clearSessionLogout(uid int) {
	if m == nil || uid <= 0 {
		return
	}
	m.sessionMu.Lock()
	delete(m.sessionLastLogout, uid)
	m.sessionMu.Unlock()
}

func (m *RobotManager) waitSessionRelogin(userinfos []shared.RuntimeOnlineUser) {
	if m == nil || len(userinfos) == 0 {
		return
	}
	delay := m.sessionReloginDelay
	if delay <= 0 {
		delay = 15 * time.Second
	}
	for {
		now := time.Now()
		wait := time.Duration(0)
		m.sessionMu.Lock()
		for _, userinfo := range userinfos {
			uid := userinfo.UID
			last := m.sessionLastLogout[uid]
			if uid <= 0 || last.IsZero() {
				continue
			}
			remaining := delay - now.Sub(last)
			if remaining <= 0 {
				delete(m.sessionLastLogout, uid)
				continue
			}
			if remaining > wait {
				wait = remaining
			}
		}
		m.sessionMu.Unlock()
		if wait <= 0 {
			return
		}
		time.Sleep(wait)
	}
}
