package scheduler

import (
	"context"
	"errors"
	"math/rand"
	"net"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	lifecyclecap "robot/internal/capability/robotlifecycle"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/config"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
	"sync"
	"sync/atomic"
	"time"
)

type RobotManager struct {
	mutationMu                      lockhub.RWLocker
	actorMutationMu                 lockhub.Locker
	database                        any
	robotState                      robotstate.Directory
	cfg                             *config.SysConfig
	doll                            Runtime
	gameCommandGate                 shared.GameCommandGate
	actions                         BackendActionTransport
	sessions                        sessionDriver
	backendRobotCreator             BackendRobotCreator
	backendRobotCleaner             BackendRobotCleaner
	backendInfo                     shared.BackendInfo
	backendLifecycleOwned           bool
	persistenceInspector            shared.PersistenceInspector
	systemAnnouncer                 shared.SystemAnnouncer
	worldShout                      WorldShout
	locks                           *lockhub.Hub
	startedAt                       time.Time
	autoMu                          lockhub.Locker
	sessionMu                       lockhub.Locker
	rand                            *rand.Rand
	sessionLastLogout               map[int]time.Time
	sessionReloginDelay             time.Duration
	sessionLogoutCleanupAt          time.Time
	autoStoreBusy                   map[int]bool
	autoStoreItemPending            int
	autoStoreDisjointPending        int
	cleanupPendingUIDs              map[int]time.Time
	autoStoreActive                 int
	autoStoreCap                    int
	autoItemStoreActive             int
	autoItemStoreCap                int
	autoEnabled                     bool
	autoPortSince                   time.Time
	autoPortReady                   bool
	autoPortLog                     time.Time
	autoPortProbeAt                 time.Time
	autoPortProbeAddr               string
	autoPortProbeOpen               bool
	autoPortProbeError              string
	autoPortDial                    func(string, string, time.Duration) (net.Conn, error)
	autoStats                       robotcap.AutoStatus
	autoBreakerUntil                time.Time
	autoBreakerReason               string
	autoBreakerLastCheck            time.Time
	autoBreakerLastOnlineFailed     int
	autoBreakerLastMoveFailed       int
	autoBreakerLastShoutLocalFailed int
	autoBreakerLastShoutWorldFailed int
	autoBreakerLastStoreFailed      int
	runtimeState                    runtimeStateTable
	followLookupMu                  lockhub.Locker
	followLookup                    followAccountLookup
	followLookupInFlight            string
	schedulerStatus                 robotcap.SchedulerStatus
	nextOperationID                 int64
	operations                      []robotcap.OperationStatus
	structuralOp                    string
	structuralOpStarted             time.Time
	structuralOpGeneration          uint64
	actorContainerOp                string
	actorContainerOpStarted         time.Time
	actorContainerOpGeneration      uint64
	configApplyMu                   lockhub.Locker
	configSnapshot                  atomic.Pointer[robotConfigSnapshot]
	runtimeFilesWatched             atomic.Bool
	shoutTemplateSnapshot           atomic.Pointer[robottemplate.ShoutTemplates]
	nameTemplateSnapshot            atomic.Pointer[robottemplate.NameTemplates]
	supervisor                      *RobotSupervisor
	storePointsCoord                *storecap.PointCoordinator
	worldHornCache                  *storecap.WorldHornCache
	storePoolLock                   lockhub.Locker
	storeItemPool                   *storecap.ItemPool
	storeTitleLock                  lockhub.Locker
	storeTitles                     *storecap.TitleCatalog
	storeTitleSnapshot              atomic.Pointer[storecap.TitleCatalog]
	storeTitlePathSnapshot          atomic.Pointer[storeTitlePathValue]
	storeTitlePath                  string
	storeTitlesLoaded               bool
	townMapCatalogMu                lockhub.RWLocker
	townMapCatalog                  []shared.MapCatalogItem
	positionWrites                  *positionBatcher
	characterCacheInvalidate        func(uid int) error
	mailNotifier                    MailNotifier
	mailNotifyNext                  time.Time
	mailNotifyRunning               bool
	mailNotifyDone                  chan struct{}
	mailNotifyCancel                context.CancelFunc
	mailNotifyLastErrorLog          time.Time
	partyAccountRangeSink           func(start, end int)
	backgroundMu                    lockhub.Locker
	backgroundWG                    sync.WaitGroup
	shuttingDown                    bool
	shutdownOnce                    sync.Once
	shutdownErr                     error
}

