package scheduler

import (
	"testing"
	"time"

	"robot/internal/foundation/config"
)

func TestNativeKeypairRequirementDefaultsOnAndCanBeDisabled(t *testing.T) {
	m := NewRobotManager(nil, nil, nil)
	if !m.NativeKeypairRequired() {
		t.Fatal("native keypair gate is not enabled by default")
	}
	m.SetNativeKeypairRequired(false)
	if m.NativeKeypairRequired() {
		t.Fatal("native keypair gate was not disabled")
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
