// Package robotstate owns the robot platform's backend-independent state.
// It deliberately contains no game-server database or character persistence.
package robotstate

import (
	"context"
	"errors"

	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/lockhub"
	"robot/internal/shared"
)

var ErrNotFound = errors.New("robot state not found")
var ErrDuplicateIdentity = errors.New("robot identity already registered")

// Identity records only robot-owned linkage. Backend-specific IDs are optional
// because simulator protocols may expose a slot/name but no native UID/CID.
type Identity struct {
	Backend            shared.BackendID
	Account            string
	CharacterName      string
	Slot               *uint16
	BackendUserID      string
	BackendCharacterID string
}

type IdentityDirectory interface {
	RegisterIdentity(context.Context, Identity) error
	RegisterIdentities(context.Context, []Identity) error
	Identities(context.Context, shared.BackendID) ([]Identity, error)
}

type Directory interface {
	SelectRobots(context.Context, robotcap.CommandRequest) ([]robotcap.Info, error)
	RobotLocations(context.Context) ([]shared.MapLocation, error)
	UpdateRobotPositions(context.Context, []robotcap.PositionUpdate) error
}

type MemoryStore struct {
	mu         lockhub.RWLocker
	robots     map[int]robotcap.Info
	locations  map[int]shared.MapLocation
	identities map[string]Identity
	batches    map[string]CreateBatch
}

func NewMemoryStore(robots []robotcap.Info) *MemoryStore {
	store := &MemoryStore{robots: make(map[int]robotcap.Info), locations: make(map[int]shared.MapLocation), identities: make(map[string]Identity), batches: make(map[string]CreateBatch)}
	for _, robot := range robots {
		store.robots[robot.UID] = robot
		store.locations[robot.UID] = shared.MapLocation{Village: robot.Village, Area: robot.Area, X: robot.X, Y: robot.Y}
	}
	return store
}

func (s *MemoryStore) RegisterIdentity(ctx context.Context, identity Identity) error {
	return s.RegisterIdentities(ctx, []Identity{identity})
}

func (s *MemoryStore) RegisterIdentities(ctx context.Context, identities []Identity) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if identity.Backend == "" || identity.Account == "" || identity.CharacterName == "" {
			return errors.New("backend, account, and character name are required")
		}
		key := string(identity.Backend) + "\x00" + identity.Account + "\x00" + identity.CharacterName
		if _, exists := s.identities[key]; exists {
			return ErrDuplicateIdentity
		}
		if _, exists := keys[key]; exists {
			return ErrDuplicateIdentity
		}
		keys[key] = struct{}{}
	}
	for _, identity := range identities {
		key := string(identity.Backend) + "\x00" + identity.Account + "\x00" + identity.CharacterName
		s.identities[key] = identity
	}
	return nil
}

func (s *MemoryStore) Identities(ctx context.Context, backend shared.BackendID) ([]Identity, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Identity, 0, len(s.identities))
	for _, identity := range s.identities {
		if backend == "" || identity.Backend == backend {
			result = append(result, identity)
		}
	}
	return result, nil
}

func (s *MemoryStore) SelectRobots(ctx context.Context, req robotcap.CommandRequest) ([]robotcap.Info, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	selected := make([]robotcap.Info, 0, len(s.robots))
	if len(req.UIDs) > 0 {
		for _, uid := range req.UIDs {
			if robot, ok := s.robots[uid]; ok {
				selected = append(selected, robot)
			}
		}
		return selected, nil
	}
	limit := req.Count
	if limit <= 0 {
		limit = 10
	}
	for _, robot := range s.robots {
		if len(selected) >= limit {
			break
		}
		selected = append(selected, robot)
	}
	return selected, nil
}

func (s *MemoryStore) RobotLocations(ctx context.Context) ([]shared.MapLocation, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	locations := make([]shared.MapLocation, 0, len(s.locations))
	for _, location := range s.locations {
		locations = append(locations, location)
	}
	return locations, nil
}

func (s *MemoryStore) UpdateRobotPositions(ctx context.Context, updates []robotcap.PositionUpdate) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, update := range updates {
		if _, ok := s.robots[update.UID]; !ok {
			return ErrNotFound
		}
	}
	for _, update := range updates {
		location := shared.MapLocation{Village: update.Village, Area: update.Area, X: update.X, Y: update.Y}
		s.locations[update.UID] = location
		robot := s.robots[update.UID]
		robot.Village, robot.Area, robot.X, robot.Y = update.Village, update.Area, update.X, update.Y
		s.robots[update.UID] = robot
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

var _ Directory = (*MemoryStore)(nil)
