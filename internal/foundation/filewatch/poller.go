package filewatch

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"robot/internal/foundation/lockhub"
)

const DefaultInterval = time.Second

// Entry describes one runtime file. Apply must fully parse and validate the
// file before publishing a new in-memory value. Poller serializes all Apply
// calls, so a process needs only one polling goroutine for all of its entries.
type Entry struct {
	Name  string
	Path  string
	Apply func(path string) error
}

type ErrorHandler func(entry Entry, err error)

type fileStamp struct {
	exists  bool
	mtimeNS int64
	size    int64
	mode    os.FileMode
	digest  [sha256.Size]byte
}

type trackedEntry struct {
	entry       Entry
	observed    fileStamp
	initialized bool
	statError   string
}

// Poller scans file metadata and a content fingerprint every interval. The
// fingerprint is intentional: filesystems with coarse timestamps can rewrite a
// file without changing size or mtime, and the watcher must still notice. The
// entry's Apply callback remains the only reader of parsed content.
type Poller struct {
	interval time.Duration
	onError  ErrorHandler

	mu      lockhub.Locker
	entries []trackedEntry
	wake    chan struct{}
	stop    chan struct{}
	done    chan struct{}
	start   sync.Once
	close   sync.Once
}

func New(interval time.Duration, entries []Entry, onError ErrorHandler) *Poller {
	if interval <= 0 {
		interval = DefaultInterval
	}
	tracked := make([]trackedEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name == "" {
			entry.Name = entry.Path
		}
		tracked = append(tracked, trackedEntry{entry: entry})
	}
	return &Poller{
		interval: interval,
		onError:  onError,
		entries:  tracked,
		wake:     make(chan struct{}, 1),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start performs an initial scan synchronously, then starts the polling loop.
func (p *Poller) Start() {
	if p == nil {
		return
	}
	p.start.Do(func() {
		p.CheckNow()
		go p.loop()
	})
}

// Wake requests an early scan without starting another goroutine.
func (p *Poller) Wake() {
	if p == nil {
		return
	}
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// CheckNow synchronously scans every entry. It is primarily useful for tests
// and for callers that need deterministic application after an atomic write.
func (p *Poller) CheckNow() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for index := range p.entries {
		p.check(&p.entries[index])
	}
}

func (p *Poller) Close() {
	if p == nil {
		return
	}
	p.close.Do(func() {
		close(p.stop)
		select {
		case <-p.done:
		default:
			// Start may never have been called.
			p.start.Do(func() { close(p.done) })
			<-p.done
		}
	})
}

func (p *Poller) loop() {
	defer close(p.done)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.CheckNow()
		case <-p.wake:
			p.CheckNow()
		case <-p.stop:
			return
		}
	}
}

func (p *Poller) check(tracked *trackedEntry) {
	stamp, err := statStamp(tracked.entry.Path)
	if err != nil {
		message := err.Error()
		if tracked.statError != message {
			tracked.statError = message
			p.report(tracked.entry, err)
		}
		return
	}
	tracked.statError = ""
	if tracked.initialized && tracked.observed == stamp {
		return
	}
	if tracked.entry.Apply == nil {
		tracked.initialized = true
		tracked.observed = stamp
		return
	}
	if err := applySafely(tracked.entry); err != nil {
		p.report(tracked.entry, err)
		return
	}
	tracked.initialized = true
	tracked.observed = stamp
}

func applySafely(entry Entry) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("file watcher apply panic: %v", rec)
		}
	}()
	return entry.Apply(entry.Path)
}

func (p *Poller) report(entry Entry, err error) {
	if p.onError != nil {
		defer func() { _ = recover() }()
		p.onError(entry, err)
	}
}

func statStamp(path string) (fileStamp, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fileStamp{}, nil
	}
	if err != nil {
		return fileStamp{}, fmt.Errorf("stat %s: %w", path, err)
	}
	stamp := fileStamp{
		exists: true, mtimeNS: info.ModTime().UnixNano(), size: info.Size(), mode: info.Mode(),
	}
	if info.Mode().IsRegular() {
		file, err := os.Open(path)
		if err != nil {
			return fileStamp{}, fmt.Errorf("open %s for fingerprint: %w", path, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fileStamp{}, fmt.Errorf("fingerprint %s: %w", path, copyErr)
		}
		if closeErr != nil {
			return fileStamp{}, fmt.Errorf("close %s after fingerprint: %w", path, closeErr)
		}
		copy(stamp.digest[:], hash.Sum(nil))
	}
	return stamp, nil
}