// BackendActionTransport is the backend-neutral action port used by the
// scheduler. Concrete packet construction remains in backend adapters.
type BackendActionTransport interface {
	MoveTown(context.Context, shared.RuntimeMoveCommand) error
	ShoutLocal(context.Context, shared.RuntimeShoutCommand) error
}

type BackendSessionTransport interface {
	Open(context.Context, int, shared.OpenSessionRequest) error
	Close(int) error
}

type BackendRobotCreator interface {
	CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error)
}

type BackendRobotCleaner interface {
	CleanupRobots(context.Context, robotcap.CleanupRequest) (robotcap.CleanupResult, error)
}

func (m *RobotManager) SetBackendRobotCreator(info shared.BackendInfo, creator BackendRobotCreator) {
	if m != nil {
		m.backendInfo = info
		m.backendLifecycleOwned = info.ID != ""
		m.backendRobotCreator = creator
	}
}

// ConfigureBackendRuntime installs the selected backend's shared runtime
// ports. Concrete database and announcement behavior remains in composition.
func (m *RobotManager) ConfigureBackendRuntime(info shared.BackendInfo, persistence shared.PersistenceInspector, announcer shared.SystemAnnouncer) {
	if m == nil {
		return
	}
	m.backendInfo = info
	m.persistenceInspector = persistence
	m.systemAnnouncer = announcer
}

func (m *RobotManager) SetBackendRobotCleaner(cleaner BackendRobotCleaner) {
	if m != nil {
		m.backendRobotCleaner = cleaner
	}
}

func (m *RobotManager) SetBackendSessionTransport(transport BackendSessionTransport) {
	if m != nil && transport != nil {
		m.sessions = protocolSessionDriver{manager: m, transport: transport}
	}
}

// SetGameCommandGate installs the selected backend's runtime precondition.
func (m *RobotManager) SetGameCommandGate(gate shared.GameCommandGate) {
	if m != nil && gate != nil {
		m.gameCommandGate = gate
	}
}

func (m *RobotManager) CheckGameCommand() error {
	if m == nil || m.gameCommandGate == nil {
		return nil
	}
	return m.gameCommandGate.Check()
}

func (m *RobotManager) SetBackendActionTransport(transport BackendActionTransport) {
	if m != nil && transport != nil {
		m.actions = transport
	}
}

// SetTownMapCatalog publishes an immutable backend-projected town map
// snapshot. An empty snapshot restores the runtime-file fallback.
func (m *RobotManager) SetTownMapCatalog(maps []shared.MapCatalogItem) {
	if m == nil {
		return
	}
	copyMaps := cloneTownMapCatalog(maps)
	m.townMapCatalogMu.Lock()
	m.townMapCatalog = copyMaps
	m.townMapCatalogMu.Unlock()
}

type storeTitlePathValue struct {
	path string
}

func NewRobotManager(database any, cfg *config.SysConfig, doll Runtime) *RobotManager {
	if doll == nil {
		doll = noopRuntime{}
	}
	manager := &RobotManager{
		database:            database,
		cfg:                 cfg,
		doll:                doll,
		gameCommandGate:     allowGameCommandGate{},
		worldShout:          noopWorldShout{},
		locks:               lockhub.New(),
		startedAt:           time.Now(),
		rand:                rand.New(rand.NewSource(time.Now().UnixNano())),
		cleanupPendingUIDs:  make(map[int]time.Time),
		sessionLastLogout:   make(map[int]time.Time),
		sessionReloginDelay: 15 * time.Second,
		worldHornCache:      storecap.NewWorldHornCache(),
	}
	manager.actions = runtimeActionTransport{manager: manager}
	manager.sessions = runtimeSessionDriver{manager: manager}
	manager.positionWrites = newPositionBatcher(manager.positionRepo(), defaultPositionBatchOptions())
	return manager
}

type allowGameCommandGate struct{}

func (allowGameCommandGate) Check() error { return nil }

