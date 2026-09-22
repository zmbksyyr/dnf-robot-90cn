package s4a21

import "fmt"

// dungeonPhase is deliberately local to the S4A21 adapter. It is not exposed
// to the scheduler until the complete entry, settlement and recovery workflow
// is implemented.
type dungeonPhase uint8

const (
	dungeonPhaseTown dungeonPhase = iota
	dungeonPhaseSelection
	dungeonPhaseEntry
	dungeonPhaseLoading
	dungeonPhaseReady
)

type dungeonRunState struct {
	phase      dungeonPhase
	generation uint64
	dungeonID  uint32
	roomX      byte
	roomY      byte
}

type dungeonRunSnapshot struct {
	Phase      uint8
	Generation uint64
	DungeonID  uint32
	RoomX      byte
	RoomY      byte
}

func (s *dungeonRunState) BeginSelection(dungeonID uint32) error {
	if s == nil {
		return fmt.Errorf("S4A21 dungeon state is nil")
	}
	if dungeonID == 0 {
		return fmt.Errorf("S4A21 dungeon id must be positive")
	}
	if s.phase != dungeonPhaseTown {
		return fmt.Errorf("S4A21 dungeon selection requires town phase")
	}
	s.generation++
	s.dungeonID = dungeonID
	s.roomX, s.roomY = 0, 0
	s.phase = dungeonPhaseSelection
	return nil
}

func (s *dungeonRunState) BeginEntry() error {
	if s == nil {
		return fmt.Errorf("S4A21 dungeon state is nil")
	}
	if s.phase != dungeonPhaseSelection {
		return fmt.Errorf("S4A21 dungeon entry requires selection phase")
	}
	s.phase = dungeonPhaseEntry
	return nil
}

func (s *dungeonRunState) StartLoading(roomX, roomY byte) error {
	if s == nil {
		return fmt.Errorf("S4A21 dungeon state is nil")
	}
	if s.phase != dungeonPhaseEntry && s.phase != dungeonPhaseReady {
		return fmt.Errorf("S4A21 map loading requires entry or ready phase")
	}
	s.roomX, s.roomY = roomX, roomY
	s.phase = dungeonPhaseLoading
	return nil
}

func (s *dungeonRunState) FinishLoading() error {
	if s == nil {
		return fmt.Errorf("S4A21 dungeon state is nil")
	}
	if s.phase != dungeonPhaseLoading {
		return fmt.Errorf("S4A21 loading completion requires loading phase")
	}
	s.phase = dungeonPhaseReady
	return nil
}

func (s *dungeonRunState) ReturnToTown() {
	if s == nil {
		return
	}
	s.phase = dungeonPhaseTown
	s.dungeonID = 0
	s.roomX, s.roomY = 0, 0
}

func (s dungeonRunState) Snapshot() dungeonRunSnapshot {
	return dungeonRunSnapshot{
		Phase:      uint8(s.phase),
		Generation: s.generation,
		DungeonID:  s.dungeonID,
		RoomX:      s.roomX,
		RoomY:      s.roomY,
	}
}
