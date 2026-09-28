package store

import (
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/lockhub"
	"robot/internal/foundation/mathx"
)

const (
	pointClaimTTL       = 2 * time.Minute
	pointCleanupMargin  = 30 * time.Second
	pointSaveMax        = 100
	pointSaveAge        = 30 * time.Second
	PointFailRetry      = 6 * time.Minute
	pointFailureBurst   = time.Minute
	pointEvidenceWindow = 2 * time.Minute
	pointEvidenceLimit  = 3
	storeProbeInterval  = 2 * time.Minute
	probeExpansionBurst = 3
)

const (
	PointStatusUnknown = "unknown"
	PointStatusSuccess = "success"
	PointStatusFailed  = "failed"
)

const (
	PointSourceUnknown     = "grid_unknown"
	PointSourceSuccess     = "grid_success"
	PointSourceFailedRetry = "grid_failed_retry"
	PointSourceProbe       = "grid_probe"
)

const (
	StoreReasonAck                 = "store_ack"
	StoreReasonDisjointAck         = "disjoint_ack"
	StoreReasonFailed              = "store_failed"
	StoreReasonOnlineFailed        = "store_online_failed"
	StoreReasonOnlineAttemptFailed = "online_failed"
	StoreReasonStartFailed         = "store_start_failed"
	StoreReasonNotConfirmed        = "store_not_confirmed"
	StoreReasonPrepareFailed       = "prepare_failed"
	StoreReasonSetAreaFailed       = "set_area_failed"
	StoreReasonCancelled           = "cancelled"
	StoreReasonRuntimeStopped      = "runtime_stopped"
	StoreReasonDisplayWaitFailed   = "display_wait_failed"
	StoreReasonInventoryNotReady   = "store_inventory_not_ready"
	StoreReasonErr011              = "store_err_0x11"
	StoreReasonErr052              = "store_err_0x52"
	StoreReasonErr052Zone          = "store_err_0x52_zone"
)

type Position struct {
	Village int
	Area    int
	X       int
	Y       int
	Source  string
	PointID string
}

type PointCoordinator struct {
	pointMu              lockhub.Locker
	cacheMu              lockhub.Locker
	flushMu              lockhub.Locker
	activeSave           activePointPersistence
	configDir            string
	sourcePath           string
	sourceName           string
	sourceMD5            string
	generatedAt          string
	points               []GridPoint
	byID                 map[string]int
	byArea               map[areaKey][]int
	areaOrder            []areaKey
	areaCursor           int
	probeAreaCursor      int
	probeExpansionCursor int
	probeExpansionStreak int
	lastProbeAt          time.Time
	probeInterval        time.Duration
	probeAttempted       map[string]bool
	pointClaims          map[string]pointClaim
	pointOccupancy       map[areaKey]map[occupancyCell]map[string]pointOccupancy
	pointEvidence        map[pointEvidenceKey]map[int]time.Time
	pointCooldown        map[string]time.Time
	packedPoints         map[string]bool
	failedPoints         map[string]bool
	successPoints        map[string]bool
	triedPoints          map[string]bool
	dirtyCount           int
	activeDirty          int
	lastCacheSave        time.Time
	logf                 func(string, ...interface{})
}

type pointClaim struct {
	UID        int
	ExpiresAt  time.Time
	ClaimUntil time.Time
	Lease      time.Duration
	ReuseAfter time.Duration
}

func (c *PointCoordinator) HasArea(village, area int) bool {
	if c == nil {
		return false
	}
	c.pointMu.Lock()
	defer c.pointMu.Unlock()
	return len(c.byArea[areaKey{village, area}]) > 0
}

func NewPointCoordinator(stateDir, mapCatalogPath string, logf func(string, ...interface{})) *PointCoordinator {
	return newPointCoordinator(stateDir, mapCatalogPath, logf)
}