// SetRobotStateDirectory injects the adapter-owned robot state directory.
// The S4A21 runtime installs this during adapter initialization.
func (m *RobotManager) SetRobotStateDirectory(directory robotstate.Directory) {
	if m == nil {
		return
	}
	m.robotState = directory
	if m.positionWrites != nil {
		_ = m.positionWrites.Close()
	}
	m.positionWrites = newPositionBatcher(m.positionRepo(), defaultPositionBatchOptions())
}

func (m *RobotManager) repo() SchedulerRepository {
	if repository, ok := m.database.(SchedulerRepository); ok {
		return repository
	}
	return missingRepository{}
}

func (m *RobotManager) ensureSchedulerStorage() error {
	if m != nil && m.robotState != nil {
		return nil
	}
	return m.repo().EnsureSchema()
}

func (m *RobotManager) selectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	if m.robotState != nil {
		return m.robotState.SelectRobots(context.Background(), req)
	}
	return m.repo().SelectRobots(req)
}

func (m *RobotManager) robotLocations() ([]shared.MapLocation, error) {
	if m.robotState != nil {
		return m.robotState.RobotLocations(context.Background())
	}
	return m.schemaRepo().RobotLocations()
}

func (m *RobotManager) schemaRepo() SchemaRepository {
	if repository, ok := m.database.(SchemaRepository); ok {
		return repository
	}
	return missingSchemaRepository{}
}

func (m *RobotManager) positionRepo() robotPositionWriter {
	if m.robotState != nil {
		return m.robotState
	}
	if repository, ok := m.database.(robotPositionWriter); ok {
		return repository
	}
	return missingPositionRepository{}
}

func (m *RobotManager) lockHub() *lockhub.Hub {
	if m.locks == nil {
		m.locks = lockhub.New()
	}
	return m.locks
}

func (m *RobotManager) withCache(reason string, fn func()) {
	_ = m.lockHub().WithResource(lockScopeConfig, lockResourceRobotConfig, reason, func() error {
		fn()
		return nil
	})
}

// BeginBackgroundWork registers API work that must finish before Manager
// resources are closed. The mutex prevents Wait from racing a late Add.
func (m *RobotManager) BeginBackgroundWork() (func(), bool) {
	if m == nil {
		return nil, false
	}
	m.backgroundMu.Lock()
	if m.shuttingDown {
		m.backgroundMu.Unlock()
		return nil, false
	}
	m.backgroundWG.Add(1)
	m.backgroundMu.Unlock()
	var once sync.Once
	return func() { once.Do(m.backgroundWG.Done) }, true
}

func (m *RobotManager) stopAndWaitBackgroundWork() {
	m.backgroundMu.Lock()
	m.shuttingDown = true
	m.backgroundMu.Unlock()
	m.backgroundWG.Wait()
}

type SchedulerRepository interface {
	SelectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error)
	EnsureSchema() error
}

