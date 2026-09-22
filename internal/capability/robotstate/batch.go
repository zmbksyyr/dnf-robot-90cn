package robotstate

import (
	"context"
	"errors"
	"strings"
	"time"

	"robot/internal/shared"
)

type BatchStatus string

const (
	BatchRunning    BatchStatus = "running"
	BatchComplete   BatchStatus = "complete"
	BatchRolledBack BatchStatus = "rolled_back"
)

var ErrDuplicateBatch = errors.New("robot creation batch already exists")

type CreateBatch struct {
	ID         string
	Backend    shared.BackendID
	Status     BatchStatus
	Identities []Identity
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// BatchDirectory tracks robot-owned provisioning progress only. Rollback is
// a state transition; backend-specific deletion remains the adapter's job.
type BatchDirectory interface {
	BeginCreateBatch(context.Context, CreateBatch) error
	CompleteCreateBatch(context.Context, string) error
	RollbackCreateBatch(context.Context, string) error
	RecoverIncompleteCreateBatches(context.Context) ([]CreateBatch, error)
}

func (s *MemoryStore) BeginCreateBatch(ctx context.Context, batch CreateBatch) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(batch.ID) == "" || batch.Backend == "" {
		return errors.New("batch id and backend are required")
	}
	now := time.Now().UTC()
	batch.Status, batch.CreatedAt, batch.UpdatedAt = BatchRunning, now, now
	batch.Identities = append([]Identity(nil), batch.Identities...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.batches[batch.ID]; exists {
		return ErrDuplicateBatch
	}
	s.batches[batch.ID] = batch
	return nil
}

func (s *MemoryStore) CompleteCreateBatch(ctx context.Context, id string) error {
	return s.updateBatchStatus(ctx, id, BatchComplete)
}

func (s *MemoryStore) RollbackCreateBatch(ctx context.Context, id string) error {
	return s.updateBatchStatus(ctx, id, BatchRolledBack)
}

func (s *MemoryStore) RecoverIncompleteCreateBatches(ctx context.Context) ([]CreateBatch, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	result := make([]CreateBatch, 0)
	for id, batch := range s.batches {
		if batch.Status != BatchRunning {
			continue
		}
		batch.Status, batch.UpdatedAt = BatchRolledBack, now
		s.batches[id] = batch
		result = append(result, cloneBatch(batch))
	}
	return result, nil
}

func (s *MemoryStore) updateBatchStatus(ctx context.Context, id string, status BatchStatus) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[id]
	if !ok {
		return ErrNotFound
	}
	if batch.Status != BatchRunning {
		return errors.New("robot creation batch is not running")
	}
	batch.Status, batch.UpdatedAt = status, time.Now().UTC()
	s.batches[id] = batch
	return nil
}

func cloneBatch(batch CreateBatch) CreateBatch {
	batch.Identities = append([]Identity(nil), batch.Identities...)
	return batch
}

var _ BatchDirectory = (*MemoryStore)(nil)
