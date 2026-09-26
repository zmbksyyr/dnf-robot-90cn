package robotlog

import (
	"bufio"
	"fmt"
	"os"
	"sync"
	"time"

	"robot/internal/foundation/lockhub"
	"robot/internal/foundation/logfile"
)

const (
	defaultMaxLogSize    int64 = 100 * 1024 * 1024
	defaultMaxLogBackups       = 5
	defaultLogBufferSize       = 64 * 1024
)

var (
	logFile          *os.File
	logWriter        *bufio.Writer
	logMu            lockhub.Locker
	logName          string
	logSize          int64
	logMaxSize       int64 = defaultMaxLogSize
	logBackups             = defaultMaxLogBackups
	logFlushInterval       = time.Second
	logFlushStop     chan struct{}
	logFlushDone     chan struct{}
	logClosing       bool
	logTrimNext      time.Time
)

// logTrimRetryInterval throttles the in-place truncation fallback so a locked
// log file does not trigger a rewrite attempt for every record.
const logTrimRetryInterval = 30 * time.Second

func ConfigureLogRotation(maxSizeMB, backups int) {
	logMu.Lock()
	defer logMu.Unlock()
	if maxSizeMB <= 0 {
		maxSizeMB = 100
	}
	if backups <= 0 {
		backups = 5
	}
	logMaxSize = int64(maxSizeMB) * 1024 * 1024
	logBackups = backups
}

func LogInit(path string) error {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile != nil || logFlushStop != nil || logClosing {
		return fmt.Errorf("log already initialized: %s", logName)
	}
	if err := logfile.Prepare(path, logMaxSize, logBackups); err != nil {
		// A log file held open by another process must not prevent startup;
		// runtime rotation falls back to in-place truncation.
		_, _ = fmt.Fprintf(os.Stderr, "[Log] prepare failed path=%s err=%v\n", path, err)
	}
	var err error
	logFile, err = os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	info, err := logFile.Stat()
	if err != nil {
		_ = logFile.Close()
		logFile = nil
		return err
	}
	logName = path
	logSize = info.Size()
	logWriter = bufio.NewWriterSize(logFile, defaultLogBufferSize)
	startLogFlusherLocked()
	logStringLocked("LOG START\n")
	flushLogLocked(false)
	return nil
}

func LogString(msg string) {
	logMu.Lock()
	defer logMu.Unlock()
	logStringLocked(msg)
}

func logStringLocked(msg string) {
	if logFile == nil || logWriter == nil {
		return
	}

	now := time.Now()
	ts := fmt.Sprintf("%02d/%02d/%02d %02d:%02d:%02d",
		now.Day(), now.Month(), now.Year()%100,
		now.Hour(), now.Minute(), now.Second())

	prefix := fmt.Sprintf("[%s] %s", ts, msg)
	rotateLogIfNeededLocked(len(prefix))
	if logFile == nil || logWriter == nil {
		return
	}
	n, err := logWriter.WriteString(prefix)
	logSize += int64(n)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[Log] write failed path=%s err=%v\n", logName, err)
	}
}

func rotateLogIfNeededLocked(nextBytes int) {
	if logFile == nil || logWriter == nil || logName == "" || nextBytes <= 0 {
		return
	}
	if logSize == 0 || logSize+int64(nextBytes) <= logMaxSize {
		return
	}
	flushLogLocked(false)
	_ = logFile.Close()
	logFile = nil
	logWriter = nil
	if err := logfile.Rotate(logName, logBackups); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[Log] rotate failed path=%s err=%v\n", logName, err)
		// Windows blocks renaming a file that another process holds open, but
		// in-place truncation still works. Fall back to keeping the newest
		// half of the limit so the size bound holds and growth stays bounded.
		if time.Now().After(logTrimNext) {
			logTrimNext = time.Now().Add(logTrimRetryInterval)
			if trimErr := logfile.TruncateToTail(logName, logMaxSize/2); trimErr != nil {
				_, _ = fmt.Fprintf(os.Stderr, "[Log] trim failed path=%s err=%v\n", logName, trimErr)
			}
		}
	}
	var openErr error
	logFile, openErr = os.OpenFile(logName, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if openErr != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[Log] reopen failed path=%s err=%v\n", logName, openErr)
		logFile = nil
		logSize = 0
		return
	}
	info, err := logFile.Stat()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[Log] stat failed path=%s err=%v\n", logName, err)
		_ = logFile.Close()
		logFile = nil
		logSize = 0
		return
	}
	logSize = info.Size()
	logWriter = bufio.NewWriterSize(logFile, defaultLogBufferSize)
}

func LogClose() {
	logMu.Lock()
	if logClosing {
		done := logFlushDone
		logMu.Unlock()
		if done != nil {
			<-done
		}
		return
	}
	if logFile == nil && logFlushStop == nil {
		logMu.Unlock()
		return
	}
	logClosing = true
	if logFile != nil {
		logStringLocked("LOG END\n")
		flushLogLocked(true)
		_ = logFile.Close()
	}
	logFile = nil
	logWriter = nil
	logSize = 0
	stop := logFlushStop
	done := logFlushDone
	if stop != nil {
		close(stop)
	}
	logMu.Unlock()
	if done != nil {
		<-done
	}
	logMu.Lock()
	if logFlushStop == stop {
		logFlushStop = nil
		logFlushDone = nil
	}
	logClosing = false
	logMu.Unlock()
}

func flushLogLocked(sync bool) {
	if logFile == nil || logWriter == nil {
		return
	}
	if err := logWriter.Flush(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "[Log] flush failed path=%s err=%v\n", logName, err)
		return
	}
	if sync {
		if err := logFile.Sync(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "[Log] sync failed path=%s err=%v\n", logName, err)
		}
	}
}

func startLogFlusherLocked() {
	interval := logFlushInterval
	if interval <= 0 {
		interval = time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	logFlushStop = stop
	logFlushDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				logMu.Lock()
				flushLogLocked(false)
				logMu.Unlock()
			case <-stop:
				return
			}
		}
	}()
}

func PrintfGreen(format string, args ...interface{}) {
	if colorsEnabled() {
		fmt.Printf("\033[1;32m"+format+"\033[0m", args...)
		return
	}
	fmt.Printf(format, args...)
}

func PrintfRed(format string, args ...interface{}) {
	if colorsEnabled() {
		fmt.Printf("\033[1;31m"+format+"\033[0m", args...)
		return
	}
	fmt.Printf(format, args...)
}

func PrintfBlue(format string, args ...interface{}) {
	if colorsEnabled() {
		fmt.Printf("\033[1;36m"+format+"\033[0m", args...)
		return
	}
	fmt.Printf(format, args...)
}

var consoleColor struct {
	once    sync.Once
	enabled bool
}

// colorsEnabled reports whether ANSI colors should be written. Escape
// sequences are only emitted for a real terminal; pipes, redirected output
// and service logs stay plain text.
func colorsEnabled() bool {
	consoleColor.once.Do(func() { consoleColor.enabled = colorSupported() })
	return consoleColor.enabled
}