type SchemaRepository interface {
	InsertIgnore(table string, values map[string]interface{}) error
	InsertIgnoreIfTableExists(table string, values map[string]interface{}) error
	TableColumns(table string) (map[string]bool, error)
	TableExists(table string) (bool, error)
	DeleteByIntIfTableExists(table, col string, id int) error
	NextInt(query string, fallback int) (int, error)
	AvailableRobotUIDs(count, start, end int) ([]int, error)
	AccountAutoIncrement() (int, error)
	PrepareRobotUIDRange(uidStart, uidEnd, uidGuard int) error
	AllocateRobotIDs(count, uidStart, uidEnd int) (lifecyclecap.RobotIDAllocation, error)
	CharacterNameExists(dbName string) (bool, error)
	EnsureAccount(uid int, innerIP string) error
	ClearTradePunish(uid int) (int64, error)
	CreateBaseCharacter(info robotcap.Info, rc robotconfig.RuntimeConfig) error
	SaveEquipmentSlots(cid int, raw []byte) error
	ReplaceAvatarItems(cid int, selected map[int]shared.EquipmentCatalogItem) error
	ReplacePetItems(cid int, pet shared.EquipmentCatalogItem, artifacts map[int]shared.EquipmentCatalogItem) error
	MarkStoreStarted(uid int) error
	PrepareStorePosition(info robotcap.Info) error
	PrepareDisjointPosition(info robotcap.Info, cost int) error
	RestoreDummyNormal(info robotcap.Info) error
	SyncCharacterVillage(cid int, village int) (int, error)
	LoadInventory(cid int) ([]byte, error)
	SaveInventory(cid int, capacity int, raw []byte) error
	SaveInventoryRaw(cid int, raw []byte) error
	ReplaceStoreStall(uid int, title string, items []storecap.StallItem) (storecap.StallResult, error)
	EnsureStorePermission(uid, cid int) (storecap.PermissionStatus, error)
	AccountOnline(uid int) (bool, error)
	EnsureDisjointProfession(info robotcap.Info) error
	RevokeStorePermission(uid, cid int) error
	FollowAccountVillageLastPlayed(account string) (int, bool, error)
	RobotCharacterName(uid int) (string, error)
	AliveRobotUIDs(uids []int) (map[int]bool, error)
	RobotStatusRows(req robotcap.CommandRequest) ([]robotcap.StatusItem, int, error)
	RobotLocations() ([]shared.MapLocation, error)
	CleanupCandidates(req robotcap.CleanupRequest) ([]robotcap.CleanupCandidate, error)
	DangerousDeletePlan(req robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error)
	BatchDeleteRobotData(uids, cids []int) error
	BatchDeleteCharacterData(cids []int) error
	DeleteCharacterAtomic(uid, cid int, deleteRobotMetadata bool) error
	BatchDeleteRobotMetadata(uids []int) error
	UpsertDummy(info robotcap.Info, innerIP string) error
	RegisterRobot(info robotcap.Info) error
	RebuildCharacView(uid int) error
	CopyTemplateDefaults(cid int) error
	RecoverIncompleteCreateBatches() error
	BeginCreateBatch(batchID string, uids, cids []int) error
	CompleteCreateBatch(batchID string) error
	RollbackCreateBatch(batchID string) error
}

type Runtime interface {
	SessionRuntime
	MoveRuntime
	ShoutRuntime
	StatusRuntime
	StoreRuntime
	AreaRuntime
}

type SessionRuntime interface {
	Logout(uid int) error
	Online(users []shared.RuntimeOnlineUser) error
}

type MoveRuntime interface {
	Move(command shared.RuntimeMoveCommand) error
}

type ShoutRuntime interface {
	Shout(command shared.RuntimeShoutCommand) error
}

type StatusRuntime interface {
	RuntimeStatus() []robotcap.RuntimeStatus
	PartyActive(uid int) bool
}

type StoreRuntime interface {
	StartPrivateStore(uid int, title string) bool
	ResetPrivateStore(uid int) bool
	StartDisjointStore(uid int, cost uint32) bool
	SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool
}

type AreaRuntime interface {
	SetArea(uid int, village, area int, x, y int) bool
}

type missingRepository struct{}

func (missingRepository) SelectRobots(robotcap.CommandRequest) ([]robotcap.Info, error) {
	return nil, errors.New("scheduler repository is not configured")
}

func (missingRepository) EnsureSchema() error {
	return errors.New("scheduler repository is not configured")
}

type missingSchemaRepository struct{}

func (missingSchemaRepository) InsertIgnore(string, map[string]interface{}) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) InsertIgnoreIfTableExists(string, map[string]interface{}) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) TableColumns(string) (map[string]bool, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) TableExists(string) (bool, error) {
	return false, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) DeleteByIntIfTableExists(string, string, int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) NextInt(string, int) (int, error) {
	return 0, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) AvailableRobotUIDs(int, int, int) ([]int, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) AccountAutoIncrement() (int, error) {
	return 0, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) PrepareRobotUIDRange(int, int, int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) AllocateRobotIDs(int, int, int) (lifecyclecap.RobotIDAllocation, error) {
	return lifecyclecap.RobotIDAllocation{}, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) CharacterNameExists(string) (bool, error) {
	return false, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) EnsureAccount(int, string) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) ClearTradePunish(int) (int64, error) {
	return 0, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) CreateBaseCharacter(robotcap.Info, robotconfig.RuntimeConfig) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) SaveEquipmentSlots(int, []byte) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) ReplaceAvatarItems(int, map[int]shared.EquipmentCatalogItem) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) ReplacePetItems(int, shared.EquipmentCatalogItem, map[int]shared.EquipmentCatalogItem) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) MarkStoreStarted(int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) PrepareStorePosition(robotcap.Info) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) PrepareDisjointPosition(robotcap.Info, int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RestoreDummyNormal(robotcap.Info) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) SyncCharacterVillage(int, int) (int, error) {
	return 0, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) LoadInventory(int) ([]byte, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) SaveInventory(int, int, []byte) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) SaveInventoryRaw(int, []byte) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) ReplaceStoreStall(int, string, []storecap.StallItem) (storecap.StallResult, error) {
	return storecap.StallResult{}, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) EnsureStorePermission(int, int) (storecap.PermissionStatus, error) {
	return storecap.PermissionStatus{}, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) AccountOnline(int) (bool, error) {
	return false, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) EnsureDisjointProfession(robotcap.Info) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RevokeStorePermission(int, int) error {
	return errors.New("scheduler schema repository is not configured")
}

