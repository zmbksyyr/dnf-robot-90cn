package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	robotcap "robot/internal/capability/robot"
	"robot/internal/foundation/lockhub"
)

type positionWriterFunc func(context.Context, []robotcap.PositionUpdate) error

func (f positionWriterFunc) UpdateRobotPositions(ctx context.Context, batch []robotcap.PositionUpdate) error {
	return f(ctx, batch)
}

type fakePositionTimer struct {
	mu       lockhub.Locker
	active   bool
	callback func()
}

func (t *fakePositionTimer) Stop() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return false
	}
	t.active = false
	return true
}

func (t *fakePositionTimer) Fire() bool {
	t.mu.Lock()
	if !t.active {
		t.mu.Unlock()
		return false
	}
	t.active = false
	callback := t.callback
	t.mu.Unlock()
	callback()
	return true
}

type fakePositionClock struct {
	mu     lockhub.Locker
	timers []*fakePositionTimer
	delays []time.Duration
}

func (c *fakePositionClock) AfterFunc(delay time.Duration, callback func()) positionTimer {
	timer := &fakePositionTimer{active: true, callback: callback}
	c.mu.Lock()
	c.timers = append(c.timers, timer)
	c.delays = append(c.delays, delay)
	c.mu.Unlock()
	return timer
}

func (c *fakePositionClock) FireNext() bool {
	c.mu.Lock()
	timers := append([]*fakePositionTimer(nil), c.timers...)
	c.mu.Unlock()
	for _, timer := range timers {
		if timer.Fire() {
			return true
		}
	}
	return false
}

func (c *fakePositionClock) Delays() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.delays...)
}

func newTestPositionBatcher(writer robotPositionWriter, clock *fakePositionClock) *positionBatcher {
	return newPositionBatcher(writer, positionBatchOptions{
		flushDelay:   time.Second,
		retryMin:     10 * time.Millisecond,
		retryMax:     40 * time.Millisecond,
		writeTimeout: time.Second,
		closeTimeout: time.Second,
		afterFunc:    clock.AfterFunc,
	})
}

func copyPositionBatch(batch []robotcap.PositionUpdate) []robotcap.PositionUpdate {
	return append([]robotcap.PositionUpdate(nil), batch...)
}

func TestPositionBatcherKeepsLatestPerUIDAndCombinesUIDs(t *testing.T) {
	clock := &fakePositionClock{}
	var calls [][]robotcap.PositionUpdate
	batcher := newTestPositionBatcher(positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		calls = append(calls, copyPositionBatch(batch))
		return nil
	}), clock)
	defer func() {
		if err := batcher.Close(); err != nil {
			t.Fatalf("close batcher: %v", err)
		}
	}()

	if err := batcher.Queue(robotcap.Info{UID: 102, CID: 202, Village: 1, Area: 2}, 1, 2, 10, 20); err != nil {
		t.Fatal(err)
	}
	if err := batcher.Queue(robotcap.Info{UID: 101, CID: 201, Village: 3, Area: 4}, 3, 4, 30, 40); err != nil {
		t.Fatal(err)
	}
	if err := batcher.Queue(robotcap.Info{UID: 102, CID: 202, Village: 1, Area: 2, X: 10, Y: 20}, 1, 2, 50, 60); err != nil {
		t.Fatal(err)
	}
	if got := len(clock.Delays()); got != 1 {
		t.Fatalf("scheduled timers got %d want 1", got)
	}
	if !clock.FireNext() {
		t.Fatal("expected position flush timer")
	}

	if len(calls) != 1 {
		t.Fatalf("writer calls got %d want 1", len(calls))
	}
	got := calls[0]
	if len(got) != 2 {
		t.Fatalf("batch size got %d want 2", len(got))
	}
	if got[0].UID != 101 || got[0].X != 30 || got[0].Y != 40 {
		t.Fatalf("first update got %+v", got[0])
	}
	if got[1].UID != 102 || got[1].Village != 1 || got[1].Area != 2 || got[1].X != 50 || got[1].Y != 60 {
		t.Fatalf("latest update got %+v", got[1])
	}
}

func TestPositionBatcherPreservesCrossAreaTarget(t *testing.T) {
	clock := &fakePositionClock{}
	var got robotcap.PositionUpdate
	batcher := newTestPositionBatcher(positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		if len(batch) != 1 {
			t.Fatalf("batch size got %d want 1", len(batch))
		}
		got = batch[0]
		return nil
	}), clock)
	defer batcher.Close()

	source := robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2, X: 10, Y: 20}
	_ = batcher.Queue(source, 3, 4, 30, 40)
	if !clock.FireNext() {
		t.Fatal("expected position flush timer")
	}
	if got.Village != 3 || got.Area != 4 || got.X != 30 || got.Y != 40 {
		t.Fatalf("target position got %+v", got)
	}
}

