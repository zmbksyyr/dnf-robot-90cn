package scheduler

import (
	"errors"
	"strings"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func TestSimulatorStoreIsRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	_, err := m.StoreManaged(robotcap.CommandRequest{Count: 1})
	if err == nil {
		t.Fatal("simulator store unexpectedly entered actor workflow")
	}
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendS4A21 || unsupported.Operation != shared.CapabilityStore {
		t.Fatalf("error = %v, want S4A21 store unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}

func TestSimulatorPartyAndSkillEntrypointsAreRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	checks := []struct {
		name string
		run  func() error
		want shared.BackendCapability
	}{
		{name: "party debug", run: func() error { _, err := m.PartyDebugStatus(); return err }, want: shared.CapabilityParty},
		{name: "party skills", run: func() error { _, err := m.ReloadPartySkills(); return err }, want: shared.CapabilitySkill},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.run()
			var unsupported shared.UnsupportedCapabilityError
			if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendS4A21 || unsupported.Operation != check.want {
				t.Fatalf("error = %v, want %s unsupported", err, check.want)
			}
			if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
				t.Fatalf("error = %v, missing stable capability code", err)
			}
		})
	}
}

func TestSimulatorWorldShoutIsRejectedBeforeActorWorkflow(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	_, err := m.ShoutManaged(robotcap.CommandRequest{Count: 1}, true)
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Operation != shared.CapabilityWorldShout {
		t.Fatalf("error = %v, want world shout unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}

func TestSimulatorAutoStoreIsSkippedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	result := NewRobotRuntime(m).AutoStore(7, nil)
	if result.State != robotcap.ActionStateCancelled || !strings.Contains(result.Message, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("result = %+v, want skipped unsupported store", result)
	}
}

func TestSimulatorVerifiedTownAndLocalShoutRemainAllowed(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	if err := m.requireBackendCapability(shared.CapabilityTownMove); err != nil {
		t.Fatalf("town move unexpectedly unsupported: %v", err)
	}
	if err := m.requireBackendCapability(shared.CapabilityShout); err != nil {
		t.Fatalf("local shout unexpectedly unsupported: %v", err)
	}
}

func TestSimulatorCleanupIsRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	_, err := m.CleanupRobots(robotcap.CleanupRequest{UIDs: []int{7}, Force: true})
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Operation != shared.CapabilityCleanup {
		t.Fatalf("error = %v, want cleanup unsupported", err)
	}
	if !strings.Contains(err.Error(), shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("error = %v, missing stable capability code", err)
	}
}

func TestSimulatorKeypairOperationsAreRejectedWithStableCapabilityError(t *testing.T) {
	m := testRobotManagerWithConfig(t, "")
	m.SetBackendRobotCreator(shared.BackendS4A21, nil)
	if status := m.KeypairStatus(); !strings.Contains(status.Error, shared.CodeBackendCapabilityUnsupported) {
		t.Fatalf("status = %+v, want stable unsupported keypair error", status)
	}
	_, err := m.ReleaseDefaultKeypair()
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Operation != shared.CapabilityKeypair {
		t.Fatalf("error = %v, want keypair unsupported", err)
	}
}
