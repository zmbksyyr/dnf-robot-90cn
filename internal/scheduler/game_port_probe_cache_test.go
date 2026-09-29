package scheduler

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
)

// waitAutoPortProbeIdle blocks until the background game-port probe finishes.
// The probe runs in its own goroutine, so a test that wants to exercise the
// cache TTL must wait for the in-flight flag to clear instead of assuming the
// goroutine was scheduled promptly.
func waitAutoPortProbeIdle(t *testing.T, m *RobotManager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.autoMu.Lock()
		inflight := m.autoPortProbeInflight
		m.autoMu.Unlock()
		if !inflight {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("game port probe did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAutoGamePortProbeUsesBoundedCache(t *testing.T) {
	m := NewRobotManager(nil, &config.SysConfig{RobotConnectIP: "127.0.0.1", RobotGamePort: 10011}, nil)
	var calls atomic.Int32
	probeDone := make(chan struct{}, 4)
	m.autoPortDial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		calls.Add(1)
		client, server := net.Pipe()
		_ = server.Close()
		probeDone <- struct{}{}
		return client, nil
	}
	rc := robotconfig.RuntimeConfig{AutoGamePortCheckTimeoutMS: 10, AutoGamePortStableSec: 1}
	now := time.Unix(100, 0)
	_ = m.autoGamePortStable(now, rc)
	select {
	case <-probeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("initial probe did not start")
	}
	waitAutoPortProbeIdle(t, m)

	_ = m.autoGamePortStable(now.Add(500*time.Millisecond), rc)
	if got := calls.Load(); got != 1 {
		t.Fatalf("dial calls within cache TTL = %d, want 1", got)
	}

	_ = m.autoGamePortStable(now.Add(1100*time.Millisecond), rc)
	select {
	case <-probeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("probe after cache TTL did not start")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("dial calls after cache TTL = %d, want 2", got)
	}
}

func TestAutoGamePortProbeDoesNotBlockTick(t *testing.T) {
	m := NewRobotManager(nil, &config.SysConfig{RobotConnectIP: "127.0.0.1", RobotGamePort: 10011}, nil)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	m.autoPortDial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return nil, errors.New("dial refused")
	}
	rc := robotconfig.RuntimeConfig{AutoGamePortCheckTimeoutMS: 10_000, AutoGamePortStableSec: 1}

	start := time.Now()
	_ = m.autoGamePortStable(time.Unix(100, 0), rc)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("probe blocked the tick for %v", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background probe did not start")
	}

	// A second tick while the probe is in flight must not start another dial.
	m.autoMu.Lock()
	m.autoPortProbeAt = time.Time{}
	m.autoMu.Unlock()
	_ = m.autoGamePortStable(time.Unix(200, 0), rc)
	if got := calls.Load(); got != 1 {
		t.Fatalf("dial calls while a probe is in flight = %d, want 1", got)
	}
	close(release)
}
