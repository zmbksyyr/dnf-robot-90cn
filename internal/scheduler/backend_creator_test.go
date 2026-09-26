package scheduler

import (
	"context"
	"errors"
	"testing"

	robotcap "robot/internal/capability/robot"
	"robot/internal/shared"
)

func TestCreateRobotsDoesNotBypassBackendWithoutCreator(t *testing.T) {
	m := NewRobotManager(nil, nil, nil)
	m.SetBackendRobotCreator(testS4BackendInfo(), nil)
	_, err := m.CreateRobots(robotcap.CreateRequest{Count: 1})
	var unsupported shared.UnsupportedCapabilityError
	if !errors.As(err, &unsupported) || unsupported.Backend != shared.BackendID("test") || unsupported.Operation != shared.CapabilityProvision {
		t.Fatalf("error=%v", err)
	}
}

type testBackendRobotCreator struct{}

func (testBackendRobotCreator) CreateRobots(context.Context, robotcap.CreateRequest) ([]robotcap.Info, error) {
	return []robotcap.Info{{UID: 1, Name: "robot"}}, nil
}

func TestCreateRobotsUsesBackendCreatorWhenConfigured(t *testing.T) {
	m := NewRobotManager(nil, nil, nil)
	m.SetBackendRobotCreator(testS4BackendInfo(), testBackendRobotCreator{})
	robots, err := m.CreateRobots(robotcap.CreateRequest{Count: 1})
	if err != nil || len(robots) != 1 || robots[0].Name != "robot" {
		t.Fatalf("robots=%+v err=%v", robots, err)
	}
}
