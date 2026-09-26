package s4a21

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPersistenceExecutorSerializesJobs(t *testing.T) {
	executor := newPersistenceExecutor(8)
	defer executor.Close()
	var active, maximum atomic.Int32
	done := make(chan error, 8)
	for range 8 {
		go func() {
			done <- executor.Do(context.Background(), func(context.Context) error {
				current := active.Add(1)
				for {
					observed := maximum.Load()
					if current <= observed || maximum.CompareAndSwap(observed, current) {
						break
					}
				}
				time.Sleep(2 * time.Millisecond)
				active.Add(-1)
				return nil
			})
		}()
	}
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent jobs = %d, want 1", got)
	}
}

func TestPersistenceExecutorSkipsCancelledQueuedJob(t *testing.T) {
	executor := newPersistenceExecutor(2)
	defer executor.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- executor.Do(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	var ran atomic.Bool
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- executor.Do(ctx, func(context.Context) error {
			ran.Store(true)
			return nil
		})
	}()
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued cancellation = %v, want context.Canceled", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for len(executor.jobs) != 0 {
		select {
		case <-deadline:
			t.Fatal("cancelled job was not drained")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if ran.Load() {
		t.Fatal("cancelled queued job ran")
	}
}

func TestPersistenceExecutorContainsFailureAndPanic(t *testing.T) {
	executor := newPersistenceExecutor(2)
	defer executor.Close()
	want := errors.New("write failed")
	if err := executor.Do(context.Background(), func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatalf("job error = %v, want %v", err, want)
	}
	if err := executor.Do(context.Background(), func(context.Context) error { panic("broken write") }); err == nil {
		t.Fatal("panic did not become an error")
	}
	if err := executor.Do(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("worker did not survive prior failures: %v", err)
	}
}

func TestPersistenceExecutorCloseStopsWorkerAndRejectsJobs(t *testing.T) {
	executor := newPersistenceExecutor(1)
	executor.Close()
	select {
	case <-executor.workerDone:
	default:
		t.Fatal("worker remains active after Close")
	}
	if err := executor.Do(context.Background(), func(context.Context) error { return nil }); !errors.Is(err, errPersistenceExecutorClosed) {
		t.Fatalf("post-close job = %v, want closed error", err)
	}
	executor.Close()
}
