package scheduler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	robottemplate "robot/internal/capability/robottemplate"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/config"
	"robot/internal/foundation/filewatch"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
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
	backendRobotPurger              BackendRobotPurger
	backendPopulationInspector      BackendPopulationInspector
	backendInfo                     shared.BackendInfo
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
	autoItemStoreActive             int
	autoEnabled                     bool
	autoPortSince                   time.Time
	autoPortReady                   bool
	autoPortLog                     time.Time
	autoPortProbeAt                 time.Time
	autoPortProbeAddr               string
	autoPortProbeOpen               bool
	autoPortProbeError              string
	autoPortProbeInflight           bool
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
	onlineGateMu                    lockhub.Locker
	onlineTokens                    float64
	onlineTokenAt                   time.Time
	onlineInFlight                  int
	onlineBreakerStreak             int
	schedulerLastOnlineSuccess      int
	schedulerLastOnlineFailed       int
	schedulerRecentOnlineSuccess    int
	schedulerRecentOnlineFailed     int
	onlineAttemptSuccess            int
	onlineAttemptFailed             int
	schedulerLastAttemptSuccess     int
	schedulerLastAttemptFailed      int
	schedulerRecentAttemptSuccess   int
	schedulerRecentAttemptFailed    int
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
	runtimeFileWatchMu              lockhub.Locker
	runtimeFilePoller               *filewatch.Poller
	shoutTemplateSnapshot           atomic.Pointer[robottemplate.ShoutTemplates]
	nameTemplateSnapshot            atomic.Pointer[robottemplate.NameTemplates]
	supervisor                      *RobotSupervisor
	storePolicy                     shared.BackendStorePolicy
	followAccountLocator            shared.FollowAccountLocator
	accountOnlineChecker            shared.AccountOnlineChecker
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
	positionWrites                  atomic.Pointer[positionBatcher]
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

// BackendRobotPurger owns backend identifier resolution and durable deletion.
// The scheduler only coordinates lifecycle state around the returned plan.
type BackendRobotPurger interface {
	PlanDangerousDelete(context.Context, robotcap.DangerousDeleteRequest) (robotcap.DangerousDeletePlan, error)
	ExecuteDangerousDelete(context.Context, robotcap.DangerousDeletePlan) (robotcap.DangerousDeleteResult, error)
}

type BackendPopulationInspector interface {
	PopulationReport(context.Context) (robotcap.PopulationReport, error)
}

