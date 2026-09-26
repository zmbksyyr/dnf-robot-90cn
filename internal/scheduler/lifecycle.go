package scheduler

import (
	"context"
	"fmt"
	actormodel "robot/internal/actor"
	robotcap "robot/internal/capability/robot"
	robotstate "robot/internal/capability/robotstate"
	"robot/internal/foundation/process"
	"robot/internal/shared"
	"strings"
	"sync"
	"time"
)

type SystemStatus struct {
	UpdatedAt       time.Time `json:"updated_at"`
	RobotCPUPercent float64   `json:"robot_cpu_percent"`
	RobotMemoryMB   int       `json:"robot_memory_mb"`
	RobotGoroutines int       `json:"robot_threads"`
	RobotUptimeSec  int       `json:"robot_uptime_seconds"`
	Running         int       `json:"running"`
	Store           int       `json:"store"`
}

func (m *RobotManager) SystemStatus() SystemStatus {
	summary := m.runtimeStatusSummarySnapshot()
	cpu, mem, goroutines := process.ResourceSnapshot()
	return SystemStatus{
		UpdatedAt:       time.Now(),
		RobotCPUPercent: cpu,
		RobotMemoryMB:   mem,
		RobotGoroutines: goroutines,
		RobotUptimeSec:  int(time.Since(m.startedAt).Seconds()),
		Running:         summary.Running,
		Store:           summary.Stores,
	}
}

