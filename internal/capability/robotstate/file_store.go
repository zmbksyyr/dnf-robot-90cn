package robotstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/atomicfile"
	"robot/internal/shared"
)

// FileStore persists only robot-owned state. It never opens or inspects a game
// server database and can therefore be used by simulated backends.
type FileStore struct {
	*MemoryStore
	path string
}

type fileSnapshot struct {
	Robots     []robotcap.Info            `json:"robots"`
	Locations  map[int]shared.MapLocation `json:"locations"`
	Identities []Identity                 `json:"identities"`
	Batches    []CreateBatch              `json:"batches"`
}

func OpenFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, errors.New("robot state path is required")
	}
	store := &FileStore{MemoryStore: NewMemoryStore(nil), path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshot fileSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	if snapshot.Locations == nil {
		snapshot.Locations = make(map[int]shared.MapLocation)
	}
	store.mu.Lock()
	for _, robot := range snapshot.Robots {
		store.robots[robot.UID] = robot
		if _, ok := snapshot.Locations[robot.UID]; !ok {
			snapshot.Locations[robot.UID] = shared.MapLocation{Village: robot.Village, Area: robot.Area, X: robot.X, Y: robot.Y}
		}
	}
	for uid, location := range snapshot.Locations {
		store.locations[uid] = location
	}
	for _, identity := range snapshot.Identities {
		store.identities[identityKey(identity)] = identity
	}
	for _, batch := range snapshot.Batches {
		store.batches[batch.ID] = cloneBatch(batch)
	}
	store.mu.Unlock()
	return store, nil
}

func (s *FileStore) persist() error {
	s.mu.RLock()
	snapshot := fileSnapshot{
		Robots: make([]robotcap.Info, 0, len(s.robots)), Locations: make(map[int]shared.MapLocation, len(s.locations)),
		Identities: make([]Identity, 0, len(s.identities)), Batches: make([]CreateBatch, 0, len(s.batches)),
	}
	for _, robot := range s.robots {
		snapshot.Robots = append(snapshot.Robots, robot)
	}
	for uid, location := range s.locations {
		snapshot.Locations[uid] = location
	}
	for _, identity := range s.identities {
		snapshot.Identities = append(snapshot.Identities, identity)
	}
	for _, batch := range s.batches {
		snapshot.Batches = append(snapshot.Batches, cloneBatch(batch))
	}
	s.mu.RUnlock()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(s.path, data, 0600)
}

// Flush publishes the current Robot-owned snapshot. Mutating operations are
// already persisted individually; the explicit barrier makes shutdown order
// visible to callers and remains correct if batching is introduced later.
func (s *FileStore) Flush() error {
	if s == nil {
		return errors.New("nil robot state store")
	}
	return s.persist()
}

func (s *FileStore) UpdateRobotPositions(ctx context.Context, updates []robotcap.PositionUpdate) error {
	if err := s.MemoryStore.UpdateRobotPositions(ctx, updates); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) RegisterRobots(ctx context.Context, robots []robotcap.Info) error {
	if err := s.MemoryStore.RegisterRobots(ctx, robots); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) UpdateRobotProfiles(ctx context.Context, robots []robotcap.Info) error {
	if err := s.MemoryStore.UpdateRobotProfiles(ctx, robots); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) RemoveRobots(ctx context.Context, uids []int) error {
	if err := s.MemoryStore.RemoveRobots(ctx, uids); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) RegisterIdentity(ctx context.Context, identity Identity) error {
	return s.RegisterIdentities(ctx, []Identity{identity})
}
func (s *FileStore) RegisterIdentities(ctx context.Context, identities []Identity) error {
	if err := s.MemoryStore.RegisterIdentities(ctx, identities); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) BeginCreateBatch(ctx context.Context, batch CreateBatch) error {
	if err := s.MemoryStore.BeginCreateBatch(ctx, batch); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) CompleteCreateBatch(ctx context.Context, id string) error {
	if err := s.MemoryStore.CompleteCreateBatch(ctx, id); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) RollbackCreateBatch(ctx context.Context, id string) error {
	if err := s.MemoryStore.RollbackCreateBatch(ctx, id); err != nil {
		return err
	}
	return s.persist()
}
func (s *FileStore) RecoverIncompleteCreateBatches(ctx context.Context) ([]CreateBatch, error) {
	result, err := s.MemoryStore.RecoverIncompleteCreateBatches(ctx)
	if err != nil {
		return nil, err
	}
	if len(result) > 0 {
		err = s.persist()
	}
	return result, err
}

func identityKey(identity Identity) string {
	return string(identity.Backend) + "\x00" + identity.Account + "\x00" + identity.CharacterName
}

var _ Directory = (*FileStore)(nil)
var _ RobotCatalog = (*FileStore)(nil)
var _ RobotProfileUpdater = (*FileStore)(nil)
var _ RobotRemover = (*FileStore)(nil)
var _ IdentityDirectory = (*FileStore)(nil)
var _ BatchDirectory = (*FileStore)(nil)