func (m *RobotManager) SetBackendRobotCreator(info shared.BackendInfo, creator BackendRobotCreator) {
	if m != nil {
		m.backendInfo = info
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

func (m *RobotManager) SetBackendRobotPurger(purger BackendRobotPurger) {
	if m != nil {
		m.backendRobotPurger = purger
	}
}

func (m *RobotManager) SetBackendPopulationInspector(inspector BackendPopulationInspector) {
	if m != nil {
		m.backendPopulationInspector = inspector
	}
}

func (m *RobotManager) PopulationReport(ctx context.Context) (robotcap.PopulationReport, error) {
	if m == nil {
		return robotcap.PopulationReport{}, fmt.Errorf("population inspector is not configured")
	}
	if m.backendPopulationInspector == nil {
		return robotcap.PopulationReport{}, fmt.Errorf("backend %s population inspector is not configured", m.backendInfo.ID)
	}
	return m.backendPopulationInspector.PopulationReport(ctx)
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

// SetBackendStorePolicy installs the selected adapter's store semantics. The
// scheduler keeps the orchestration; wire codes and shop constants stay in the
// adapter.
func (m *RobotManager) SetBackendStorePolicy(policy shared.BackendStorePolicy) {
	if m == nil {
		return
	}
	m.storePolicy = policy
}

// SetBackendFollowAccountLocator installs the adapter's follow-account lookup.
func (m *RobotManager) SetBackendFollowAccountLocator(locator shared.FollowAccountLocator) {
	if m == nil {
		return
	}
	m.followAccountLocator = locator
}

// SetBackendAccountOnlineChecker installs the adapter's account-online probe.
func (m *RobotManager) SetBackendAccountOnlineChecker(checker shared.AccountOnlineChecker) {
	if m == nil {
		return
	}
	m.accountOnlineChecker = checker
}

// lookupFollowAccountVillage queries the adapter for the follow account's last
// played village. Without an adapter implementation the follow target is
// unavailable.
func (m *RobotManager) lookupFollowAccountVillage(account string) (int, bool, error) {
	if m == nil || m.followAccountLocator == nil {
		return 0, false, errSchedulerStorageUnavailable
	}
	return m.followAccountLocator.FollowAccountVillageLastPlayed(context.Background(), account)
}

// accountOnline asks the adapter whether the account still has a session.
func (m *RobotManager) accountOnline(uid int) (bool, error) {
	if m == nil || m.accountOnlineChecker == nil {
		return false, errSchedulerStorageUnavailable
	}
	return m.accountOnlineChecker.AccountOnline(uid)
}

// disjointStoreCost returns the adapter-declared disjoint-store gold cost.
func (m *RobotManager) disjointStoreCost() uint32 {
	if m == nil || m.storePolicy == nil {
		return 0
	}
	return m.storePolicy.DisjointStoreCost()
}

// disjointFailure maps an adapter failure code to a stable reason string and
// whether the same session may retry at another coordinate.
func (m *RobotManager) disjointFailure(errCode byte) (string, bool) {
	if m != nil && m.storePolicy != nil {
		return m.storePolicy.DisjointFailure(errCode)
	}
	if errCode == 0 {
		return "disjoint_failed", false
	}
	return fmt.Sprintf("disjoint_err_0x%02x", errCode), false
}

// disjointReasonRetryable decides whether a failure reason allows an in-session
// coordinate retry. Scheduler-owned reasons keep their meaning; adapter-owned
// reasons are classified by the adapter.
func (m *RobotManager) disjointReasonRetryable(reason string) bool {
	switch reason {
	case "set_area_failed", "ack_timeout":
		return true
	}
	if m != nil && m.storePolicy != nil {
		if retry, known := m.storePolicy.DisjointReasonRetryable(reason); known {
			return retry
		}
	}
	return false
}

var errSchedulerStorageUnavailable = errors.New("scheduler robot state directory is not configured")

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
	manager.positionWrites.Store(newPositionBatcher(manager.positionRepo(), defaultPositionBatchOptions()))
	return manager
}

type allowGameCommandGate struct{}

func (allowGameCommandGate) Check() error { return nil }

// SetRobotStateDirectory injects the adapter-owned robot state directory.
// The selected adapter installs this during runtime initialization.
func (m *RobotManager) SetRobotStateDirectory(directory robotstate.Directory) {
	if m == nil {
		return
	}
	m.robotState = directory
	if batcher := m.positionWrites.Swap(newPositionBatcher(m.positionRepo(), defaultPositionBatchOptions())); batcher != nil {
		if err := batcher.Close(); err != nil {
			robotLogf("POSITION_BATCH_CLOSE_FAILED err=%v\n", err)
		}
	}
}

func (m *RobotManager) ensureSchedulerStorage() error {
	if m != nil && m.robotState != nil {
		return nil
	}
	return errSchedulerStorageUnavailable
}

func (m *RobotManager) selectRobots(req robotcap.CommandRequest) ([]robotcap.Info, error) {
	if m.robotState != nil {
		return m.robotState.SelectRobots(context.Background(), req)
	}
	return nil, errSchedulerStorageUnavailable
}

func (m *RobotManager) robotLocations() ([]shared.MapLocation, error) {
	if m.robotState != nil {
		return m.robotState.RobotLocations(context.Background())
	}
	return nil, errSchedulerStorageUnavailable
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

type missingPositionRepository struct{}

func (missingPositionRepository) UpdateRobotPositions(context.Context, []robotcap.PositionUpdate) error {
	return errors.New("scheduler position repository is not configured")
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

func robotLogf(format string, args ...interface{}) {
	foundationlog.Robotf(format, args...)
}