func (m *RobotManager) DatabaseStatus() shared.PersistenceStatus {
	if err := m.requireBackendCapability(shared.CapabilityDatabase); err != nil {
		return shared.PersistenceStatus{Error: err.Error(), CheckedAt: time.Now()}
	}
	if m.persistenceInspector == nil {
		return shared.PersistenceStatus{Error: "backend persistence inspector is not configured", CheckedAt: time.Now()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return m.persistenceInspector.Status(ctx)
}

type RobotStatusResult struct {
	Robots    []robotcap.StatusItem `json:"robots"`
	Total     int                   `json:"total"`
	Running   int                   `json:"running"`
	Store     int                   `json:"store"`
	UpdatedAt time.Time             `json:"updated_at"`
}

func (m *RobotManager) RobotsStatus(req robotcap.CommandRequest) (RobotStatusResult, error) {
	runtime := m.runtimeStatusMap()
	actors := m.actorStatusMap()
	cleanupPending := m.cleanupPendingSet()
	villageNames := mapCatalogVillageNames(m.loadMapCatalog())
	accounts := m.robotStateAccounts()
	var items []robotcap.StatusItem
	var total int
	if m.robotState != nil {
		robots, err := m.selectRobots(req)
		if err != nil {
			return RobotStatusResult{}, err
		}
		items = make([]robotcap.StatusItem, 0, len(robots))
		for _, robot := range robots {
			items = append(items, robotcap.StatusItem{
				UID: robot.UID, CID: robot.CID, Name: robot.Name, Account: accounts[robot.Name], Level: robot.Level,
				Job: robot.Job, Grow: robot.Grow, Village: robot.Village, Area: robot.Area,
				X: robot.X, Y: robot.Y, DBState: robotcap.DBStateExists,
			})
		}
		total = len(items)
	} else {
		var err error
		items, total, err = m.schemaRepo().RobotStatusRows(req)
		if err != nil {
			return RobotStatusResult{}, err
		}
	}

	out := RobotStatusResult{UpdatedAt: time.Now()}
	for _, item := range items {
		stateName := robotcap.RuntimeStateStop
		onlineDesired := false
		item.DBState = robotcap.DBStateExists
		if item.MissingCore {
			item.DBState = robotcap.DBStateMissingCore
			stateName = robotcap.RuntimeStateWrong
		} else if st, ok := runtime[item.UID]; ok {
			item.State = st.State
			stateName = st.StateName
			item.Online = robotcap.ActiveRuntimeStatus(st)
			item.DisconnectReason = st.DisconnectReason
			item.Reconnects = st.Reconnects
			item.RobotType = st.RobotType
			item.StoreDisplaySent = st.StoreDisplaySent
			item.StoreDisplayAck = st.StoreDisplayAck
			item.StoreDisplayItems = st.StoreDisplayItems
			item.StoreCreated = st.StoreCreated
			item.DisjointActive = st.DisjointActive
			item.UptimeSeconds = st.UptimeSeconds
			if st.Village != 0 || st.Area != 0 || st.X != 0 || st.Y != 0 {
				item.Village = st.Village
				item.Area = st.Area
				item.X = st.X
				item.Y = st.Y
			}
		}
		item.VillageName = villageNames[item.Village]
		if actor, ok := actors[item.UID]; ok {
			item.ActorAttached = true
			item.ActorSlot = actor.SlotID
			item.ActorState = string(actor.State)
			item.ActorBusy = actor.Busy
			item.ActorBusyKind = actor.BusyKind
			onlineDesired = actor.OnlineDesired
			item.Operation = actorOperation(actor)
		}
		if cleanupPending[item.UID] {
			item.Operation = "cleanup"
		}
		item.RobotState = robotStateView(item, stateName, onlineDesired)
		if item.Online {
			out.Running++
		}
		if item.RobotType == 2 || item.RobotType == 3 || item.StoreCreated || item.StoreDisplayAck {
			out.Store++
		}
		out.Robots = append(out.Robots, item)
	}
	out.Total = total
	return out, nil
}

func mapCatalogVillageNames(maps []shared.MapCatalogItem) map[int]string {
	out := make(map[int]string)
	for _, mp := range maps {
		if mp.Village <= 0 || strings.TrimSpace(mp.VillageName) == "" {
			continue
		}
		if _, ok := out[mp.Village]; !ok {
			out[mp.Village] = strings.TrimSpace(mp.VillageName)
		}
	}
	return out
}

func (m *RobotManager) robotStateAccounts() map[string]string {
	accounts := map[string]string{}
	if m == nil || m.robotState == nil {
		return accounts
	}
	directory, ok := m.robotState.(robotstate.IdentityDirectory)
	if !ok {
		return accounts
	}
	identities, err := directory.Identities(context.Background(), m.backendInfo.ID)
	if err != nil {
		return accounts
	}
	for _, identity := range identities {
		if identity.CharacterName != "" && identity.Account != "" {
			accounts[identity.CharacterName] = identity.Account
		}
	}
	return accounts
}

func robotStateView(item robotcap.StatusItem, stateName string, onlineDesired bool) shared.RobotState {
	actual := shared.RuntimeActualState(stateName, item.DisconnectReason, item.MissingCore)
	desired := shared.DesiredUnknown
	if item.ActorAttached || item.Operation != "" {
		desired = shared.DesiredFromOperation(item.Operation, onlineDesired)
	}
	phase := shared.PhaseConfirmed
	if actual == shared.ActualDisconnected || actual == shared.ActualError {
		phase = shared.PhaseFailed
	} else if item.Operation != "" {
		phase = shared.PhaseExecuting
	} else if item.ActorAttached {
		phase = shared.PhaseAssigned
	}
	lastError := ""
	if item.MissingCore {
		lastError = robotcap.DBStateMissingCore
	} else if item.DisconnectReason != 0 {
		lastError = fmt.Sprintf("disconnect_%d", item.DisconnectReason)
	} else if actual == shared.ActualError {
		lastError = stateName
	}
	return shared.RobotState{
		UID:          item.UID,
		CID:          item.CID,
		ActorID:      actorID(item),
		DesiredState: desired,
		ActualState:  actual,
		Phase:        phase,
		LastError:    lastError,
		UpdatedAt:    time.Now(),
	}
}

func actorID(item robotcap.StatusItem) string {
	if !item.ActorAttached || item.ActorSlot <= 0 {
		return ""
	}
	return fmt.Sprintf("actor-%d", item.ActorSlot)
}

func actorOperation(actor actormodel.Snapshot) string {
	return actormodel.Operation(actor)
}

func (m *RobotManager) actorStatusMap() map[int]actormodel.Snapshot {
	out := map[int]actormodel.Snapshot{}
	registry := m.currentActorRegistry()
	if registry == nil {
		return out
	}
	for _, snap := range registry.actorSnapshots() {
		if snap.UID > 0 {
			out[snap.UID] = snap
		}
	}
	return out
}

func (m *RobotManager) CleanupRobots(req robotcap.CleanupRequest) (robotcap.CleanupResult, error) {
	if err := m.requireBackendCapability(shared.CapabilityCleanup); err != nil {
		return robotcap.CleanupResult{}, err
	}
	if m.backendLifecycleOwned && m.backendRobotCleaner == nil {
		return robotcap.CleanupResult{}, fmt.Errorf("backend %s cleanup adapter is not configured", m.backendInfo.ID)
	}
	return m.cleanupRobots(req)
}

func (m *RobotManager) cleanupRobots(req robotcap.CleanupRequest) (robotcap.CleanupResult, error) {
	var finishOperation func(string, error) robotcap.OperationStatus
	var opErr error
	var opResult robotcap.CleanupResult
	if req.Force {
		if !req.InternalConfirmedBroken {
			var err error
			_, finishOperation, err = m.beginTrackedStructuralOperation("cleanup", robotcap.CleanupRequestScope(req))
			if err != nil {
				return robotcap.CleanupResult{}, err
			}
			defer func() {
				finishOperation(robotcap.CleanupOperationSummary(opResult, opErr), opErr)
			}()
		}
	}
	var result robotcap.CleanupResult
	var err error
	if m.backendRobotCleaner != nil {
		var finishDelete func()
		if req.Force {
			uids, selectErr := m.backendCleanupUIDs(req)
			if selectErr != nil {
				opErr = selectErr
				return robotcap.CleanupResult{}, selectErr
			}
			finishDelete = (lifecycleCleanupEnv{manager: m, request: req}).PrepareDelete(uids)
		}
		if finishDelete != nil {
			defer finishDelete()
		}
		result, err = m.backendRobotCleaner.CleanupRobots(context.Background(), req)
	} else {
		result, err = m.lifecycleCleaner(req).Cleanup(req)
	}
	opErr = err
	opResult = result
	return result, err
}

func (m *RobotManager) backendCleanupUIDs(req robotcap.CleanupRequest) ([]int, error) {
	if m.robotState == nil {
		return nil, fmt.Errorf("backend robot state is not configured")
	}
	robots, err := m.robotState.SelectRobots(context.Background(), robotcap.CommandRequest{Count: int(^uint(0) >> 1)})
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]struct{}, len(req.UIDs))
	for _, uid := range req.UIDs {
		wanted[uid] = struct{}{}
	}
	uids := make([]int, 0, len(robots))
	for _, robot := range robots {
		if len(wanted) > 0 {
			if _, ok := wanted[robot.UID]; !ok {
				continue
			}
		} else if req.MinUID > 0 && robot.UID < req.MinUID || req.MaxUID > 0 && robot.UID > req.MaxUID {
			continue
		}
		uids = append(uids, robot.UID)
	}
	return uids, nil
}

