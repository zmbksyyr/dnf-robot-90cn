package s4a21

import (
	"context"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/shared"
)

type creatorProvisioner struct{}

func (creatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	results := make([]shared.ProvisionCharacterResult, len(requests))
	for i, request := range requests {
		results[i] = shared.ProvisionCharacterResult{Backend: shared.BackendS4A21, CharacterName: request.CharacterName, Created: true, RobotUID: request.RobotUID}
	}
	return results, nil
}

func TestRobotCreatorBuildsPlansAndRegistersState(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{2}, SpawnFallbackVillage: 1, SpawnArea: 3, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}}, IDStart: 17000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 2})
	if err != nil || len(robots) != 2 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	identities, err := store.Identities(context.Background(), shared.BackendS4A21)
	if err != nil || len(identities) != 2 {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
}

func TestRobotCreatorContinuesAfterExistingSimulatorBatch(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0}, SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200},
		Names:  robottemplate.NameTemplates{Common: []string{"Alpha", "Beta", "Gamma"}}, IDStart: 17000000, AccountPrefix: "robot",
	}
	first, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(second) != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if second[0].UID != 17000002 {
		t.Fatalf("second uid=%d, want 17000002", second[0].UID)
	}
	if second[0].Name == first[0].Name || second[0].Name == first[1].Name {
		t.Fatalf("second name=%q collides with first=%+v", second[0].Name, first)
	}
}
