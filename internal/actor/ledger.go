package actor

import (
	"sort"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/lockhub"
)

type Ledger struct {
	indexMu    lockhub.RWLocker
	actors     map[int]*Actor
	uidActors  map[int]*Actor
	draining   map[int]*Actor
	blockedUID map[int]struct{}
	quarantine map[int]struct{}
	nextSlotID int
}

type LeaseSnapshot struct {
	UID     int
	Actor   *Actor
	Blocked bool
}

type LedgerCounts struct {
	Auto           int
	Leased         int
	Idle           int
	Draining       int
	Releasing      int
	Blocked        int
	StateIdle      int
	StateAssigned  int
	StateOnline    int
	StateRunning   int
	StateBusy      int
	StateReleasing int
}

func NewLedger() Ledger {
	return Ledger{
		actors:     make(map[int]*Actor),
		uidActors:  make(map[int]*Actor),
		draining:   make(map[int]*Actor),
		blockedUID: make(map[int]struct{}),
		quarantine: make(map[int]struct{}),
	}
}

func (l *Ledger) LeaseSnapshots() []LeaseSnapshot {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	leases := make([]LeaseSnapshot, 0, len(l.uidActors))
	for uid, actor := range l.uidActors {
		if uid > 0 && actor != nil && l.draining[actor.slotIDValue()] != actor {
			_, blocked := l.blockedUID[uid]
			leases = append(leases, LeaseSnapshot{UID: uid, Actor: actor, Blocked: blocked})
		}
	}
	return leases
}

func (l *Ledger) Counts(now time.Time, rc robotconfig.RuntimeConfig) LedgerCounts {
	counts := LedgerCounts{Blocked: l.BlockedCount()}
	type actorCountView struct {
		actor    *Actor
		snapshot Snapshot
		draining bool
	}
	l.indexMu.RLock()
	actors := make([]actorCountView, 0, len(l.actors))
	for slotID, actor := range l.actors {
		// Take one state snapshot under the index lock; evaluation below then
		// needs no further actor lock acquisitions.
		snapshot := actor.snapshot()
		if snapshot.Mode != ModeAuto {
			continue
		}
		actors = append(actors, actorCountView{
			actor: actor, snapshot: snapshot, draining: l.draining[slotID] == actor,
		})
	}
	l.indexMu.RUnlock()
	statusConfig := StatusConfig{
		BadFailures:            rc.SchedulerBadFailures,
		OnlineConfirmTimeoutMS: rc.OnlineConfirmTimeoutMS,
	}
	for _, view := range actors {
		actor := view.actor
		status := EvaluateStatus(view.snapshot, now, statusConfig, actor.runtimeStatusLookup())
		counts.Auto++
		if status.UID > 0 {
			counts.Leased++
		} else {
			counts.Idle++
		}
		if view.draining {
			counts.Draining++
			counts.Releasing++
			counts.StateReleasing++
			continue
		}
		if status.State == StateReleasing {
			counts.Releasing++
		}
		switch status.State {
		case StateIdle:
			counts.StateIdle++
		case StateOffline:
			counts.StateAssigned++
		case StateAssigned:
			counts.StateAssigned++
		case StateOnline:
			counts.StateOnline++
		case StateRunning:
			counts.StateRunning++
		case StateBusy:
			counts.StateBusy++
		case StateReleasing:
			counts.StateReleasing++
		}
	}
	return counts
}

func (l *Ledger) BlockLeaseIfCurrent(uid int, actor *Actor) bool {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if l.uidActors[uid] != actor {
		return false
	}
	l.blockedUID[uid] = struct{}{}
	return true
}

func (l *Ledger) UnblockUID(uid int) {
	l.indexMu.Lock()
	if _, quarantined := l.quarantine[uid]; !quarantined {
		delete(l.blockedUID, uid)
	}
	l.indexMu.Unlock()
}

// ClearQuarantine releases a quarantined UID once its runtime session has been
// confirmed closed and its robot data cleaned up, allowing the UID to be
// reused again. Quarantine is never cleared implicitly.
func (l *Ledger) ClearQuarantine(uid int) bool {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if _, quarantined := l.quarantine[uid]; !quarantined {
		return false
	}
	delete(l.quarantine, uid)
	delete(l.blockedUID, uid)
	return true
}

func (l *Ledger) IsQuarantinedUID(uid int) bool {
	l.indexMu.RLock()
	_, ok := l.quarantine[uid]
	l.indexMu.RUnlock()
	return ok
}

func (l *Ledger) BlockedUIDs(limit int) []int {
	if limit <= 0 {
		return nil
	}
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	uids := make([]int, 0, limit)
	for uid := range l.blockedUID {
		uids = append(uids, uid)
		if len(uids) >= limit {
			break
		}
	}
	return uids
}

