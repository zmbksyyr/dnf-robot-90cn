package cn90

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

func TestRobotCreatorBuildsSixHundredRobotsOneAtATime(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
			NameASCIIFallback: true, NameASCIIPrefix: "batch",
		},
		Names: robottemplate.NameTemplates{}, IDStart: 18000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 600})
	if err != nil || len(robots) != 600 {
		t.Fatalf("robots=%d err=%v", len(robots), err)
	}
	seenNames := make(map[string]struct{}, len(robots))
	for index, robot := range robots {
		if robot.UID != 18000000+index {
			t.Fatalf("robot[%d] uid=%d", index, robot.UID)
		}
		if _, exists := seenNames[robot.Name]; exists {
			t.Fatalf("duplicate name %q", robot.Name)
		}
		seenNames[robot.Name] = struct{}{}
	}
	identities, err := store.Identities(context.Background(), BackendID)
	if err != nil || len(identities) != 600 {
		t.Fatalf("identities=%d err=%v", len(identities), err)
	}
}

type creatorProvisioner struct{}

func (creatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	results := make([]shared.ProvisionCharacterResult, len(requests))
	for i, request := range requests {
		results[i] = shared.ProvisionCharacterResult{Backend: BackendID, CharacterName: request.CharacterName, Created: true, RobotUID: request.RobotUID}
	}
	return results, nil
}

type concurrentCreatorProvisioner struct {
	mu   lockhub.Locker
	uids map[int]struct{}
}

func (p *concurrentCreatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	request := requests[0]
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.uids[request.RobotUID]; exists {
		return nil, fmt.Errorf("duplicate provision uid %d", request.RobotUID)
	}
	p.uids[request.RobotUID] = struct{}{}
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: request.CharacterName, Created: true, RobotUID: request.RobotUID,
	}}, nil
}

func TestRobotCreatorSerializesConcurrentIdentityAllocation(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	provisioner := &concurrentCreatorProvisioner{uids: make(map[int]struct{})}
	creator := RobotCreator{
		Provisioner: provisioner, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			RobotUIDEnd: 17000019, LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names:   robottemplate.NameTemplates{Common: []string{"Alpha", "Beta", "Gamma", "Delta"}},
		IDStart: 17000000, AccountPrefix: "robot",
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 5})
			if err == nil && len(robots) != 5 {
				err = fmt.Errorf("created %d robots, want 5", len(robots))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	robots, err := store.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 100})
	if err != nil || len(robots) != 20 {
		t.Fatalf("robots=%d err=%v", len(robots), err)
	}
}

type profiledCreatorProvisioner struct{}

func (profiledCreatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	request := requests[0]
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: request.CharacterName, Created: true, RobotUID: request.RobotUID,
		ProfileKnown: true, Job: request.Job, Grow: 0, Level: 1,
	}}, nil
}

func TestRobotCreatorRegistersBackendReportedProfile(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	creator := RobotCreator{
		Provisioner: profiledCreatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 70, LevelMax: 70, Jobs: []int{1}, GrowTypes: []int{2},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}}, IDStart: 17000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	if robots[0].Level != 1 || robots[0].Job != 1 || robots[0].Grow != 0 {
		t.Fatalf("registered planned profile instead of backend profile: %+v", robots[0])
	}
}

type growthRecordingInitializer struct {
	level int
	grow  int
}

func (g *growthRecordingInitializer) ResolveCharacterProfile(_ context.Context, _ string, info robotcap.Info) (robotcap.Info, error) {
	return info, nil
}

func (g *growthRecordingInitializer) InitializeCharacter(_ context.Context, _ string, info robotcap.Info, level, grow int) (robotcap.Info, error) {
	g.level, g.grow = level, grow
	info.Level, info.Grow = level, grow
	return info, nil
}

func (g *growthRecordingInitializer) ApplyCharacterLoadout(context.Context, string, robotcap.Info) error {
	return nil
}

