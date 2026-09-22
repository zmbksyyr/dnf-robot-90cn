package scheduler

import (
	"context"
	"fmt"
	"robot/internal/capability/catalog"
	equipcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/capability/robotspawn"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

func (m *RobotManager) robotName(uid, job, grow int, used map[string]struct{}, rc robotconfig.RuntimeConfig) string {
	return robottemplate.AllocateName(uid, job, grow, used, rc, m.loadNameTemplates(), func(dbName string) bool {
		exists, _ := m.schemaRepo().CharacterNameExists(dbName)
		return exists
	}, m.randBetween)
}

func (m *RobotManager) CreateRobots(req robotcap.CreateRequest) ([]robotcap.Info, error) {
	_, finishOperation, err := m.beginTrackedStructuralOperation("create", fmt.Sprintf("count=%d", req.Count))
	if err != nil {
		return nil, err
	}
	var opErr error
	var robots []robotcap.Info
	defer func() {
		finishOperation(fmt.Sprintf("created=%d", len(robots)), opErr)
	}()
	if m.backendRobotBackend != "" && m.backendRobotBackend != shared.BackendNative {
		if m.backendRobotCreator == nil {
			opErr = shared.UnsupportedCapabilityError{Backend: m.backendRobotBackend, Operation: shared.CapabilityProvision, Reason: "backend robot creator is not configured"}
			return nil, opErr
		}
		robots, err = m.backendRobotCreator.CreateRobots(context.Background(), req)
		opErr = err
		return robots, err
	}
	robots, err = m.lifecycleCreator().Create(req)
	opErr = err
	return robots, err
}

func (m *RobotManager) equipFromCatalog(cid int, level int, job int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	if len(items) == 0 {
		return nil
	}
	raw := equipcap.BuildEquipmentSlots(items, level, job, rc, m.randIntn, m.withRand)
	if equipcap.EquipmentSlotsNeedRepair(raw, equipmentCatalogByID(items), level, job, rc) {
		return fmt.Errorf("generated equipment is incomplete or invalid for cid=%d level=%d job=%d", cid, level, job)
	}
	return m.schemaRepo().SaveEquipmentSlots(cid, raw)
}

func equipmentCatalogByID(items []shared.EquipmentCatalogItem) map[int]shared.EquipmentCatalogItem {
	out := make(map[int]shared.EquipmentCatalogItem, len(items))
	for _, item := range items {
		if item.ID > 0 {
			out[item.ID] = item
		}
	}
	return out
}

func (m *RobotManager) avatarFromCatalog(cid int, level int, job int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	if len(items) == 0 {
		return nil
	}
	selected := equipcap.SelectAvatar(items, job, rc, m.randIntn)
	if rc.MinAvatarSlots > 0 && len(selected) < rc.MinAvatarSlots {
		return nil
	}
	return m.schemaRepo().ReplaceAvatarItems(cid, selected)
}

func (m *RobotManager) petFromCatalog(cid int, rc robotconfig.RuntimeConfig, items []shared.EquipmentCatalogItem) error {
	if !rc.PetEnabled || !petProbabilityHit(rc.PetProbabilityPercent, m.randIntn) {
		return nil
	}
	pet, artifacts, ok := equipcap.SelectPet(items, rc, m.randIntn)
	if !ok {
		robotLogf("[RobotCreate] optional pet skipped cid=%d reason=no_compatible_creature\n", cid)
		return nil
	}
	if rc.PetArtifactEnabled && len(artifacts) < rc.MinPetArtifactSlots {
		robotLogf("[RobotCreate] optional pet artifacts below minimum cid=%d selected=%d minimum=%d\n", cid, len(artifacts), rc.MinPetArtifactSlots)
	}
	if err := m.schemaRepo().ReplacePetItems(cid, pet, artifacts); err != nil {
		robotLogf("[RobotCreate] optional pet write skipped cid=%d pet_id=%d artifacts=%d err=%v\n", cid, pet.ID, len(artifacts), err)
	}
	return nil
}

func petProbabilityHit(percent int, randIntn func(int) int) bool {
	if percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return randIntn != nil && randIntn(100) < percent
}

func (m *RobotManager) loadItemCatalogs() catalog.ItemCatalogView {
	if m.cfg == nil {
		return catalog.ItemCatalogView{}
	}
	return catalog.ViewItemCatalogs(layout.New(m.cfg.ConfigDir).PVF)
}

func (m *RobotManager) applyConfiguredLocation(info *robotcap.Info, rc robotconfig.RuntimeConfig, maps []shared.MapCatalogItem) {
	robotspawn.ApplyConfiguredLocation(spawnEnv{manager: m}, info, rc, maps)
}

func (m *RobotManager) randomMap(maps []shared.MapCatalogItem, level int) (shared.MapCatalogItem, bool) {
	return robotspawn.RandomMap(spawnEnv{manager: m}, maps, level)
}

type spawnEnv struct {
	manager *RobotManager
}

func (e spawnEnv) FollowAccountVillage(account string) (int, bool, error) {
	return e.manager.schemaRepo().FollowAccountVillageLastPlayed(account)
}

func (e spawnEnv) RandBetween(min, max int) int {
	return e.manager.randBetween(min, max)
}

func (e spawnEnv) RandIntn(n int) int {
	return e.manager.randIntn(n)
}