func (l *Ledger) BlockedCount() int {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	return len(l.blockedUID)
}

func (l *Ledger) BlockedUIDSet() map[int]struct{} {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	out := make(map[int]struct{}, len(l.blockedUID))
	for uid := range l.blockedUID {
		out[uid] = struct{}{}
	}
	return out
}

func (l *Ledger) TryLeaseUID(uid int, actor *Actor) bool {
	if uid <= 0 || actor == nil {
		return false
	}
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if l.draining[actor.SlotIDValue()] == actor {
		return false
	}
	if _, blocked := l.blockedUID[uid]; blocked {
		return false
	}
	if _, leased := l.uidActors[uid]; leased {
		return false
	}
	l.uidActors[uid] = actor
	return true
}

func (l *Ledger) UnleaseUID(uid int, actor *Actor) {
	if uid <= 0 {
		return
	}
	l.indexMu.Lock()
	if actor == nil || l.uidActors[uid] == actor || l.uidActors[uid] == nil {
		delete(l.uidActors, uid)
	}
	l.indexMu.Unlock()
}

func (l *Ledger) RemoveLeaseIfActor(uid int, actor *Actor) {
	l.indexMu.Lock()
	if l.uidActors[uid] == actor {
		delete(l.uidActors, uid)
	}
	l.indexMu.Unlock()
}

func (l *Ledger) BlockUID(uid int) {
	if uid <= 0 {
		return
	}
	l.indexMu.Lock()
	l.blockedUID[uid] = struct{}{}
	l.indexMu.Unlock()
}

func (l *Ledger) EnsureAutoActorSlots(runtime RobotRuntime, rc robotconfig.RuntimeConfig, target int, status map[int]robotcap.RuntimeStatus) []*Actor {
	if target < 0 {
		target = 0
	}
	l.indexMu.Lock()
	l.reapDoneDrainingLocked()
	current := 0
	pending := 0
	for slotID, actor := range l.actors {
		if actor.ModeValue() == ModeAuto {
			current++
			if l.draining[slotID] != actor && SnapshotSchedulerPending(actor.Snapshot()) {
				pending++
			}
		}
	}
	addCount := target - current
	if addCount < 0 {
		addCount = 0
	}
	pendingLimit := robotconfig.PendingActorLimit(target, rc)
	if pending >= pendingLimit {
		addCount = 0
	}
	if addCount > robotconfig.ScaleUpBatch(rc) {
		addCount = robotconfig.ScaleUpBatch(rc)
	}
	added := addCount
	for addCount > 0 {
		slotID := l.nextSlotLocked()
		actor := NewActor(slotID, ModeAuto, runtime)
		l.actors[slotID] = actor
		actor.Start()
		addCount--
	}
	current += added
	if current <= target {
		l.indexMu.Unlock()
		return nil
	}
	var candidates []*Actor
	for slotID, actor := range l.actors {
		if actor.ModeValue() == ModeAuto && l.draining[slotID] != actor {
			candidates = append(candidates, actor)
		}
	}
	SortActorsForStop(candidates, status)
	removeCount := current - target
	if removeCount > robotconfig.ScaleDownBatch(current, target) {
		removeCount = robotconfig.ScaleDownBatch(current, target)
	}
	// Lowering the target must not close hundreds of sessions in one burst:
	// the server has to run a full teardown per closed session and its login
	// path starves while that backlog drains.
	if cap := rc.SchedulerScaleDownBatch; cap > 0 && removeCount > cap {
		removeCount = cap
	}
	if removeCount > len(candidates) {
		removeCount = len(candidates)
	}
	extra := candidates[:removeCount]
	for _, actor := range extra {
		l.draining[actor.SlotIDValue()] = actor
	}
	l.indexMu.Unlock()
	return extra
}

func (l *Ledger) ActorPointers() []*Actor {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	actors := make([]*Actor, 0, len(l.actors))
	for _, actor := range l.actors {
		actors = append(actors, actor)
	}
	return actors
}

func (l *Ledger) ActorForUID(uid int) *Actor {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	return l.uidActors[uid]
}

func (l *Ledger) HasUID(uid int) bool {
	if uid <= 0 {
		return false
	}
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	return l.uidActors[uid] != nil
}

func (l *Ledger) ReserveEmptyAutoActor(uid int) (*Actor, bool, bool) {
	if uid <= 0 {
		return nil, false, false
	}
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if _, blocked := l.blockedUID[uid]; blocked {
		return nil, false, false
	}
	if existing := l.uidActors[uid]; existing != nil {
		return existing, true, true
	}
	var actor *Actor
	for slotID, candidate := range l.actors {
		if l.draining[slotID] == candidate {
			continue
		}
		snap := candidate.Snapshot()
		if snap.Mode == ModeAuto && snap.UID <= 0 {
			actor = candidate
			break
		}
	}
	if actor == nil {
		return nil, false, false
	}
	l.uidActors[uid] = actor
	return actor, false, true
}

