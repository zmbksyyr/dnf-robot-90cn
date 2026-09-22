package shared

// RuntimeOnlineUser is the protocol-independent login command exchanged
// between scheduler and the DNF runtime.
type RuntimeOnlineUser struct {
	// IP is the actual df_game_r connection host.
	IP string
	// LoginIP is the stable client identity written to the native login tables.
	// Empty keeps compatibility with older callers by falling back to IP.
	LoginIP string
	Port    int
	Token   string
	UID     int
	// Backend credentials are optional and only consumed by a selected
	// backend session adapter. Native callers continue using Token.
	AccountName  string
	PasswordHash string

	// CID is the database character identity (taiwan_cain.charac_info.charac_no).
	CID int
	// GuildID is the persistent taiwan_cain.charac_info guild membership.
	GuildID int
	// CharacterSlot is the one-byte character-list index used by CMD 4/12.
	CharacterSlot int

	MaxReconnect   int
	ReconnectDelay int
	BirthVillage   int
	BirthArea      int
	BirthGateArea  int
	BirthX         int
	BirthY         int
	// DisjointCost queues CMD 238 on the login session itself. Zero keeps the
	// normal login path unchanged.
	DisjointCost uint32
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
	Type    int
}