var errOperationConflict = fmt.Errorf("structural operation already running")

func (m *RobotManager) BeginOperation(typ, scope string) robotcap.OperationStatus {
	op, _ := m.BeginOperationGuarded(typ, scope, false)
	return op
}

func (m *RobotManager) BeginOperationGuarded(typ, scope string, structural bool) (robotcap.OperationStatus, error) {
	now := time.Now()
	var op robotcap.OperationStatus
	var err error
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerOperation, "begin_operation", func() error {
		typ = strings.TrimSpace(typ)
		scope = strings.TrimSpace(scope)
		if structural {
			for _, existing := range m.operations {
				if existing.State != robotcap.OperationStateRunning || !robotcap.IsStructuralOperation(existing.Type) {
					continue
				}
				err = fmt.Errorf("%w: %s %s", errOperationConflict, existing.Type, existing.Scope)
				return nil
			}
		}
		m.nextOperationID++
		op = robotcap.OperationStatus{
			ID:        m.nextOperationID,
			Type:      typ,
			Scope:     scope,
			State:     robotcap.OperationStateRunning,
			StartedAt: now,
			UpdatedAt: now,
		}
		m.operations = append([]robotcap.OperationStatus{op}, m.operations...)
		if len(m.operations) > 20 {
			m.operations = m.operations[:20]
		}
		return nil
	})
	return op, err
}