func newPointCoordinator(configDir, sourcePath string, logf func(string, ...interface{})) *PointCoordinator {
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	c := &PointCoordinator{
		configDir:      configDir,
		sourcePath:     sourcePath,
		byID:           make(map[string]int),
		byArea:         make(map[areaKey][]int),
		pointClaims:    make(map[string]pointClaim),
		pointOccupancy: make(map[areaKey]map[occupancyCell]map[string]pointOccupancy),
		pointEvidence:  make(map[pointEvidenceKey]map[int]time.Time),
		pointCooldown:  make(map[string]time.Time),
		packedPoints:   make(map[string]bool),
		failedPoints:   make(map[string]bool),
		successPoints:  make(map[string]bool),
		triedPoints:    make(map[string]bool),
		probeAttempted: make(map[string]bool),
		lastCacheSave:  time.Now(),
		probeInterval:  storeProbeInterval,
		logf:           logf,
	}
	if configDir != "" {
		if err := c.load(); err != nil {
			c.logf("[StorePoint] load_failed err=%v\n", err)
		}
	}
	return c
}

func (c *PointCoordinator) Claim(uid int) (Position, bool) {
	return c.ClaimWithLease(uid, pointClaimTTL)
}

func (c *PointCoordinator) ClaimWithLease(uid int, lease time.Duration) (Position, bool) {
	lease = normalizePointLease(lease)
	return c.claim(uid, lease, lease, nil, false)
}

// ClaimForStore keeps cleanup ownership for the longest possible configured
// store lifetime while making a successful point reusable at this UID's exact
// staggered store expiry.
func (c *PointCoordinator) ClaimForStore(uid, storeDurationSec int) (Position, bool) {
	return c.ClaimForStoreWhere(uid, storeDurationSec, nil)
}

// ClaimForStoreWhere applies destination policy before a point is claimed.
func (c *PointCoordinator) ClaimForStoreWhere(uid, storeDurationSec int, allowed func(Position) bool) (Position, bool) {
	return c.claimForStoreWhere(uid, storeDurationSec, allowed, false)
}

// ClaimForStoreInAreaWhere claims a point inside one preferred area before the
// caller falls back to the global pool. Robots whose current area still has
// free points avoid a server-side area transition, which is the most
// load-sensitive step of a disjoint-store attempt.
func (c *PointCoordinator) ClaimForStoreInAreaWhere(uid, storeDurationSec int, village, area int, allowed func(Position) bool) (Position, bool) {
	if c == nil {
		return Position{}, false
	}
	lease := normalizePointLease(StorePointLeaseDuration(storeDurationSec))
	reuseAfter := robotconfig.StoreDurationForUID(storeDurationSec, uid)
	if reuseAfter < 0 {
		reuseAfter = 0
	}
	c.pointMu.Lock()
	defer c.pointMu.Unlock()
	now := time.Now()
	c.clearExpiredClaims(now)
	key := areaKey{village, area}
	if len(c.byArea[key]) == 0 {
		return Position{}, false
	}
	stages := []func() (Position, bool){
		func() (Position, bool) {
			return c.claimFromArea(uid, key, PointStatusSuccess, true, now, lease, reuseAfter, allowed)
		},
		func() (Position, bool) {
			return c.claimFromArea(uid, key, PointStatusSuccess, false, now, lease, reuseAfter, allowed)
		},
		func() (Position, bool) {
			return c.claimFromArea(uid, key, PointStatusUnknown, true, now, lease, reuseAfter, allowed)
		},
		func() (Position, bool) {
			return c.claimFailedFromArea(uid, key, true, true, now, lease, reuseAfter, allowed)
		},
		func() (Position, bool) {
			return c.claimFromArea(uid, key, PointStatusUnknown, false, now, lease, reuseAfter, allowed)
		},
		func() (Position, bool) {
			return c.claimFailedFromArea(uid, key, false, false, now, lease, reuseAfter, allowed)
		},
	}
	for _, stage := range stages {
		if pos, ok := stage(); ok {
			return pos, true
		}
	}
	return Position{}, false
}

