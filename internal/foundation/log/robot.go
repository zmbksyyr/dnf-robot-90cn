package log

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type Sink func(msg string)

var robotSink atomic.Pointer[Sink]

func SetRobotSink(sink Sink) {
	if sink == nil {
		robotSink.Store(nil)
		return
	}
	robotSink.Store(&sink)
}

func Robotf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if sink := robotSink.Load(); sink != nil {
		(*sink)(msg)
		return
	}
	WriteConsole(msg)
}

const consoleQueueSize = 256

// consoleQueue serializes console output on a background goroutine. A console
// paused by QuickEdit text selection, a closed pipe, or a pipe without a reader
// blocks the write indefinitely; callers must not block with it. Messages go
// through a bounded queue and are dropped (with a notice) once it fills, while
// the file log installed by robotlog stays the authoritative record.
type consoleQueue struct {
	once    sync.Once
	queue   chan string
	dropped atomic.Int64
	print   func(string)
}

func newConsoleQueue() *consoleQueue {
	return &consoleQueue{
		queue: make(chan string, consoleQueueSize),
		print: func(msg string) { _, _ = fmt.Print(msg) },
	}
}

func (c *consoleQueue) Write(msg string) {
	c.once.Do(func() { go c.loop() })
	select {
	case c.queue <- msg:
	default:
		c.dropped.Add(1)
	}
}

func (c *consoleQueue) loop() {
	for msg := range c.queue {
		if dropped := c.dropped.Swap(0); dropped > 0 {
			c.print(fmt.Sprintf("[log] %d console lines dropped while the console was blocked\n", dropped))
		}
		c.print(msg)
	}
}

var processConsole = newConsoleQueue()

// WriteConsole queues a message for the process console without blocking the
// caller.
func WriteConsole(msg string) {
	processConsole.Write(msg)
}
