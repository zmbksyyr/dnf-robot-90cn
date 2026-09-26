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

func TestAutoGamePortProbeUsesBoundedCache(t *testing.T) {
	m := NewRobotManager(nil, &config.SysConfig{RobotConnectIP: "127.0.0.1", RobotGamePort: 10011}, nil)
	calls := 0
	probeDone := make(chan struct{}, 4)
	m.autoPortDial = func(_, _ string, _ time.Duration) (net.Conn, error) {
		calls++
		client, server := net.Pipe()
		_ = server.Close()
		probeDone <- struct{}{}
		return client, nil
	}
	rc := robotconfig.RuntimeConfig{AutoGamePortCheckTimeoutMS: 10, AutoGamePortStableSec: 1}
	now := time.Unix(100, 0)
	_ = m.autoGamePortStable(now, rc)
	<-probeDone
	_ = m.autoGamePortStable(now.Add(500*time.Millisecond), rc)
	if calls != 1 {
		t.Fatalf("dial calls within cache TTL = %d, want 1", calls)
	}
	_ = m.autoGamePortStable(now.Add(1100*time.Millisecond), rc)
	<-probeDone
	if calls != 2 {
		t.Fatalf("dial calls after cache TTL = %d, want 2", calls)
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
