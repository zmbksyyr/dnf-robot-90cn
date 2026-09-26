//go:build linux

package process

import (
	"os"
	"strconv"
	"strings"
	"time"

	"robot/internal/foundation/lockhub"
)

// platformCPUPercent samples this process via /proc/self/stat. The first call
// only establishes a baseline and reports 0.
func platformCPUPercent() float64 {
	stat, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(stat))
	if len(parts) < 15 {
		return 0
	}
	utime, err1 := strconv.ParseFloat(parts[13], 64)
	stime, err2 := strconv.ParseFloat(parts[14], 64)
	if err1 != nil || err2 != nil {
		return 0
	}
	const clockTicks = 100.0
	now := time.Now()
	ticks := utime + stime
	cpuSample.Lock()
	defer cpuSample.Unlock()
	if cpuSample.at.IsZero() {
		cpuSample.ticks = ticks
		cpuSample.at = now
		return 0
	}
	elapsed := now.Sub(cpuSample.at).Seconds()
	deltaTicks := ticks - cpuSample.ticks
	cpuSample.ticks = ticks
	cpuSample.at = now
	if elapsed <= 0 || deltaTicks < 0 {
		return 0
	}
	return (deltaTicks / clockTicks) / elapsed * 100
}

var cpuSample struct {
	lockhub.Locker
	ticks float64
	at    time.Time
}

// platformRSSMB reads the resident set size from /proc/self/statm.
func platformRSSMB() int {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 2 {
		return 0
	}
	pages, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0
	}
	return pages * os.Getpagesize() / 1024 / 1024
}