func TestPositionBatcherDiscardDropsPendingUID(t *testing.T) {
	clock := &fakePositionClock{}
	var calls int
	batcher := newTestPositionBatcher(positionWriterFunc(func(_ context.Context, _ []robotcap.PositionUpdate) error {
		calls++
		return nil
	}), clock)
	if err := batcher.Queue(robotcap.Info{UID: 101, CID: 201}, 1, 2, 30, 40); err != nil {
		t.Fatal(err)
	}
	batcher.Discard(101)
	if !clock.FireNext() {
		t.Fatal("expected position flush timer")
	}
	if calls != 0 {
		t.Fatalf("discarded position was written %d times", calls)
	}
}

func TestPositionBatcherFailureDoesNotOverwriteNewerPendingPosition(t *testing.T) {
	clock := &fakePositionClock{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu lockhub.Locker
	var calls [][]robotcap.PositionUpdate
	writer := positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		mu.Lock()
		call := len(calls)
		calls = append(calls, copyPositionBatch(batch))
		mu.Unlock()
		if call == 0 {
			close(firstStarted)
			<-releaseFirst
			return errors.New("temporary write failure")
		}
		return nil
	})
	batcher := newTestPositionBatcher(writer, clock)
	defer batcher.Close()

	_ = batcher.Queue(robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2}, 1, 2, 10, 20)
	fired := make(chan struct{})
	go func() {
		clock.FireNext()
		close(fired)
	}()
	<-firstStarted
	_ = batcher.Queue(robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2, X: 10, Y: 20}, 1, 2, 70, 80)
	_ = batcher.Queue(robotcap.Info{UID: 102, CID: 202, Village: 9, Area: 10}, 9, 10, 90, 100)
	close(releaseFirst)
	<-fired

	if !clock.FireNext() {
		t.Fatal("expected retry timer")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("writer calls got %d want 2", len(calls))
	}
	if len(calls[1]) != 2 {
		t.Fatalf("retry batch size got %d want 2", len(calls[1]))
	}
	if got := calls[1][0]; got.UID != 101 || got.X != 70 || got.Y != 80 {
		t.Fatalf("retry used stale position: %+v", got)
	}
}

func TestPositionBatcherSuccessfulInFlightFlushPreservesNewerTarget(t *testing.T) {
	clock := &fakePositionClock{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu lockhub.Locker
	var calls [][]robotcap.PositionUpdate
	writer := positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		mu.Lock()
		call := len(calls)
		calls = append(calls, copyPositionBatch(batch))
		mu.Unlock()
		if call == 0 {
			close(firstStarted)
			<-releaseFirst
		}
		return nil
	})
	batcher := newTestPositionBatcher(writer, clock)
	defer batcher.Close()

	_ = batcher.Queue(robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2}, 1, 2, 10, 20)
	fired := make(chan struct{})
	go func() {
		clock.FireNext()
		close(fired)
	}()
	<-firstStarted
	_ = batcher.Queue(robotcap.Info{UID: 101, CID: 201, Village: 1, Area: 2}, 1, 2, 30, 40)
	close(releaseFirst)
	<-fired
	if !clock.FireNext() {
		t.Fatal("expected timer for position queued during flush")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 || len(calls[1]) != 1 {
		t.Fatalf("writer calls got %+v", calls)
	}
	got := calls[1][0]
	if got.X != 30 || got.Y != 40 {
		t.Fatalf("newer target got %+v", got)
	}
}

func TestPositionBatcherRetryBackoffIsBounded(t *testing.T) {
	clock := &fakePositionClock{}
	failures := 0
	batcher := newTestPositionBatcher(positionWriterFunc(func(_ context.Context, _ []robotcap.PositionUpdate) error {
		failures++
		if failures <= 4 {
			return errors.New("database unavailable")
		}
		return nil
	}), clock)
	defer batcher.Close()

	_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 1, 2)
	for i := 0; i < 5; i++ {
		if !clock.FireNext() {
			t.Fatalf("missing timer at attempt %d", i+1)
		}
	}
	want := []time.Duration{time.Second, 10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond}
	got := clock.Delays()
	if len(got) != len(want) {
		t.Fatalf("timer delays got %v want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("timer delay[%d] got %s want %s", index, got[index], want[index])
		}
	}
}

