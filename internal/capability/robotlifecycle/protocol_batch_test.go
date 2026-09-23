package robotlifecycle

import (
	"context"
	"errors"
	"testing"

	robotstate "robot/internal/capability/robotstate"
	"robot/internal/shared"
)

type testBatchProvisioner struct {
	results []shared.ProvisionCharacterResult
	err     error
}

func (p testBatchProvisioner) ProvisionCharacters(context.Context, []shared.ProvisionCharacterRequest) ([]shared.ProvisionCharacterResult, error) {
	return p.results, p.err
}

func TestProvisionProtocolBatchCompletesAndRegistersIdentities(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	slot := uint16(2)
	result, err := ProvisionProtocolBatch(context.Background(), store, store, testBatchProvisioner{results: []shared.ProvisionCharacterResult{{Backend: shared.BackendS4A21, CharacterName: "robot", Created: true, BackendSlot: &slot}}}, "batch-1", shared.BackendS4A21, []shared.ProvisionCharacterRequest{{AccountName: "acct", CharacterName: "robot"}})
	if err != nil || len(result.Results) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	identities, err := store.Identities(context.Background(), shared.BackendS4A21)
	if err != nil || len(identities) != 1 || identities[0].Slot == nil || *identities[0].Slot != slot {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
}

func TestProvisionProtocolBatchRegistersFallbackNameByRobotUID(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	result, err := ProvisionProtocolBatch(context.Background(), store, store, testBatchProvisioner{results: []shared.ProvisionCharacterResult{{Backend: shared.BackendS4A21, CharacterName: "rb42", RobotUID: 42, Created: true}}}, "batch-fallback", shared.BackendS4A21, []shared.ProvisionCharacterRequest{{AccountName: "robot42", CharacterName: "invalid-template", RobotUID: 42}})
	if err != nil || len(result.Results) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	identities, err := store.Identities(context.Background(), shared.BackendS4A21)
	if err != nil || len(identities) != 1 || identities[0].Account != "robot42" || identities[0].CharacterName != "rb42" {
		t.Fatalf("identities=%+v err=%v", identities, err)
	}
}

func TestProvisionProtocolBatchRollsBackPartialFailure(t *testing.T) {
	store := robotstate.NewMemoryStore(nil)
	result, err := ProvisionProtocolBatch(context.Background(), store, store, testBatchProvisioner{results: nil, err: errors.New("connection lost")}, "batch-2", shared.BackendS4A21, []shared.ProvisionCharacterRequest{{AccountName: "acct", CharacterName: "robot"}})
	if err == nil || len(result.Results) != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	recovered, err := store.RecoverIncompleteCreateBatches(context.Background())
	if err != nil || len(recovered) != 0 {
		t.Fatalf("running batches after rollback=%+v err=%v", recovered, err)
	}
}