// ClaimForItemStoreWhere permits one globally rate-limited probe of an
// unmarked PVF town area. Disjoint stores never call this path because a
// successful special store is not evidence that an area is publicly usable.
func (c *PointCoordinator) ClaimForItemStoreWhere(uid, storeDurationSec int, allowed func(Position) bool) (Position, bool) {
	return c.claimForStoreWhere(uid, storeDurationSec, allowed, true)
}

func (c *PointCoordinator) claimForStoreWhere(uid, storeDurationSec int, allowed func(Position) bool, allowProbe bool) (Position, bool) {
	cleanupLease := StorePointLeaseDuration(storeDurationSec)
	reuseAfter := robotconfig.StoreDurationForUID(storeDurationSec, uid)
	if reuseAfter < 0 {
		reuseAfter = 0
	}
	return c.claim(uid, cleanupLease, reuseAfter, allowed, allowProbe)
}

func (c *PointCoordinator) claim(uid int, lease, reuseAfter time.Duration, allowed func(Position) bool, allowProbe bool) (Position, bool) {
	c.pointMu.Lock()
	defer c.pointMu.Unlock()
	now := time.Now()
	lease = normalizePointLease(lease)
	c.clearExpiredClaims(now)
	if len(c.areaOrder) == 0 {
		return Position{}, false
	}
	if allowProbe && (c.lastProbeAt.IsZero() || now.Sub(c.lastProbeAt) >= c.probeInterval) {
		if pos, ok := c.claimProbe(uid, now, lease, reuseAfter, allowed); ok {
			c.lastProbeAt = now
			return pos, true
		}
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFromArea(uid, area, PointStatusSuccess, true, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFromArea(uid, area, PointStatusSuccess, false, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFromArea(uid, area, PointStatusUnknown, true, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFailedFromArea(uid, area, true, true, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFromArea(uid, area, PointStatusUnknown, false, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	if pos, ok := c.claimAcrossAreas(func(area areaKey) (Position, bool) {
		return c.claimFailedFromArea(uid, area, false, false, now, lease, reuseAfter, allowed)
	}); ok {
		return pos, true
	}
	return Position{}, false
}

func (c *PointCoordinator) claimProbe(uid int, now time.Time, lease, reuseAfter time.Duration, allowed func(Position) bool) (Position, bool) {
	if c.probeExpansionStreak < probeExpansionBurst {
		if pos, ok := c.claimProbeExpansion(uid, now, lease, reuseAfter, allowed); ok {
			c.probeExpansionStreak++
			return pos, true
		}
	}
	if pos, ok := c.claimProbeDiscovery(uid, now, lease, reuseAfter, allowed); ok {
		c.probeExpansionStreak = 0
		return pos, true
	}
	if pos, ok := c.claimProbeExpansion(uid, now, lease, reuseAfter, allowed); ok {
		c.probeExpansionStreak++
		return pos, true
	}
	return Position{}, false
}

func (c *PointCoordinator) claimProbeDiscovery(uid int, now time.Time, lease, reuseAfter time.Duration, allowed func(Position) bool) (Position, bool) {
	for scanned := 0; scanned < len(c.areaOrder); scanned++ {
		area := c.areaOrder[c.probeAreaCursor%len(c.areaOrder)]
		c.probeAreaCursor = (c.probeAreaCursor + 1) % len(c.areaOrder)
		for _, idx := range c.byArea[area] {
			pt := c.points[idx]
			if !c.probePointAvailable(pt, area, now, lease, allowed) {
				continue
			}
			pos := Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, Source: PointSourceProbe, PointID: pt.ID}
			c.probeAttempted[pt.ID] = true
			c.setPointClaimLocked(pt.ID, newPointClaim(uid, now, lease, reuseAfter))
			c.logf("[StoreProbe] discover point=%s village=%d area=%d x=%d y=%d uid=%d\n", pt.ID, pt.Village, pt.Area, pt.X, pt.Y, uid)
			return pos, true
		}
	}
	return Position{}, false
}

func (c *PointCoordinator) claimProbeExpansion(uid int, now time.Time, lease, reuseAfter time.Duration, allowed func(Position) bool) (Position, bool) {
	for scanned := 0; scanned < len(c.areaOrder); scanned++ {
		area := c.areaOrder[c.probeExpansionCursor%len(c.areaOrder)]
		c.probeExpansionCursor = (c.probeExpansionCursor + 1) % len(c.areaOrder)
		anchors := c.verifiedProbeAnchors(area)
		if len(anchors) == 0 {
			continue
		}
		bestIdx, bestDistance := -1, int64(^uint64(0)>>1)
		for _, idx := range c.byArea[area] {
			pt := c.points[idx]
			if !c.probePointAvailable(pt, area, now, lease, allowed) {
				continue
			}
			distance := nearestProbeDistanceSquared(pt, anchors)
			if bestIdx < 0 || distance < bestDistance {
				bestIdx, bestDistance = idx, distance
			}
		}
		if bestIdx < 0 {
			continue
		}
		pt := c.points[bestIdx]
		pos := Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, Source: PointSourceProbe, PointID: pt.ID}
		c.probeAttempted[pt.ID] = true
		c.setPointClaimLocked(pt.ID, newPointClaim(uid, now, lease, reuseAfter))
		c.logf("[StoreProbe] expand point=%s village=%d area=%d x=%d y=%d uid=%d distance2=%d\n", pt.ID, pt.Village, pt.Area, pt.X, pt.Y, uid, bestDistance)
		return pos, true
	}
	return Position{}, false
}

func (c *PointCoordinator) probePointAvailable(pt GridPoint, area areaKey, now time.Time, lease time.Duration, allowed func(Position) bool) bool {
	if !pt.Probe || c.probeAttempted[pt.ID] || c.triedPoints[pt.ID] || c.failedPoints[pt.ID] || !c.packedPoints[pt.ID] {
		return false
	}
	pos := Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, Source: PointSourceProbe, PointID: pt.ID}
	if allowed != nil && !allowed(pos) {
		return false
	}
	return !c.positionRecentlyOccupied(area, pt, now) && !c.recentFailedPoint(pt, now, lease)
}

func (c *PointCoordinator) verifiedProbeAnchors(area areaKey) []GridPoint {
	var anchors []GridPoint
	for _, idx := range c.byArea[area] {
		pt := c.points[idx]
		if pt.ProbeVerified && !pt.Probe {
			anchors = append(anchors, pt)
		}
	}
	return anchors
}

func nearestProbeDistanceSquared(point GridPoint, anchors []GridPoint) int64 {
	best := int64(^uint64(0) >> 1)
	for _, anchor := range anchors {
		dx := int64(point.X - anchor.X)
		dy := int64(point.Y - anchor.Y)
		distance := dx*dx + dy*dy
		if distance < best {
			best = distance
		}
	}
	return best
}

func (c *PointCoordinator) claimAcrossAreas(fn func(areaKey) (Position, bool)) (Position, bool) {
	for scanned := 0; scanned < len(c.areaOrder); scanned++ {
		areaKey := c.areaOrder[c.areaCursor%len(c.areaOrder)]
		c.areaCursor = (c.areaCursor + 1) % len(c.areaOrder)
		if pos, ok := fn(areaKey); ok {
			return pos, true
		}
	}
	return Position{}, false
}

func (c *PointCoordinator) claimFromArea(uid int, area areaKey, status string, packedOnly bool, now time.Time, lease, reuseAfter time.Duration, allowed func(Position) bool) (Position, bool) {
	for _, idx := range c.byArea[area] {
		pt := c.points[idx]
		if pt.Probe {
			continue
		}
		pos := Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, PointID: pt.ID}
		if allowed != nil && !allowed(pos) {
			continue
		}
		if status == PointStatusSuccess {
			if !c.successPoints[pt.ID] {
				continue
			}
		} else if status == PointStatusUnknown && c.triedPoints[pt.ID] {
			continue
		}
		if packedOnly && !c.packedPoints[pt.ID] {
			continue
		}
		if c.failedPoints[pt.ID] {
			continue
		}
		if c.positionRecentlyOccupied(area, pt, now) {
			continue
		}
		if c.recentFailedPoint(pt, now, lease) {
			continue
		}
		claim := newPointClaim(uid, now, lease, reuseAfter)
		c.setPointClaimLocked(pt.ID, claim)
		source := PointSourceUnknown
		if status == PointStatusSuccess {
			source = PointSourceSuccess
		}
		pos.Source = source
		return pos, true
	}
	return Position{}, false
}

func (c *PointCoordinator) claimFailedFromArea(uid int, area areaKey, packedOnly, requireAreaSuccess bool, now time.Time, lease, reuseAfter time.Duration, allowed func(Position) bool) (Position, bool) {
	if requireAreaSuccess && !c.areaHasUsableSuccess(area, now, lease, allowed) {
		return Position{}, false
	}
	for _, idx := range c.byArea[area] {
		pt := c.points[idx]
		if pt.Probe {
			continue
		}
		pos := Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, Source: PointSourceFailedRetry, PointID: pt.ID}
		if allowed != nil && !allowed(pos) {
			continue
		}
		if packedOnly && !c.packedPoints[pt.ID] {
			continue
		}
		if !c.failedPoints[pt.ID] || c.recentFailedPoint(pt, now, lease) {
			continue
		}
		if c.positionRecentlyOccupied(area, pt, now) {
			continue
		}
		claim := newPointClaim(uid, now, lease, reuseAfter)
		c.setPointClaimLocked(pt.ID, claim)
		return pos, true
	}
	return Position{}, false
}

func (c *PointCoordinator) areaHasUsableSuccess(area areaKey, now time.Time, lease time.Duration, allowed func(Position) bool) bool {
	for _, idx := range c.byArea[area] {
		pt := c.points[idx]
		if pt.Probe {
			continue
		}
		if allowed != nil && !allowed(Position{Village: pt.Village, Area: pt.Area, X: pt.X, Y: pt.Y, PointID: pt.ID}) {
			continue
		}
		if !c.successPoints[pt.ID] {
			continue
		}
		if c.recentFailedPoint(pt, now, lease) {
			continue
		}
		return true
	}
	return false
}

func (c *PointCoordinator) recentFailedPoint(pt GridPoint, now time.Time, lease time.Duration) bool {
	if c.pointCoolingDownLocked(pt.ID, now) {
		return true
	}
	if !pointPenaltyReason(pt.LastReason) {
		return false
	}
	retry, permanent := pointFailureRetry(pt.LastReason, lease)
	if permanent {
		return true
	}
	if pt.LastResultAt == "" {
		return true
	}
	last, err := time.Parse(time.RFC3339, pt.LastResultAt)
	if err != nil {
		return true
	}
	return now.Sub(last) < retry
}

func (c *PointCoordinator) Report(uid int, pos Position, ok bool, reason string) {
	if pos.PointID == "" {
		return
	}
	c.pointMu.Lock()
	nowTime := time.Now()
	c.clearExpiredClaims(nowTime)
	existing, claimed := c.pointClaims[pos.PointID]
	if claimed && existing.UID != uid {
		c.pointMu.Unlock()
		return
	}
	ownedClaim := claimed && existing.UID == uid
	successReason := pointSuccessReason(reason)
	if ok {
		claim := existing
		if !ownedClaim {
			claim = newPointClaim(uid, nowTime, pointClaimTTL, pointClaimTTL)
		} else {
			claim.ExpiresAt = nowTime.Add(claim.Lease)
			claim.ClaimUntil = nowTime.Add(claim.Lease)
		}
		if successReason {
			claim.ClaimUntil = time.Time{}
		}
		c.setPointClaimLocked(pos.PointID, claim)
	} else {
		c.discardPositionLocked(uid, pos)
	}
	penalty := pointPenaltyReason(reason)
	if !ok && !penalty {
		c.pointMu.Unlock()
		return
	}
	if ok || penalty {
		c.triedPoints[pos.PointID] = true
	}
	idx, hasPoint := c.byID[pos.PointID]
	now := nowTime.Format(time.RFC3339)
	activeChanged := false
	if ok {
		c.clearPointEvidenceLocked(pos.PointID)
		delete(c.pointCooldown, pos.PointID)
		delete(c.failedPoints, pos.PointID)
		probePromoted := hasPoint && c.points[idx].Probe && reason == StoreReasonAck
		if !hasPoint || !c.points[idx].Probe || probePromoted {
			c.successPoints[pos.PointID] = true
		}
		if successReason {
			claim := c.pointClaims[pos.PointID]
			c.setPointSuccessOccupancyLocked(pos.PointID, nowTime.Add(claim.ReuseAfter))
			activeChanged = true
		} else {
			activeChanged = c.clearPointSuccessOccupancyLocked(pos.PointID)
		}
		if hasPoint {
			if probePromoted {
				c.points[idx].Probe = false
				c.points[idx].ProbeVerified = true
				c.logf("[StoreProbe] promoted point=%s village=%d area=%d x=%d y=%d uid=%d\n", pos.PointID, pos.Village, pos.Area, pos.X, pos.Y, uid)
			}
			c.points[idx].Status = PointStatusSuccess
			c.points[idx].Success++
			c.points[idx].LastUID = uid
			c.points[idx].LastReason = reason
			c.points[idx].LastResultAt = now
		}
	} else {
		if ownedClaim {
			activeChanged = c.clearPointSuccessOccupancyLocked(pos.PointID)
		}
		if hasPoint {
			if !penalty {
				c.points[idx].LastUID = uid
				c.points[idx].LastReason = reason
				c.points[idx].LastResultAt = now
			} else if c.points[idx].Success > 0 {
				c.successPoints[pos.PointID] = true
				c.points[idx].Status = PointStatusSuccess
			} else {
				c.failedPoints[pos.PointID] = true
				c.points[idx].Status = PointStatusFailed
			}
			if penalty {
				c.points[idx].Failed++
				c.points[idx].LastUID = uid
				c.points[idx].LastReason = reason
				c.points[idx].LastResultAt = now
			}
		} else if penalty {
			c.failedPoints[pos.PointID] = true
		}
		if penalty && restrictivePointReason(reason) {
			c.clearPointEvidenceLocked(pos.PointID)
			delete(c.pointCooldown, pos.PointID)
			c.markRestrictiveZoneLocked(uid, pos, now)
			c.rebuildPackedPointsLocked()
		}
	}
	c.dirtyCount++
	if activeChanged {
		c.activeDirty++
	}
	shouldSave := c.cacheSaveDueLocked()
	c.pointMu.Unlock()
	if activeChanged {
		c.scheduleActiveOccupancySave()
	}
	if shouldSave {
		c.saveCache()
	}
}

// Discard releases an unconfirmed point claim without changing persisted point
// history. It is used for cancellation and session-scoped failures.
func (c *PointCoordinator) Discard(uid int, pos Position) {
	if pos.PointID == "" {
		return
	}
	c.pointMu.Lock()
	defer c.pointMu.Unlock()
	c.discardPositionLocked(uid, pos)
}

func (c *PointCoordinator) ReleaseUID(uid int) {
	if uid <= 0 {
		return
	}
	c.pointMu.Lock()
	releasedActive := 0
	for id, claim := range c.pointClaims {
		if claim.UID != uid {
			continue
		}
		c.clearPointClaimLocked(id)
		if c.clearPointSuccessOccupancyLocked(id) {
			releasedActive++
		}
	}
	if releasedActive > 0 {
		c.activeDirty += releasedActive
	}
	c.pointMu.Unlock()
	if releasedActive > 0 {
		c.scheduleActiveOccupancySave()
	}
}

func (c *PointCoordinator) discardPositionLocked(uid int, pos Position) {
	if claim, ok := c.pointClaims[pos.PointID]; ok && (uid <= 0 || claim.UID == uid) {
		c.clearPointClaimLocked(pos.PointID)
	}
}

func StorePointLeaseDuration(storeDurationSec int) time.Duration {
	maxDuration := robotconfig.MaxStoreDurationSec(storeDurationSec)
	return normalizePointLease(time.Duration(maxDuration)*time.Second + pointCleanupMargin)
}

func normalizePointLease(lease time.Duration) time.Duration {
	if lease < pointClaimTTL {
		return pointClaimTTL
	}
	return lease
}

func newPointClaim(uid int, now time.Time, lease, reuseAfter time.Duration) pointClaim {
	lease = normalizePointLease(lease)
	return pointClaim{
		UID:        uid,
		ExpiresAt:  now.Add(lease),
		ClaimUntil: now.Add(lease),
		Lease:      lease,
		ReuseAfter: reuseAfter,
	}
}

func pointPenaltyReason(reason string) bool {
	switch reason {
	case "store_err_0x38", "store_err_0x3e", StoreReasonErr052, StoreReasonErr052Zone,
		"disjoint_err_0x14", "disjoint_err_0x3e", "disjoint_err_0x52", "disjoint_err_0xbe",
		"enchant_err_0x52", "enchant_err_0xbe":
		return true
	default:
		return false
	}
}

func pointFailureRetry(reason string, lease time.Duration) (time.Duration, bool) {
	switch reason {
	case StoreReasonErr052, StoreReasonErr052Zone, "disjoint_err_0x52", "enchant_err_0x52":
		return 0, true
	case "store_err_0x38", "disjoint_err_0x14", "disjoint_err_0xbe", "enchant_err_0xbe":
		return normalizePointLease(lease), false
	default:
		return PointFailRetry, false
	}
}

func restrictivePointReason(reason string) bool {
	return reason == StoreReasonErr052 || reason == "disjoint_err_0x52" || reason == "enchant_err_0x52"
}

func ambiguousPointFailureReason(reason string) bool {
	return pointPenaltyReason(reason) && !restrictivePointReason(reason) && reason != StoreReasonErr052Zone
}

func pointSuccessReason(reason string) bool {
	return reason == StoreReasonAck || reason == StoreReasonDisjointAck
}

func (c *PointCoordinator) SuccessCount() int {
	c.pointMu.Lock()
	defer c.pointMu.Unlock()
	return len(c.successPoints)
}

func (c *PointCoordinator) markRestrictiveZoneLocked(uid int, pos Position, now string) {
	area := areaKey{pos.Village, pos.Area}
	maxDX := RestrictHalfX * 2
	maxDY := RestrictHalfY * 2
	for _, idx := range c.byArea[area] {
		pt := &c.points[idx]
		if pt.Success > 0 || pt.Status == PointStatusSuccess {
			continue
		}
		if mathx.AbsInt(pt.X-pos.X) > maxDX || mathx.AbsInt(pt.Y-pos.Y) > maxDY {
			continue
		}
		c.failedPoints[pt.ID] = true
		c.triedPoints[pt.ID] = true
		pt.Status = PointStatusFailed
		pt.LastUID = uid
		pt.LastReason = StoreReasonErr052Zone
		pt.LastResultAt = now
	}
}

func (c *PointCoordinator) clearExpiredClaims(now time.Time) {
	for id, claim := range c.pointClaims {
		if now.After(claim.ExpiresAt) {
			c.clearPointClaimLocked(id)
		}
	}
	for pointID, until := range c.pointCooldown {
		if !now.Before(until) {
			delete(c.pointCooldown, pointID)
		}
	}
}