type missingPositionRepository struct{}

func (missingPositionRepository) UpdateRobotPositions(context.Context, []robotcap.PositionUpdate) error {
	return errors.New("scheduler position repository is not configured")
}

func (missingSchemaRepository) FollowAccountVillageLastPlayed(string) (int, bool, error) {
	return 0, false, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RobotCharacterName(int) (string, error) {
	return "", errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) AliveRobotUIDs([]int) (map[int]bool, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RobotStatusRows(robotcap.CommandRequest) ([]robotcap.StatusItem, int, error) {
	return nil, 0, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RobotLocations() ([]shared.MapLocation, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) CleanupCandidates(robotcap.CleanupRequest) ([]robotcap.CleanupCandidate, error) {
	return nil, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) DangerousDeletePlan(robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error) {
	return robotcap.DangerousDeletePlan{}, errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) BatchDeleteRobotData([]int, []int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) BatchDeleteCharacterData([]int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) DeleteCharacterAtomic(int, int, bool) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) BatchDeleteRobotMetadata([]int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) UpsertDummy(robotcap.Info, string) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RegisterRobot(robotcap.Info) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RebuildCharacView(int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) CopyTemplateDefaults(int) error {
	return errors.New("scheduler schema repository is not configured")
}

func (missingSchemaRepository) RecoverIncompleteCreateBatches() error {
	return errors.New("scheduler schema repository is not configured")
}
func (missingSchemaRepository) BeginCreateBatch(string, []int, []int) error {
	return errors.New("scheduler schema repository is not configured")
}
func (missingSchemaRepository) CompleteCreateBatch(string) error {
	return errors.New("scheduler schema repository is not configured")
}
func (missingSchemaRepository) RollbackCreateBatch(string) error {
	return errors.New("scheduler schema repository is not configured")
}

type noopRuntime struct{}

func (noopRuntime) Logout(int) error {
	return nil
}

func (noopRuntime) Move(shared.RuntimeMoveCommand) error {
	return nil
}

func (noopRuntime) Shout(shared.RuntimeShoutCommand) error {
	return nil
}

func (noopRuntime) Online([]shared.RuntimeOnlineUser) error {
	return nil
}

func (noopRuntime) RuntimeStatus() []robotcap.RuntimeStatus {
	return nil
}

func (noopRuntime) PartyActive(int) bool { return false }

func (noopRuntime) StartPrivateStore(uid int, title string) bool {
	return false
}

func (noopRuntime) ResetPrivateStore(uid int) bool {
	return false
}

func (noopRuntime) StartDisjointStore(uid int, cost uint32) bool {
	return false
}

func (noopRuntime) SetArea(uid int, village, area int, x, y int) bool {
	return false
}

func (noopRuntime) SetAreaFrom(uid int, village, area int, x, y int, fromVillage, fromArea int) bool {
	return false
}

type WorldShout interface {
	SendWorldShout(msg, name string, senderID uint16) error
	SendMonitorAnnouncement(kind, msg, name string, senderID uint16) error
}

type noopWorldShout struct{}

func (noopWorldShout) SendWorldShout(msg, name string, senderID uint16) error {
	return nil
}

func (noopWorldShout) SendMonitorAnnouncement(kind, msg, name string, senderID uint16) error {
	return nil
}

func (m *RobotManager) SetWorldShout(worldShout WorldShout) {
	if worldShout == nil {
		worldShout = noopWorldShout{}
	}
	m.worldShout = worldShout
}

func robotLogf(format string, args ...interface{}) {
	foundationlog.Robotf(format, args...)
}