func TestRobotCreatorPassesPlannedGrowthToInitializer(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	initializer := &growthRecordingInitializer{}
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 60, LevelMax: 60, Jobs: []int{1}, GrowTypes: []int{1},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
			NameASCIIFallback: true, NameASCIIPrefix: "growth",
		},
		Names: robottemplate.NameTemplates{}, IDStart: 17000000, AccountPrefix: "robot",
		JobGrows: map[int][]int{1: {3}}, Profiles: initializer, Loadouts: initializer,
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	// first grow 3 (branch) + second grow 1 (awakening) = 0x13.
	if initializer.level != 60 || initializer.grow != 0x13 {
		t.Fatalf("initializer level=%d grow=0x%02X", initializer.level, initializer.grow)
	}
	if robots[0].Level != 60 || robots[0].Grow != 0x13 {
		t.Fatalf("registered robot=%+v", robots[0])
	}
}

// reusableCreatorProvisioner simulates the game server keeping the character
// a previous create attempt already provisioned.
type reusableCreatorProvisioner struct {
	mu    lockhub.Locker
	names map[string]string
}

func (p *reusableCreatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	request := requests[0]
	name, reused := p.names[request.AccountName]
	if !reused {
		name = request.CharacterName
		p.names[request.AccountName] = name
	}
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: name, Created: true, Reused: reused, RobotUID: request.RobotUID,
		ProfileKnown: true, Job: request.Job, Grow: 0, Level: 1,
	}}, nil
}

type failingOnceInitializer struct {
	fail bool
}

func (i *failingOnceInitializer) ResolveCharacterProfile(_ context.Context, _ string, info robotcap.Info) (robotcap.Info, error) {
	return info, nil
}

func (i *failingOnceInitializer) InitializeCharacter(_ context.Context, _ string, info robotcap.Info, level, grow int) (robotcap.Info, error) {
	if i.fail {
		i.fail = false
		return info, errors.New("post-provision initialization failure")
	}
	info.Level, info.Grow = level, grow
	return info, nil
}

func (i *failingOnceInitializer) ApplyCharacterLoadout(context.Context, string, robotcap.Info) error {
	return nil
}

func TestRobotCreatorRecoversAfterPostProvisionFailure(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	initializer := &failingOnceInitializer{fail: true}
	creator := RobotCreator{
		Provisioner: &reusableCreatorProvisioner{names: map[string]string{}}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
			NameASCIIFallback: true, NameASCIIPrefix: "recover",
		},
		Names: robottemplate.NameTemplates{}, IDStart: 17000000, AccountPrefix: "robot",
		Profiles: initializer, Loadouts: initializer,
	}
	if _, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1}); err == nil {
		t.Fatal("first create unexpectedly succeeded")
	}
	// The failed attempt provisioned a server character. The retry must adopt
	// the same character instead of failing with a duplicate identity.
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 {
		t.Fatalf("recovery create robots=%d err=%v", len(robots), err)
	}
	identities, err := store.Identities(context.Background(), BackendID)
	if err != nil || len(identities) != 1 || identities[0].Account != "robot17000000" {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
}

type reusedCreatorProvisioner struct{}

func (reusedCreatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	request := requests[0]
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: "existing-character", Created: true, Reused: true, RobotUID: request.RobotUID,
		ProfileKnown: true, Job: 3, Grow: 2, Level: 63,
	}}, nil
}

type recordingCharacterInitializer struct {
	profileCalls int
	loadoutCalls int
}

type failingLoadoutInitializer struct{}

func (failingLoadoutInitializer) ApplyCharacterLoadout(context.Context, string, robotcap.Info) error {
	return errors.New("test loadout failure")
}

func TestRobotCreatorDoesNotRegisterPartiallyInitializedCharacter(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}}, IDStart: 17000000, AccountPrefix: "robot",
		Loadouts: failingLoadoutInitializer{},
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err == nil || len(robots) != 0 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	registered, selectErr := store.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if selectErr != nil || len(registered) != 0 {
		t.Fatalf("partially initialized robots=%+v err=%v", registered, selectErr)
	}
}

func (a *recordingCharacterInitializer) ResolveCharacterProfile(_ context.Context, _ string, info robotcap.Info) (robotcap.Info, error) {
	a.profileCalls++
	return info, nil
}

func (a *recordingCharacterInitializer) ApplyPlannedCharacterLevel(_ context.Context, _ string, info robotcap.Info, _ int) (robotcap.Info, error) {
	a.profileCalls++
	return info, nil
}

func (a *recordingCharacterInitializer) ApplyCharacterLoadout(_ context.Context, _ string, _ robotcap.Info) error {
	a.loadoutCalls++
	return nil
}