func (l *Ledger) ReserveManualActor(uid int, runtime RobotRuntime) (*Actor, bool, bool) {
	if uid <= 0 || runtime == nil {
		return nil, false, false
	}
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if _, blocked := l.blockedUID[uid]; blocked {
		return nil, false, false
	}
	if existing := l.uidActors[uid]; existing != nil {
		return existing, true, true
	}
	slotID := l.nextSlotLocked()
	actor := NewActor(slotID, ModeManual, runtime)
	l.actors[slotID] = actor
	l.uidActors[uid] = actor
	actor.Start()
	return actor, false, true
}

func (l *Ledger) AdoptManualActors() int {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	adopted := 0
	for slotID, actor := range l.actors {
		if actor == nil || l.draining[slotID] == actor || actor.ModeValue() != ModeManual {
			continue
		}
		actor.SetMode(ModeAuto)
		adopted++
	}
	return adopted
}

func (l *Ledger) AutoActorPointers() []*Actor {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	actors := make([]*Actor, 0, len(l.actors))
	for slotID, actor := range l.actors {
		if actor.ModeValue() == ModeAuto && l.draining[slotID] != actor {
			actors = append(actors, actor)
		}
	}
	return actors
}

func (l *Ledger) IdleAutoActors() []*Actor {
	l.indexMu.RLock()
	actors := make([]*Actor, 0, len(l.actors))
	for slotID, actor := range l.actors {
		if actor.ModeValue() == ModeAuto && l.draining[slotID] != actor {
			actors = append(actors, actor)
		}
	}
	l.indexMu.RUnlock()

	// Compact the temporary actor view in place. The supervisor calls this on
	// every scale tick, so avoiding a second slice removes steady-state churn.
	out := actors[:0]
	for _, actor := range actors {
		if SnapshotEmpty(actor.Snapshot()) {
			out = append(out, actor)
		}
	}
	return out
}

func (l *Ledger) FilterBlockedRuntimeStatus(status map[int]robotcap.RuntimeStatus) {
	if len(status) == 0 {
		return
	}
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	for uid := range l.blockedUID {
		delete(status, uid)
	}
}

func (l *Ledger) BeginDrainAutoActors() []*Actor {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	actors := make([]*Actor, 0, len(l.actors))
	for slotID, actor := range l.actors {
		if actor.ModeValue() != ModeAuto || l.draining[slotID] == actor {
			continue
		}
		actors = append(actors, actor)
		l.draining[slotID] = actor
	}
	return actors
}

func (l *Ledger) BeginDrainSomeAutoActors(status map[int]robotcap.RuntimeStatus, limit, floor int) []*Actor {
	if limit <= 0 {
		return nil
	}
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	var candidates []*Actor
	for slotID, actor := range l.actors {
		if actor.ModeValue() == ModeAuto && l.draining[slotID] != actor {
			candidates = append(candidates, actor)
		}
	}
	SortActorsForStop(candidates, status)
	if floor < 0 {
		floor = 0
	}
	if len(candidates) <= floor {
		return nil
	}
	maxStop := len(candidates) - floor
	if limit > maxStop {
		limit = maxStop
	}
	if limit > len(candidates) {
		limit = len(candidates)
	}
	actors := candidates[:limit]
	for _, actor := range actors {
		l.draining[actor.SlotIDValue()] = actor
	}
	return actors
}

func (l *Ledger) BeginDrainAllActors() []*Actor {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	actors := make([]*Actor, 0, len(l.actors))
	for slotID, actor := range l.actors {
		actors = append(actors, actor)
		l.draining[slotID] = actor
	}
	return actors
}

func (l *Ledger) BeginDrainUID(uid int) *Actor {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	actor := l.uidActors[uid]
	if actor != nil {
		l.draining[actor.SlotIDValue()] = actor
	}
	return actor
}

