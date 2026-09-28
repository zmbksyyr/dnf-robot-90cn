package shared

import "context"

type OpenSessionRequest struct {
	AccountName   string
	PasswordHash  string
	CharacterSlot uint16
	// EnablePartyDungeonFollower is an explicit session intent. Backends may
	// implement it with their own verified workflow or leave party following
	// unsupported.
	EnablePartyDungeonFollower bool
	// InitialTown* are robot-owned position metadata used by adapters that
	// need to distinguish an in-area coordinate move from an area transition.
	// They are not sent as game-database mutations or protocol credentials.
	InitialTownKnown bool
	InitialVillage   int
	InitialArea      int
	InitialX         int
	InitialY         int
}

type TownMoveIntent struct {
	X, Y      int16
	Direction byte
	Motion    uint16
}

type TownAreaMoveIntent struct {
	Village int
	Area    int
	X       int16
	Y       int16
}

type DungeonMoveIntent struct {
	NextX, NextY byte
}

type ShoutChannel string

const (
	ShoutChannelArea  ShoutChannel = "area"
	ShoutChannelParty ShoutChannel = "party"
	ShoutChannelWorld ShoutChannel = "world"
)

type ShoutIntent struct {
	Channel ShoutChannel
	Message string
}

type RobotSession interface {
	MoveTown(context.Context, TownMoveIntent) error
	MoveDungeon(context.Context, DungeonMoveIntent) error
	Shout(context.Context, ShoutIntent) error
	Close() error
}

type SessionFactory interface {
	OpenSession(context.Context, OpenSessionRequest) (RobotSession, error)
}