func (m *RobotManager) beginTrackedStructuralOperation(typ, scope string) (op robotcap.OperationStatus, finish func(string, error) robotcap.OperationStatus, err error) {
	m.mutationMu.Lock()
	locked := true
	releaseLock := func() {
		if locked {
			locked = false
			m.mutationMu.Unlock()
		}
	}
	// A panic between Lock and the caller's deferred finish must not leave the
	// structural write lock held forever.
	defer func() {
		if recovered := recover(); recovered != nil {
			releaseLock()
			op = robotcap.OperationStatus{}
			finish = nil
			err = fmt.Errorf("begin %s operation: %v", typ, recovered)
		}
	}()
	op, err = m.BeginOperationGuarded(typ, scope, true)
	if err != nil {
		releaseLock()
		return robotcap.OperationStatus{}, nil, err
	}
	done := m.beginStructuralOp(typ)
	var finishOnce sync.Once
	finish = func(summary string, opErr error) robotcap.OperationStatus {
		status := m.CompleteOperation(op.ID, summary, opErr)
		finishOnce.Do(func() {
			done()
			releaseLock()
		})
		return status
	}
	return op, finish, nil
}

func (m *RobotManager) CompleteOperation(id int64, summary string, err error) robotcap.OperationStatus {
	now := time.Now()
	var op robotcap.OperationStatus
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerOperation, "complete_operation", func() error {
		for i := range m.operations {
			if m.operations[i].ID != id {
				continue
			}
			m.operations[i].UpdatedAt = now
			m.operations[i].FinishedAt = now
			m.operations[i].Summary = strings.TrimSpace(summary)
			if err != nil {
				m.operations[i].State = robotcap.OperationStateFailed
				m.operations[i].Error = err.Error()
			} else {
				m.operations[i].State = robotcap.OperationStateDone
			}
			op = m.operations[i]
			return nil
		}
		op = robotcap.OperationStatus{ID: id, State: robotcap.OperationStateUnknown, Summary: strings.TrimSpace(summary), UpdatedAt: now}
		if err != nil {
			op.State = robotcap.OperationStateFailed
			op.Error = err.Error()
		}
		return nil
	})
	return op
}

func (m *RobotManager) RecentOperation() robotcap.OperationStatus {
	var op robotcap.OperationStatus
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerOperation, "recent_operation", func() error {
		if len(m.operations) > 0 {
			op = m.operations[0]
		}
		return nil
	})
	return op
}

func (m *RobotManager) OperationStatus() []robotcap.OperationStatus {
	var out []robotcap.OperationStatus
	_ = m.lockHub().WithResource(lockScopeScheduler, lockResourceSchedulerOperation, "operation_status", func() error {
		out = make([]robotcap.OperationStatus, len(m.operations))
		copy(out, m.operations)
		return nil
	})
	return out
}

func (m *RobotManager) beginStructuralOp(op string) func() {
	if strings.TrimSpace(op) == "" {
		op = "unknown"
	}
	m.autoMu.Lock()
	m.structuralOpGeneration++
	generation := m.structuralOpGeneration
	m.structuralOp = op
	m.structuralOpStarted = time.Now()
	m.autoMu.Unlock()
	robotLogf("[RobotLifecycle] op=%s state=begin\n", op)
	return func() {
		m.autoMu.Lock()
		if m.structuralOp == op && m.structuralOpGeneration == generation {
			m.structuralOp = ""
			m.structuralOpStarted = time.Time{}
		}
		m.autoMu.Unlock()
		robotLogf("[RobotLifecycle] op=%s state=end\n", op)
	}
}

func (m *RobotManager) structuralOperation() (string, time.Time, bool) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.structuralOp, m.structuralOpStarted, m.structuralOp != ""
}

func (m *RobotManager) beginActorContainerOp(op string) func() {
	m.actorMutationMu.Lock()
	if strings.TrimSpace(op) == "" {
		op = "actor_container"
	}
	m.autoMu.Lock()
	m.actorContainerOpGeneration++
	generation := m.actorContainerOpGeneration
	m.actorContainerOp = op
	m.actorContainerOpStarted = time.Now()
	m.autoMu.Unlock()
	robotLogf("[RobotLifecycle] actor_container op=%s state=begin\n", op)
	return func() {
		m.autoMu.Lock()
		if m.actorContainerOp == op && m.actorContainerOpGeneration == generation {
			m.actorContainerOp = ""
			m.actorContainerOpStarted = time.Time{}
		}
		m.autoMu.Unlock()
		robotLogf("[RobotLifecycle] actor_container op=%s state=end\n", op)
		m.actorMutationMu.Unlock()
	}
}

func (m *RobotManager) actorContainerOperation() (string, time.Time, bool) {
	m.autoMu.Lock()
	defer m.autoMu.Unlock()
	return m.actorContainerOp, m.actorContainerOpStarted, m.actorContainerOp != ""
}
