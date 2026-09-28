package actor

import (
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
)

func BenchmarkLedgerCounts550(b *testing.B) {
	ledger := NewLedger()
	runtime := &partyWaitRuntime{status: robotcap.RuntimeStatus{StateName: robotcap.RuntimeStateRunning}}
	ledger.indexMu.Lock()
	for i := 0; i < 550; i++ {
		actor := NewActor(i+1, ModeAuto, runtime)
		actor.resetForUID(17000000 + i)
		ledger.actors[i+1] = actor
		ledger.uidActors[17000000+i] = actor
	}
	ledger.indexMu.Unlock()
	rc := robotconfig.RuntimeConfig{SchedulerBadFailures: 3, OnlineConfirmTimeoutMS: 60000}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ledger.Counts(time.Now(), rc)
	}
}

func testLedgerActor(slotID int, mode Mode, uid int) *Actor {
	actor := NewActor(slotID, mode, nil)
	if uid > 0 {
		actor.resetForUID(uid)
	}
	return actor
}

func TestEnsureAutoActorSlotsHonorsScaleDownBatchCap(t *testing.T) {
	ledger := NewLedger()
	for i := 0; i < 200; i++ {
		addTestLedgerActor(&ledger, testLedgerActor(i+1, ModeAuto, 17000000+i))
	}
	extra := ledger.EnsureAutoActorSlots(nil, robotconfig.RuntimeConfig{SchedulerScaleDownBatch: 5, SchedulerOnlineBatchSize: 120}, 50, nil)
	if len(extra) != 5 {
		t.Fatalf("scale down got %d want configured cap 5", len(extra))
	}

	ledger = NewLedger()
	for i := 0; i < 200; i++ {
		addTestLedgerActor(&ledger, testLedgerActor(i+1, ModeAuto, 17000000+i))
	}
	extra = ledger.EnsureAutoActorSlots(nil, robotconfig.RuntimeConfig{SchedulerScaleDownBatch: 50, SchedulerOnlineBatchSize: 120}, 50, nil)
	if len(extra) != 8 {
		t.Fatalf("scale down got %d want computed batch 8", len(extra))
	}
}

func addTestLedgerActor(ledger *Ledger, actor *Actor) {
	ledger.actors[actor.slotIDValue()] = actor
}

func TestLedgerReserveEmptyAutoActor(t *testing.T) {
	ledger := NewLedger()
	a1 := testLedgerActor(1, ModeAuto, 0)
	a2 := testLedgerActor(2, ModeAuto, 202)
	addTestLedgerActor(&ledger, a1)
	addTestLedgerActor(&ledger, a2)

	actor, existing, ok := ledger.ReserveEmptyAutoActor(101)
	if !ok || existing || actor != a1 {
		t.Fatalf("reserve got ok=%v existing=%v actor=%v want empty actor 1", ok, existing, actor)
	}
	if got := ledger.ActorForUID(101); got != a1 {
		t.Fatalf("leased actor got %v want %v", got, a1)
	}

	actor, existing, ok = ledger.ReserveEmptyAutoActor(101)
	if !ok || !existing || actor != a1 {
		t.Fatalf("idempotent reserve got ok=%v existing=%v actor=%v want existing actor 1", ok, existing, actor)
	}

	ledger.BlockUID(303)
	if actor, existing, ok := ledger.ReserveEmptyAutoActor(303); ok || existing || actor != nil {
		t.Fatalf("blocked reserve got ok=%v existing=%v actor=%v want rejected", ok, existing, actor)
	}
}

func TestLedgerBeginDrainUIDsRetainsActorsAndLeases(t *testing.T) {
	ledger := NewLedger()
	a1 := testLedgerActor(1, ModeAuto, 0)
	a2 := testLedgerActor(2, ModeAuto, 0)
	addTestLedgerActor(&ledger, a1)
	addTestLedgerActor(&ledger, a2)
	ledger.TryLeaseUID(101, a1)
	ledger.TryLeaseUID(102, a2)
	ledger.BlockUID(101)

	actors, missing := ledger.BeginDrainUIDs([]int{101, 102, 102, 999})
	if len(actors) != 2 {
		t.Fatalf("detached actors got %d want 2", len(actors))
	}
	if len(missing) != 1 || missing[0] != 999 {
		t.Fatalf("missing got %v want [999]", missing)
	}
	if actorCount, leases := len(ledger.actors), len(ledger.uidActors); actorCount != 2 || leases != 2 {
		t.Fatalf("draining actors must retain ownership, actors=%d leases=%d", actorCount, leases)
	}
	if got := ledger.DrainingCount(); got != 2 {
		t.Fatalf("draining actors got %d want 2", got)
	}
	if _, blocked := ledger.blockedUID[101]; !blocked {
		t.Fatalf("blocked marker must remain until actor exit")
	}
}

func TestLedgerFilterBlockedRuntimeStatus(t *testing.T) {
	ledger := NewLedger()
	ledger.BlockUID(101)
	status := map[int]robotcap.RuntimeStatus{
		101: {UID: 101},
		102: {UID: 102},
	}
	ledger.FilterBlockedRuntimeStatus(status)
	if _, ok := status[101]; ok {
		t.Fatalf("blocked uid should be removed from runtime status")
	}
	if _, ok := status[102]; !ok {
		t.Fatalf("unblocked uid should remain in runtime status")
	}
}

