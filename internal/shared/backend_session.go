package shared

import "context"

type OpenSessionRequest struct {
	AccountName   string
	PasswordHash  string
	CharacterSlot uint16
}

type TownMoveIntent struct {
	X, Y      int16
	Direction byte
	Motion    uint16
}

type DungeonMoveIntent struct {
	NextX, NextY byte
	PathX, PathY uint32
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
