package scheduler

import (
	"os"
	"strings"
	"testing"

	"robot/internal/foundation/layout"
)

func TestPauseAutoActionsRestoresPreviousState(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_actions = true\nauto_target_online_count = 200\n")
	m.autoEnabled = true

	restore := m.pauseAutoActions()
	if m.autoActionsEnabled(m.loadRobotConfig()) {
		t.Fatal("pauseAutoActions did not suspend automatic actions")
	}
	path := layout.New(m.cfg.ConfigDir).RobotConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "auto_actions = true") {
		t.Fatalf("pause touched the persisted config:\n%s", string(data))
	}

	restore()
	if !m.autoActionsEnabled(m.loadRobotConfig()) {
		t.Fatal("restore did not re-enable automatic actions")
	}
}

func TestPauseAutoActionsKeepsDisabledState(t *testing.T) {
	m := testRobotManagerWithConfig(t, "[auto]\nauto_actions = false\n")
	m.autoEnabled = false
	restore := m.pauseAutoActions()
	restore()
	if m.autoActionsEnabled(m.loadRobotConfig()) {
		t.Fatal("pause of a disabled manager must not enable auto")
	}
}
