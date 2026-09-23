package scheduler

import (
	"errors"
	"testing"
	"time"

	"robot/internal/foundation/config"
)

type rejectingGameCommandGate struct{ err error }

func (g rejectingGameCommandGate) Check() error { return g.err }

func TestGameCommandGateDefaultsOpenAndCanBeInjected(t *testing.T) {
	m := NewRobotManager(nil, nil, nil)
	if err := m.CheckGameCommand(); err != nil {
		t.Fatalf("default game command gate rejected command: %v", err)
	}
	want := errors.New("runtime unavailable")
	m.SetGameCommandGate(rejectingGameCommandGate{err: want})
	if err := m.CheckGameCommand(); !errors.Is(err, want) {
		t.Fatalf("game command gate error = %v, want %v", err, want)
	}
}

func TestManagerShutdownWaitsForRegisteredBackgroundWork(t *testing.T) {
	manager := NewRobotManager(nil, &config.SysConfig{}, nil)
	finish, ok := manager.BeginBackgroundWork()
	if !ok {
		t.Fatal("background work was rejected before shutdown")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- manager.Shutdown() }()

	select {
	case err := <-shutdownDone:
		t.Fatalf("shutdown returned before background work completed: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	finish()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after background work completed")
	}
	if _, ok := manager.BeginBackgroundWork(); ok {
		t.Fatal("background work was accepted after shutdown began")
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatalf("second shutdown failed: %v", err)
	}
}

func TestRobotSupervisorRunSafelyContainsPanic(t *testing.T) {
	supervisor := &RobotSupervisor{}
	if supervisor.runSafely("test", func() { panic("boom") }) {
		t.Fatal("panicking supervisor operation reported success")
	}
	called := false
	if !supervisor.runSafely("healthy", func() { called = true }) || !called {
		t.Fatal("supervisor did not remain usable after contained panic")
	}
}