func TestLedgerBlockedLeaseRetainsOwnershipForHealthRetry(t *testing.T) {
	ledger := NewLedger()
	actor := testLedgerActor(1, ModeAuto, 101)
	addTestLedgerActor(&ledger, actor)
	if !ledger.TryLeaseUID(101, actor) {
		t.Fatal("lease uid 101")
	}

	if !ledger.BlockLeaseIfCurrent(101, actor) {
		t.Fatal("block current lease")
	}
	if !ledger.BlockLeaseIfCurrent(101, actor) {
		t.Fatal("blocking the current lease should be idempotent")
	}
	if got := ledger.ActorForUID(101); got != actor {
		t.Fatalf("blocked lease lost actor ownership: got %v want %v", got, actor)
	}
	leases := ledger.LeaseSnapshots()
	if len(leases) != 1 || leases[0].UID != 101 || leases[0].Actor != actor || !leases[0].Blocked {
		t.Fatalf("blocked lease snapshot = %+v", leases)
	}
	if reserved, existing, ok := ledger.ReserveEmptyAutoActor(101); ok || existing || reserved != nil {
		t.Fatalf("blocked lease became assignable: actor=%v existing=%v ok=%v", reserved, existing, ok)
	}
}

func TestLedgerBeginDrainSomeAutoActorsHonorsFloor(t *testing.T) {
	ledger := NewLedger()
	for i := 1; i <= 5; i++ {
		addTestLedgerActor(&ledger, testLedgerActor(i, ModeAuto, 100+i))
	}
	actors := ledger.BeginDrainSomeAutoActors(nil, 4, 3)
	if len(actors) != 2 {
		t.Fatalf("detached actors got %d want 2 due to floor", len(actors))
	}
	if got := len(ledger.actors); got != 5 {
		t.Fatalf("draining actors must remain in capacity, got %d want 5", got)
	}
	if got := ledger.DrainingCount(); got != 2 {
		t.Fatalf("draining actors got %d want 2", got)
	}
}

func TestLedgerReapsUnexpectedlyStoppedActorAndBlocksUID(t *testing.T) {
	ledger := NewLedger()
	actor := testLedgerActor(1, ModeAuto, 101)
	addTestLedgerActor(&ledger, actor)
	if !ledger.TryLeaseUID(101, actor) {
		t.Fatal("lease uid 101")
	}
	close(actor.done)

	ledger.ReapDoneDraining()
	if got := ledger.ActorForUID(101); got != nil {
		t.Fatalf("dead actor lease remains: %v", got)
	}
	if len(ledger.ActorPointers()) != 0 {
		t.Fatal("dead actor remains in slot registry")
	}
	if _, blocked := ledger.blockedUID[101]; !blocked {
		t.Fatal("unexpectedly stopped actor uid was not blocked for cleanup")
	}
}

func TestLedgerReapsDoneActorsWithOneLeasePassSemantics(t *testing.T) {
	ledger := NewLedger()
	unexpected := testLedgerActor(1, ModeAuto, 101)
	draining := testLedgerActor(2, ModeAuto, 102)
	live := testLedgerActor(3, ModeAuto, 103)
	for _, actor := range []*Actor{unexpected, draining, live} {
		addTestLedgerActor(&ledger, actor)
		if !ledger.TryLeaseUID(actor.UIDValue(), actor) {
			t.Fatalf("lease uid=%d", actor.UIDValue())
		}
	}
	ledger.BlockUID(102)
	ledger.draining[draining.SlotIDValue()] = draining
	close(unexpected.done)
	close(draining.done)

	if reaped := ledger.ReapDoneDraining(); reaped != 2 {
		t.Fatalf("reaped=%d, want 2", reaped)
	}
	if ledger.ActorForUID(101) != nil || ledger.ActorForUID(102) != nil {
		t.Fatal("completed actor lease remains")
	}
	if ledger.ActorForUID(103) != live || len(ledger.ActorPointers()) != 1 {
		t.Fatal("live actor or lease was removed")
	}
	if _, blocked := ledger.blockedUID[101]; !blocked {
		t.Fatal("unexpected exit uid was not blocked")
	}
	if _, blocked := ledger.blockedUID[102]; blocked {
		t.Fatal("normally drained uid remained blocked")
	}
}

func BenchmarkLedgerReapDoneDraining550(b *testing.B) {
	const actorCount = 550
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		ledger := NewLedger()
		for index := 0; index < actorCount; index++ {
			slotID := index + 1
			uid := 17_000_000 + index
			actor := &Actor{slotID: slotID, uid: uid, done: make(chan struct{})}
			close(actor.done)
			ledger.actors[slotID] = actor
			ledger.uidActors[uid] = actor
			if index&1 == 0 {
				ledger.draining[slotID] = actor
			}
		}
		b.StartTimer()
		if reaped := ledger.ReapDoneDraining(); reaped != actorCount {
			b.Fatalf("reaped=%d, want %d", reaped, actorCount)
		}
	}
}
