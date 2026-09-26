package log

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRobotfUsesSinkWithoutDuplicatingStdout(t *testing.T) {
	oldStdout := os.Stdout
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writeEnd
	defer func() {
		os.Stdout = oldStdout
		SetRobotSink(nil)
		_ = readEnd.Close()
		_ = writeEnd.Close()
	}()

	var sinkMessage string
	SetRobotSink(func(msg string) {
		sinkMessage = msg
	})
	Robotf("robot %d\n", 7)
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(readEnd)
	if err != nil {
		t.Fatal(err)
	}
	if sinkMessage != "robot 7\n" {
		t.Fatalf("sink message = %q", sinkMessage)
	}
	if len(stdout) != 0 {
		t.Fatalf("stdout duplicated sink message: %q", stdout)
	}
}

func TestRobotSinkCanBeReplacedWhileLogging(t *testing.T) {
	defer SetRobotSink(nil)

	var calls atomic.Int64
	sink := func(string) {
		calls.Add(1)
	}
	SetRobotSink(sink)

	const iterations = 1000
	var writers sync.WaitGroup
	writers.Add(2)
	go func() {
		defer writers.Done()
		for i := 0; i < iterations; i++ {
			SetRobotSink(sink)
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; i < iterations; i++ {
			Robotf("event=%d\n", i)
		}
	}()
	writers.Wait()

	if got := calls.Load(); got != iterations {
		t.Fatalf("sink calls = %d, want %d", got, iterations)
	}
}

// A console paused by QuickEdit selection or a pipe without a reader must never
// block the caller: the queue fills and drops instead.
func TestConsoleQueueDropsInsteadOfBlockingCallers(t *testing.T) {
	blocked := make(chan struct{})
	entered := make(chan struct{}, 1)
	queue := newConsoleQueue()
	queue.print = func(string) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-blocked
	}
	defer close(blocked)
	queue.Write("first line\n")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("console writer did not enter the blocked print")
	}

	done := make(chan struct{})
	go func() {
		for i := 0; i < consoleQueueSize*4; i++ {
			queue.Write("line\n")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("console writes blocked after the console stopped consuming")
	}
	if queue.dropped.Load() == 0 {
		t.Fatal("expected dropped console lines while the console was blocked")
	}
}
