package robotlifecycle

import (
	"context"
	"errors"
	"fmt"

	robotstate "robot/internal/capability/robotstate"
	"robot/internal/shared"
)

// ProtocolBatchResult keeps the adapter result separate from the robot-owned
// batch journal. A failed batch may still contain useful partial results for
// diagnostics and retry decisions.
type ProtocolBatchResult struct {
	Results []shared.ProvisionCharacterResult
}

// ProvisionProtocolBatch applies the common batch lifecycle to a backend
// provisioner. The adapter owns protocol details; this function owns only
// robot-state bookkeeping and stable completion semantics.
func ProvisionProtocolBatch(
	ctx context.Context,
	batchStore robotstate.BatchDirectory,
	identityStore robotstate.IdentityDirectory,
	provisioner shared.BatchCharacterProvisioner,
	batchID string,
	backend shared.BackendID,
	requests []shared.ProvisionCharacterRequest,
) (ProtocolBatchResult, error) {
	if batchStore == nil || identityStore == nil || provisioner == nil {
		return ProtocolBatchResult{}, errors.New("protocol batch dependencies are required")
	}
	if len(requests) == 0 {
		return ProtocolBatchResult{}, errors.New("protocol batch requests are empty")
	}
	identities := make([]robotstate.Identity, 0, len(requests))
	for _, request := range requests {
		if request.AccountName == "" || request.CharacterName == "" {
			return ProtocolBatchResult{}, errors.New("protocol batch request has empty account or character name")
		}
		identities = append(identities, robotstate.Identity{Backend: backend, Account: request.AccountName, CharacterName: request.CharacterName})
	}
	if err := batchStore.BeginCreateBatch(ctx, robotstate.CreateBatch{ID: batchID, Backend: backend, Identities: identities}); err != nil {
		return ProtocolBatchResult{}, fmt.Errorf("begin protocol batch: %w", err)
	}
	result, err := provisioner.ProvisionCharacters(ctx, requests)
	createdCount := 0
	for _, provisioned := range result {
		if !provisioned.Created {
			continue
		}
		createdCount++
		identityBackend := provisioned.Backend
		if identityBackend == "" {
			identityBackend = backend
		}
		identity := robotstate.Identity{Backend: identityBackend, CharacterName: provisioned.CharacterName}
		for _, request := range requests {
			if request.CharacterName == provisioned.CharacterName {
				identity.Account = request.AccountName
				break
			}
		}
		if provisioned.BackendSlot != nil {
			identity.Slot = provisioned.BackendSlot
		}
		if identity.Account == "" {
			err = errors.Join(err, fmt.Errorf("provisioned character %q has no matching request", provisioned.CharacterName))
			continue
		}
		if registerErr := identityStore.RegisterIdentity(ctx, identity); registerErr != nil {
			err = errors.Join(err, fmt.Errorf("register provisioned character %q: %w", identity.CharacterName, registerErr))
		}
	}
	if err != nil || len(result) != len(requests) || createdCount != len(requests) {
		if rollbackErr := batchStore.RollbackCreateBatch(ctx, batchID); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
		if err == nil {
			err = fmt.Errorf("protocol batch stopped after %d of %d requests", createdCount, len(requests))
		}
		return ProtocolBatchResult{Results: result}, err
	}
	if err := batchStore.CompleteCreateBatch(ctx, batchID); err != nil {
		return ProtocolBatchResult{Results: result}, fmt.Errorf("complete protocol batch: %w", err)
	}
	return ProtocolBatchResult{Results: result}, nil
}