func TestRobotCreatorPreservesReusedProfileAndReconcilesLoadout(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	initializer := &recordingCharacterInitializer{}
	creator := RobotCreator{
		Provisioner: reusedCreatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 70, LevelMax: 70, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names: robottemplate.NameTemplates{Common: []string{"new-template"}}, IDStart: 17000000, AccountPrefix: "robot",
		Profiles: initializer, Loadouts: initializer,
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	if robots[0].Name != "existing-character" || robots[0].Job != 3 || robots[0].Grow != 2 || robots[0].Level != 63 {
		t.Fatalf("reused profile was replaced: %+v", robots[0])
	}
	if initializer.profileCalls != 0 || initializer.loadoutCalls != 1 {
		t.Fatalf("reused character initialization: profile=%d loadout=%d", initializer.profileCalls, initializer.loadoutCalls)
	}
	identities, err := store.Identities(context.Background(), BackendID)
	if err != nil || len(identities) != 1 || identities[0].Account != "robot17000000" || identities[0].CharacterName != "existing-character" {
		t.Fatalf("recovered identities=%+v err=%v", identities, err)
	}
}

type conflictThenCreateProvisioner struct {
	calls int
}

func (p *conflictThenCreateProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	p.calls++
	request := requests[0]
	if p.calls == 1 {
		return []shared.ProvisionCharacterResult{{Backend: BackendID, RobotUID: request.RobotUID}}, &AccountRosterConflictError{Account: request.AccountName, Count: 2}
	}
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: request.CharacterName, Created: true, RobotUID: request.RobotUID,
	}}, nil
}

func TestRobotCreatorSkipsAmbiguousAccountAndContinuesWithNextUID(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	provisioner := &conflictThenCreateProvisioner{}
	creator := RobotCreator{
		Provisioner: provisioner, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha", "Beta"}}, IDStart: 17000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	if robots[0].UID != 17000001 || provisioner.calls != 2 {
		t.Fatalf("conflicting account was not skipped: robots=%+v calls=%d", robots, provisioner.calls)
	}
}

type failingCreatorProvisioner struct {
	calls int
}

func (p *failingCreatorProvisioner) ProvisionCharacters(_ context.Context, requests []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	p.calls++
	if p.calls == 2 {
		return nil, errors.New("test provision failure")
	}
	request := requests[0]
	return []shared.ProvisionCharacterResult{{
		Backend: BackendID, CharacterName: request.CharacterName,
		Created: true, RobotUID: request.RobotUID,
	}}, nil
}

func TestRobotCreatorKeepsCharactersCompletedBeforeFailure(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	provisioner := &failingCreatorProvisioner{}
	creator := RobotCreator{
		Provisioner: provisioner, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names:   robottemplate.NameTemplates{Common: []string{"Alpha", "Beta", "Gamma"}},
		IDStart: 17000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 3})
	if err == nil || len(robots) != 1 || robots[0].UID != 17000000 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
	registered, selectErr := store.SelectRobots(context.Background(), robotcap.CommandRequest{Count: 10})
	if selectErr != nil || len(registered) != 1 || registered[0].UID != robots[0].UID {
		t.Fatalf("registered=%+v err=%v", registered, selectErr)
	}
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
	identities, err := store.Identities(context.Background(), BackendID)
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

func TestRobotCreatorReusesFirstAvailableUIDGap(t *testing.T) {
	store := robotstate.NewMemoryStore([]robotcap.Info{
		{UID: 17000000, Name: "ExistingA"},
		{UID: 17000002, Name: "ExistingC"},
	})
	creator := RobotCreator{
		Provisioner: creatorProvisioner{}, BatchStore: store, IdentityStore: store, RobotCatalog: store,
		Config: robotconfig.RuntimeConfig{
			RobotUIDEnd: 17000003, LevelMin: 50, LevelMax: 50, Jobs: []int{1}, GrowTypes: []int{0},
			SpawnFallbackVillage: 1, SpawnArea: 1, SpawnXMin: 100, SpawnXMax: 100, SpawnYMin: 200, SpawnYMax: 200,
		},
		Names: robottemplate.NameTemplates{Common: []string{"Alpha"}}, IDStart: 17000000, AccountPrefix: "robot",
	}
	robots, err := creator.CreateRobots(context.Background(), robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 || robots[0].UID != 17000001 {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
}
