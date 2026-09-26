//go:build windows

package process

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"robot/internal/foundation/lockhub"
)

// platformCPUPercent samples kernel+user time through GetProcessTimes. The
// first call only establishes a baseline and reports 0.
func platformCPUPercent() float64 {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	now := time.Now()
	seconds := filetimeSeconds(kernel) + filetimeSeconds(user)
	cpuSample.Lock()
	defer cpuSample.Unlock()
	if cpuSample.at.IsZero() {
		cpuSample.seconds = seconds
		cpuSample.at = now
		return 0
	}
	elapsed := now.Sub(cpuSample.at).Seconds()
	delta := seconds - cpuSample.seconds
	cpuSample.seconds = seconds
	cpuSample.at = now
	if elapsed <= 0 || delta < 0 {
		return 0
	}
	return delta / elapsed * 100
}

var cpuSample struct {
	lockhub.Locker
	seconds float64
	at      time.Time
}

func filetimeSeconds(value windows.Filetime) float64 {
	return float64(value.HighDateTime)*429.4967296 + float64(value.LowDateTime)*1e-7
}

type processMemoryCounters struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

var (
	psapi                    = windows.NewLazySystemDLL("psapi.dll")
	procGetProcessMemoryInfo = psapi.NewProc("GetProcessMemoryInfo")
)

// platformRSSMB reads the working set size through GetProcessMemoryInfo.
func platformRSSMB() int {
	handle, err := windows.GetCurrentProcess()
	if err != nil {
		return 0
	}
	counters := processMemoryCounters{cb: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	result, _, _ := procGetProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.cb),
	)
	if result == 0 {
		return 0
	}
	return int(counters.workingSetSize / 1024 / 1024)
}