func (l *Ledger) BeginDrainUIDs(uids []int) ([]*Actor, []int) {
	if len(uids) == 0 {
		return nil, nil
	}
	seen := make(map[int]struct{}, len(uids))
	actors := make([]*Actor, 0, len(uids))
	missing := make([]int, 0)
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	for _, uid := range uids {
		if uid <= 0 {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		actor := l.uidActors[uid]
		if actor == nil {
			missing = append(missing, uid)
			continue
		}
		l.draining[actor.SlotIDValue()] = actor
		actors = append(actors, actor)
	}
	return actors, missing
}

func (l *Ledger) ReapActor(actor *Actor) bool {
	if actor == nil || !actorDone(actor) {
		return false
	}
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	if l.draining[actor.SlotIDValue()] != actor {
		return true
	}
	return l.reapActorLocked(actor)
}

func (l *Ledger) ReapDoneDraining() int {
	l.indexMu.Lock()
	defer l.indexMu.Unlock()
	return l.reapDoneActorsLocked(true)
}

func (l *Ledger) IsDraining(actor *Actor) bool {
	if actor == nil {
		return false
	}
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	return l.draining[actor.SlotIDValue()] == actor
}

func (l *Ledger) DrainingCount() int {
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	return len(l.draining)
}

func (l *Ledger) reapDoneDrainingLocked() int {
	return l.reapDoneActorsLocked(false)
}

// reapDoneActorsLocked removes all completed actors with one actor scan and
// one lease scan. Unexpected exits keep their UIDs blocked for supervisor
// cleanup; normally drained actors clear any existing block as before.
func (l *Ledger) reapDoneActorsLocked(includeUnexpected bool) int {
	// The bool records whether leases owned by this actor must remain blocked.
	// A draining entry always wins over an unexpected classification.
	deadActors := make(map[*Actor]bool)
	reaped := 0
	if includeUnexpected {
		for slotID, actor := range l.actors {
			if actor == nil || l.draining[slotID] == actor || !actorDone(actor) {
				continue
			}
			delete(l.actors, slotID)
			deadActors[actor] = true
			reaped++
		}
	}
	for slotID, actor := range l.draining {
		if actor == nil || !actorDone(actor) {
			continue
		}
		delete(l.draining, slotID)
		if l.actors[slotID] == actor {
			delete(l.actors, slotID)
		}
		if _, exists := deadActors[actor]; !exists {
			reaped++
		}
		deadActors[actor] = actor.Snapshot().Quarantined
	}
	if len(deadActors) == 0 {
		return reaped
	}
	for uid, leased := range l.uidActors {
		block, dead := deadActors[leased]
		if !dead {
			continue
		}
		delete(l.uidActors, uid)
		if block && uid > 0 {
			l.blockedUID[uid] = struct{}{}
			l.quarantine[uid] = struct{}{}
		} else {
			delete(l.blockedUID, uid)
		}
	}
	return reaped
}

func (l *Ledger) reapActorLocked(actor *Actor) bool {
	slotID := actor.SlotIDValue()
	if l.draining[slotID] != actor || !actorDone(actor) {
		return false
	}
	delete(l.draining, slotID)
	if l.actors[slotID] == actor {
		delete(l.actors, slotID)
	}
	for uid, leased := range l.uidActors {
		if leased == actor {
			delete(l.uidActors, uid)
			if actor.Snapshot().Quarantined {
				l.blockedUID[uid] = struct{}{}
				l.quarantine[uid] = struct{}{}
			} else {
				delete(l.blockedUID, uid)
			}
		}
	}
	return true
}

func actorDone(actor *Actor) bool {
	select {
	case <-actor.Done():
		return true
	default:
		return false
	}
}

func (l *Ledger) ActorOwnsUID(uid int) bool {
	if uid <= 0 {
		return false
	}
	if l.ActorForUID(uid) != nil {
		return true
	}
	l.indexMu.RLock()
	defer l.indexMu.RUnlock()
	for _, actor := range l.actors {
		if actor.UIDValue() == uid {
			return true
		}
	}
	return false
}

func (l *Ledger) nextSlotLocked() int {
	l.nextSlotID++
	return l.nextSlotID
}

func SortActorsForStop(actors []*Actor, status map[int]robotcap.RuntimeStatus) {
	type stopEntry struct {
		actor    *Actor
		uid      int
		slot     int
		priority int
	}
	// Snapshot every key once: taking actor locks inside the comparator makes
	// the sort O(n log n) lock acquisitions on the scheduler hot path.
	entries := make([]stopEntry, 0, len(actors))
	for _, actor := range actors {
		uid := actor.UIDValue()
		entries = append(entries, stopEntry{
			actor:    actor,
			uid:      uid,
			slot:     actor.SlotIDValue(),
			priority: StopPriority(uid, status),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		if left.priority != right.priority {
			return left.priority < right.priority
		}
		if left.uid <= 0 || right.uid <= 0 {
			if left.uid != right.uid {
				return left.uid <= 0
			}
			return left.slot > right.slot
		}
		if left.uid != right.uid {
			return left.uid > right.uid
		}
		return left.slot > right.slot
	})
	for index := range entries {
		actors[index] = entries[index].actor
	}
}
