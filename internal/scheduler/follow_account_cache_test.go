package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"robot/internal/foundation/config"
)

type followAccountTestLocator struct {
	village      int
	villageCalls atomic.Int32
	started      chan struct{}
	release      chan struct{}
}

func (l *followAccountTestLocator) FollowAccountVillageLastPlayed(context.Context, string) (int, bool, error) {
	if l.villageCalls.Add(1) == 1 && l.started != nil {
		close(l.started)
	}
	if l.release != nil {
		<-l.release
	}
	return l.village, l.village > 0, nil
}

func TestFollowAccountLookupCoalescesConcurrentMoves(t *testing.T) {
	locator := &followAccountTestLocator{
		village: 3,
		started: make(chan struct{}), release: make(chan struct{}),
	}
	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, nil)
	manager.SetBackendFollowAccountLocator(locator)
	t.Cleanup(func() { _ = manager.Shutdown() })

	first := make(chan followAccountLookup, 1)
	go func() {
		lookup, _ := manager.loadFollowAccount("leader")
		first <- lookup
	}()
	<-locator.started

	started := time.Now()
	if _, ok := manager.loadFollowAccount("leader"); ok {
		t.Fatal("in-flight first lookup unexpectedly returned cached data")
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("concurrent lookup blocked for %s", elapsed)
	}
	if locator.villageCalls.Load() != 1 {
		t.Fatalf("village lookup calls during refresh = %d, want 1", locator.villageCalls.Load())
	}

	close(locator.release)
	lookup := <-first
	if !lookup.villageOK || lookup.village != 3 {
		t.Fatalf("refreshed lookup = %+v", lookup)
	}
	for range 32 {
		if _, ok := manager.loadFollowAccount("leader"); !ok {
			t.Fatal("fresh lookup was not cached")
		}
	}
	if locator.villageCalls.Load() != 1 {
		t.Fatalf("cached lookup queries village=%d, want 1", locator.villageCalls.Load())
	}
}

func TestFollowAccountLookupReportsMissingLocator(t *testing.T) {
	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, nil)
	t.Cleanup(func() { _ = manager.Shutdown() })
	if _, ok := manager.loadFollowAccount("leader"); ok {
		t.Fatal("lookup without an adapter locator reported success")
	}
}
