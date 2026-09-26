package scheduler

import (
	"errors"
	"testing"

	"robot/internal/foundation/config"
)

type loginRepairInvalidationRuntime struct {
	noopRuntime
	calls [][]int
}

func (r *loginRepairInvalidationRuntime) InvalidateLoginRepairs(uids []int) {
	r.calls = append(r.calls, append([]int(nil), uids...))
}

// The legacy lifecycle boundary has no persistence port; it must still drop the
// login-repair cache before reporting the operation as unavailable.
func TestLegacyLifecycleDropsLoginRepairCacheBeforeUnavailable(t *testing.T) {
	runtime := &loginRepairInvalidationRuntime{}
	manager := NewRobotManager(nil, &config.SysConfig{ConfigDir: t.TempDir()}, runtime)
	t.Cleanup(func() { _ = manager.Shutdown() })

	err := (lifecycleCreateEnv{manager: manager}).EnsureAccount(17000001, "127.0.0.1")
	if !errors.Is(err, errSchedulerStorageUnavailable) {
		t.Fatalf("EnsureAccount error = %v, want storage unavailable", err)
	}
	err = (lifecycleCleanupEnv{manager: manager}).BatchDeleteRobotData([]int{17000001, 17000002}, []int{900001, 900002})
	if !errors.Is(err, errSchedulerStorageUnavailable) {
		t.Fatalf("BatchDeleteRobotData error = %v, want storage unavailable", err)
	}
	if len(runtime.calls) != 2 || len(runtime.calls[0]) != 1 || runtime.calls[0][0] != 17000001 || len(runtime.calls[1]) != 2 {
		t.Fatalf("login repair invalidations = %v", runtime.calls)
	}
}