func TestPositionBatcherCloseWaitsForCallbackThenFlushesPending(t *testing.T) {
	clock := &fakePositionClock{}
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	defer release()
	var mu lockhub.Locker
	var calls [][]robotcap.PositionUpdate
	writer := positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		mu.Lock()
		call := len(calls)
		calls = append(calls, copyPositionBatch(batch))
		mu.Unlock()
		if call == 0 {
			close(firstStarted)
			<-releaseFirst
		}
		return nil
	})
	batcher := newTestPositionBatcher(writer, clock)
	_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 10, 20)
	fired := make(chan struct{})
	go func() {
		clock.FireNext()
		close(fired)
	}()
	<-firstStarted
	_ = batcher.Queue(robotcap.Info{UID: 102}, 0, 0, 30, 40)

	closed := make(chan error, 1)
	go func() { closed <- batcher.Close() }()
	select {
	case err := <-closed:
		t.Fatalf("close returned before in-flight flush completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	<-fired
	if err := <-closed; err != nil {
		t.Fatalf("close batcher: %v", err)
	}

	mu.Lock()
	if len(calls) != 2 || len(calls[1]) != 1 || calls[1][0].UID != 102 {
		t.Fatalf("close flush calls got %+v", calls)
	}
	mu.Unlock()
	if err := batcher.Queue(robotcap.Info{UID: 103}, 0, 0, 50, 60); !errors.Is(err, errPositionBatcherClosed) {
		t.Fatalf("queue after close error got %v", err)
	}
}

func TestPositionBatcherCloseReturnsFinalFlushError(t *testing.T) {
	clock := &fakePositionClock{}
	wantErr := errors.New("final write failed")
	batcher := newTestPositionBatcher(positionWriterFunc(func(_ context.Context, _ []robotcap.PositionUpdate) error {
		return wantErr
	}), clock)
	_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 10, 20)
	if err := batcher.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("close error got %v want %v", err, wantErr)
	}
	if err := batcher.Close(); !errors.Is(err, wantErr) {
		t.Fatalf("second close error got %v want %v", err, wantErr)
	}
}

func TestPositionBatcherCloseHasBoundedWaitForInFlightFlush(t *testing.T) {
	clock := &fakePositionClock{}
	started := make(chan struct{})
	release := make(chan struct{})
	writer := positionWriterFunc(func(_ context.Context, _ []robotcap.PositionUpdate) error {
		close(started)
		<-release
		return nil
	})
	batcher := newPositionBatcher(writer, positionBatchOptions{
		flushDelay:   time.Second,
		retryMin:     10 * time.Millisecond,
		retryMax:     40 * time.Millisecond,
		writeTimeout: time.Second,
		closeTimeout: 30 * time.Millisecond,
		afterFunc:    clock.AfterFunc,
	})
	_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 10, 20)
	fired := make(chan struct{})
	go func() {
		clock.FireNext()
		close(fired)
	}()
	<-started

	start := time.Now()
	err := batcher.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error got %v want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded close took %s", elapsed)
	}
	close(release)
	<-fired
}

func TestPositionBatcherCloseTimeoutPreservesPendingFinalFlush(t *testing.T) {
	clock := &fakePositionClock{}
	writer := positionWriterFunc(func(ctx context.Context, _ []robotcap.PositionUpdate) error {
		<-ctx.Done()
		return ctx.Err()
	})
	batcher := newPositionBatcher(writer, positionBatchOptions{
		flushDelay:   time.Second,
		retryMin:     10 * time.Millisecond,
		retryMax:     40 * time.Millisecond,
		writeTimeout: time.Second,
		closeTimeout: 30 * time.Millisecond,
		afterFunc:    clock.AfterFunc,
	})
	_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 10, 20)

	err := batcher.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error got %v want deadline exceeded", err)
	}
	batcher.mu.Lock()
	pending := len(batcher.pending)
	batcher.mu.Unlock()
	if pending != 1 {
		t.Fatalf("pending updates got %d want 1 after failed final flush", pending)
	}
}

func TestRobotManagerShutdownFlushesPendingPositions(t *testing.T) {
	clock := &fakePositionClock{}
	var calls [][]robotcap.PositionUpdate
	manager := testRobotManagerWithConfig(t, "")
	manager.positionWrites.Store(newTestPositionBatcher(positionWriterFunc(func(_ context.Context, batch []robotcap.PositionUpdate) error {
		calls = append(calls, copyPositionBatch(batch))
		return nil
	}), clock))
	if batcher := manager.positionWrites.Load(); batcher != nil {
		_ = batcher.Queue(robotcap.Info{UID: 101}, 0, 0, 10, 20)
	}

	if err := manager.Shutdown(); err != nil {
		t.Fatalf("manager shutdown: %v", err)
	}
	if len(calls) != 1 || len(calls[0]) != 1 || calls[0][0].UID != 101 {
		t.Fatalf("shutdown flush calls got %+v", calls)
	}
}

func BenchmarkPositionBatcherTakePending600(b *testing.B) {
	updates := make([]robotcap.PositionUpdate, 600)
	for index := range updates {
		updates[index] = robotcap.PositionUpdate{UID: 17000000 + index}
	}
	batcher := &positionBatcher{pending: make(map[int]robotcap.PositionUpdate, len(updates))}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, update := range updates {
			batcher.pending[update.UID] = update
		}
		batch := batcher.takePendingLocked()
		if len(batch) != len(updates) {
			b.Fatalf("batch size got %d want %d", len(batch), len(updates))
		}
		batcher.recycleBatchLocked(batch)
	}
}
