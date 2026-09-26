//go:build !linux && !windows

package process

// platformCPUPercent is unavailable on platforms without a sampling backend.
func platformCPUPercent() float64 { return 0 }

// platformRSSMB is unavailable on platforms without a sampling backend; the
// shared collector falls back to runtime.MemStats.
func platformRSSMB() int { return 0 }
