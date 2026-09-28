package shared

// RuntimeOnlineUser is the protocol-independent login command exchanged
// between scheduler and the DNF runtime.
type RuntimeOnlineUser struct {
	// IP is the adapter game-service connection host.
	IP string
	// LoginIP is the stable client identity used by the selected adapter.
	// Empty keeps compatibility with older callers by falling back to IP.
	LoginIP string
	Port    int
	UID     int
	// Backend credentials are optional and only consumed by a selected
	// backend session adapter.
	AccountName  string
	PasswordHash string

	// CID is the adapter's persistent character identity when one is exposed.
	CID int
	// GuildID is the adapter's persistent guild membership identity.
	GuildID int
	// CharacterSlot is the one-byte character-list index used by CMD 4/12.
	CharacterSlot int

	BirthVillage int
	BirthArea    int
	BirthX       int
	BirthY       int
}

type RuntimeMoveCommand struct {
	UID      int
	Village  int
	Area     int
	X        int
	Y        int
	MoveType int
	Speed    int
}

type RuntimeShoutCommand struct {
	UID     int
	Message string
}
