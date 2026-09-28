package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	"robot/internal/shared"
)

type runtimeActionTransport struct {
	manager *RobotManager
}

func (t runtimeActionTransport) MoveTown(_ context.Context, command shared.RuntimeMoveCommand) error {
	return t.manager.doll.Move(command)
}

func (t runtimeActionTransport) ShoutLocal(_ context.Context, command shared.RuntimeShoutCommand) error {
	return t.manager.doll.Shout(command)
}

func (t runtimeActionTransport) PartyActive(uid int) bool {
	return t.manager.doll.PartyActive(uid)
}

func (t runtimeActionTransport) RuntimeStatusMap() map[int]robotcap.RuntimeStatus {
	if provider, ok := t.manager.doll.(runtimeStatusMapProvider); ok {
		status := provider.RuntimeStatusMap()
		if status != nil {
			return status
		}
	}
	status := make(map[int]robotcap.RuntimeStatus)
	for _, item := range t.manager.doll.RuntimeStatus() {
		status[item.UID] = item
	}
	return status
}

type sessionDriver interface {
	EnsureWorldHorn(int) error
	PrepareOnline(robotcap.Info, robotconfig.RuntimeConfig) robotcap.Info
	SendLogout(int) error
	SendOnline([]shared.RuntimeOnlineUser) error
	ForceClose(int) bool
}

type runtimeSessionDriver struct {
	manager *RobotManager
}

func (d runtimeSessionDriver) EnsureWorldHorn(cid int) error {
	return d.manager.storePreparer().EnsureWorldHornByCID(cid)
}

func (runtimeSessionDriver) PrepareOnline(info robotcap.Info, _ robotconfig.RuntimeConfig) robotcap.Info {
	return info
}

func (d runtimeSessionDriver) SendLogout(uid int) error {
	err := d.manager.doll.Logout(uid)
	if err == nil {
		d.manager.markSessionLogout(uid, time.Now())
	}
	return err
}

func (d runtimeSessionDriver) SendOnline(users []shared.RuntimeOnlineUser) error {
	d.manager.waitSessionRelogin(users)
	return d.manager.doll.Online(users)
}

func (d runtimeSessionDriver) ForceClose(uid int) bool {
	closer, ok := d.manager.doll.(interface{ ForceClose(int) bool })
	return ok && closer.ForceClose(uid)
}

type protocolSessionDriver struct {
	manager   *RobotManager
	transport BackendSessionTransport
}

func (protocolSessionDriver) EnsureWorldHorn(int) error { return nil }

func (d protocolSessionDriver) PrepareOnline(info robotcap.Info, rc robotconfig.RuntimeConfig) robotcap.Info {
	maps := d.manager.loadMapCatalog()
	if rc.SpawnFixed || strings.TrimSpace(rc.FollowAccount) != "" {
		d.manager.applyConfiguredLocation(&info, rc, maps)
		return info
	}
	spawnMaps := robotspawn.NormalMaps(maps)
	if len(spawnMaps) == 0 {
		spawnMaps = maps
	}
	if robotspawn.HasUsableMap(spawnMaps, info.Village, info.Area) {
		return info
	}
	// Adopted or freshly created characters can carry the server's default
	// town (1/0) or an event/housing area an older build repaired into. Neither
	// confirms town entry, so pick a capacity-balanced regular town instead.
	locations, err := d.manager.robotLocations()
	if err != nil {
		locations = nil
	}
	if target, ok := robotspawn.BalancedLocation(spawnEnv{manager: d.manager}, spawnMaps, info.Level, locations); ok {
		robotLogf("[SpawnRepair] uid=%d cid=%d level=%d invalid=%d/%d -> %d/%d/%d/%d\n",
			info.UID, info.CID, info.Level, info.Village, info.Area, target.Map.Village, target.Map.Area, target.X, target.Y)
		info.Village, info.Area = target.Map.Village, target.Map.Area
		info.X, info.Y = target.X, target.Y
	}
	return info
}

func (d protocolSessionDriver) SendLogout(uid int) error {
	err := d.transport.Close(uid)
	if err == nil {
		d.manager.markSessionLogout(uid, time.Now())
	}
	return err
}

func (d protocolSessionDriver) SendOnline(users []shared.RuntimeOnlineUser) error {
	rc := d.manager.loadRobotConfig()
	if err := d.manager.populateBackendSessionIdentities(users); err != nil {
		return err
	}
	opened := make([]int, 0, len(users))
	for _, user := range users {
		if user.AccountName == "" {
			d.closeOpened(opened)
			return fmt.Errorf("backend session account is required for uid %d", user.UID)
		}
		if err := d.transport.Open(context.Background(), user.UID, shared.OpenSessionRequest{
			AccountName: user.AccountName, PasswordHash: user.PasswordHash, CharacterSlot: uint16(user.CharacterSlot),
			EnablePartyDungeonFollower: backendPartyFollowerEnabled(d.manager.backendInfo, rc),
			InitialTownKnown:           true,
			InitialVillage:             user.BirthVillage,
			InitialArea:                user.BirthArea,
			InitialX:                   user.BirthX,
			InitialY:                   user.BirthY,
		}); err != nil {
			d.closeOpened(opened)
			return err
		}
		opened = append(opened, user.UID)
	}
	return nil
}

func backendPartyFollowerEnabled(info shared.BackendInfo, rc robotconfig.RuntimeConfig) bool {
	status := info.Capabilities[shared.CapabilityDungeonFollow]
	if !status.Enabled {
		return false
	}
	// Account mode requires the configured operator account; adapters may instead
	// declare auto-accept without requiring an account filter.
	if status.Mode == "account" {
		return strings.TrimSpace(rc.FollowAccount) != ""
	}
	return true
}

func (d protocolSessionDriver) closeOpened(uids []int) {
	for _, uid := range uids {
		_ = d.transport.Close(uid)
	}
}

func (d protocolSessionDriver) ForceClose(uid int) bool {
	return d.transport.Close(uid) == nil
}
