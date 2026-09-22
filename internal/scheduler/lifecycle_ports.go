package scheduler

import (
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	lifecyclecap "robot/internal/capability/robotlifecycle"
	storecap "robot/internal/capability/store"
	"robot/internal/shared"
	"time"
)

func (m *RobotManager) lifecycleCreator() lifecyclecap.Creator {
	return lifecyclecap.Creator{Env: lifecycleCreateEnv{manager: m}}
}

type lifecycleCreateEnv struct {
	manager *RobotManager
}

func (e lifecycleCreateEnv) AllocateRobotIDs(count, uidStart, uidEnd int) (lifecyclecap.RobotIDAllocation, error) {
	return e.manager.schemaRepo().AllocateRobotIDs(count, uidStart, uidEnd)
}

func (e lifecycleCreateEnv) AvatarFromCatalog(cid int, level int, job int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	return e.manager.avatarFromCatalog(cid, level, job, rc, items)
}

func (e lifecycleCreateEnv) ApplyConfiguredLocation(info *robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) {
	e.manager.applyConfiguredLocation(info, rc, maps)
}

func (e lifecycleCreateEnv) Config() robotconfig.RuntimeConfig {
	return e.manager.loadRobotConfig()
}

func (e lifecycleCreateEnv) CopyTemplateDefaults(cid int) error {
	return e.manager.schemaRepo().CopyTemplateDefaults(cid)
}

func (e lifecycleCreateEnv) CreateBaseCharacter(info robotcap.Info, rc robotconfig.RuntimeConfig) error {
	return e.manager.schemaRepo().CreateBaseCharacter(info, rc)
}

func (e lifecycleCreateEnv) EnsureAccount(uid int, innerIP string) error {
	e.manager.invalidateLoginRepairs([]int{uid})
	return e.manager.schemaRepo().EnsureAccount(uid, innerIP)
}

func (e lifecycleCreateEnv) EnsureWorldHornByCID(cid int) error {
	return e.manager.storePreparer().EnsureWorldHornByCID(cid)
}

func (e lifecycleCreateEnv) EnsureSchema() error {
	return e.manager.repo().EnsureSchema()
}

func (e lifecycleCreateEnv) RecoverIncompleteCreateBatches() error {
	return e.manager.schemaRepo().RecoverIncompleteCreateBatches()
}

func (e lifecycleCreateEnv) BeginCreateBatch(batchID string, uids, cids []int) error {
	return e.manager.schemaRepo().BeginCreateBatch(batchID, uids, cids)
}

func (e lifecycleCreateEnv) CompleteCreateBatch(batchID string) error {
	return e.manager.schemaRepo().CompleteCreateBatch(batchID)
}

func (e lifecycleCreateEnv) RollbackCreateBatch(batchID string) error {
	return e.manager.schemaRepo().RollbackCreateBatch(batchID)
}

func (e lifecycleCreateEnv) EquipFromCatalog(cid int, level int, job int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	return e.manager.equipFromCatalog(cid, level, job, rc, items)
}

func (e lifecycleCreateEnv) LoadCreateCatalogs() lifecyclecap.CreateCatalogs {
	snapshot := e.manager.loadItemCatalogs()
	return lifecyclecap.CreateCatalogs{Equipment: snapshot.Equipment}
}

func (e lifecycleCreateEnv) LoadMapCatalog() []shared.MapCatalogItem {
	return storecap.FilterNormalMaps(e.manager.loadMapCatalog())
}

func (e lifecycleCreateEnv) PetFromCatalog(cid int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	return e.manager.petFromCatalog(cid, rc, items)
}

func (e lifecycleCreateEnv) RobotLocations() ([]shared.MapLocation, error) {
	return e.manager.robotLocations()
}

func (e lifecycleCreateEnv) PrepareRobotUIDRange(uidStart, uidEnd, uidGuard int) error {
	return e.manager.schemaRepo().PrepareRobotUIDRange(uidStart, uidEnd, uidGuard)
}

func (e lifecycleCreateEnv) RebuildCharacView(uid int) error {
	return e.manager.schemaRepo().RebuildCharacView(uid)
}

func (e lifecycleCreateEnv) RegisterRobot(info robotcap.Info) error {
	return e.manager.schemaRepo().RegisterRobot(info)
}

func (e lifecycleCreateEnv) RandomFrom(vals []int) int {
	return e.manager.randomFrom(vals)
}

func (e lifecycleCreateEnv) RandomMap(maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool) {
	return e.manager.randomMap(maps, level)
}

func (e lifecycleCreateEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e lifecycleCreateEnv) RobotGamePort() int {
	if e.manager.cfg == nil {
		return 0
	}
	return e.manager.cfg.RobotGamePort
}

func (e lifecycleCreateEnv) RobotInnerIP() string {
	if e.manager.cfg == nil {
		return ""
	}
	return e.manager.cfg.RobotInnerIP
}

func (e lifecycleCreateEnv) RobotName(uid, job, grow int, used map[string]struct{}, rc robotconfig.RuntimeConfig) string {
	return e.manager.robotName(uid, job, grow, used, rc)
}

func (e lifecycleCreateEnv) UpsertDummy(info robotcap.Info, innerIP string) error {
	return e.manager.schemaRepo().UpsertDummy(info, innerIP)
}

func (m *RobotManager) lifecycleCleaner(req robotcap.CleanupRequest) lifecyclecap.Cleaner {
	return lifecyclecap.Cleaner{Env: lifecycleCleanupEnv{manager: m, request: req}}
}

type lifecycleCleanupEnv struct {
	manager *RobotManager
	request robotcap.CleanupRequest
}

func (e lifecycleCleanupEnv) BatchDeleteRobotData(uids, cids []int) error {
	if err := e.manager.schemaRepo().BatchDeleteRobotData(uids, cids); err != nil {
		return err
	}
	for _, cid := range cids {
		e.manager.worldHornCache.Invalidate(cid)
	}
	e.manager.invalidateLoginRepairs(uids)
	return nil
}

func (e lifecycleCleanupEnv) BatchDeleteRobotMetadata(uids []int) error {
	if err := e.manager.schemaRepo().BatchDeleteRobotMetadata(uids); err != nil {
		return err
	}
	e.manager.invalidateLoginRepairs(uids)
	return nil
}

func (e lifecycleCleanupEnv) CleanupCandidates(req robotcap.CleanupRequest) ([]robotcap.CleanupCandidate, error) {
	return e.manager.schemaRepo().CleanupCandidates(req)
}

func (e lifecycleCleanupEnv) EnsureSchema() error {
	return e.manager.repo().EnsureSchema()
}

func (e lifecycleCleanupEnv) PrepareDelete(uids []int) func() {
	e.manager.markCleanupPending(uids)
	if registry := e.manager.currentActorRegistry(); registry != nil {
		registry.StopUIDs(uids, true)
		e.manager.waitCleanupQuiescence(uids, 30*time.Second)
	} else {
		_, _ = e.manager.sessionService().Logout(robotcap.CommandRequest{UIDs: uids})
		if !e.request.InternalConfirmedBroken {
			time.Sleep(5 * time.Second)
		}
	}
	return func() {
		e.manager.clearCleanupPending(uids)
	}
}
