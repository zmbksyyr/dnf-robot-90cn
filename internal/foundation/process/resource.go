package process

import (
	"runtime"
	"time"

	"robot/internal/foundation/lockhub"
)

const resourceSnapshotTTL = time.Second

type resourceValues struct {
	cpuPercent float64
	memoryMB   int
	goroutines int
}

type resourceSnapshotCache struct {
	lockhub.Locker
	values resourceValues
	at     time.Time
}

var processResourceCache resourceSnapshotCache

func ResourceSnapshot() (float64, int, int) {
	values := processResourceCache.load(time.Now(), collectResourceSnapshot)
	return values.cpuPercent, values.memoryMB, values.goroutines
}

func (c *resourceSnapshotCache) load(now time.Time, collect func() resourceValues) resourceValues {
	c.Lock()
	defer c.Unlock()
	if !c.at.IsZero() && now.Sub(c.at) < resourceSnapshotTTL {
		return c.values
	}
	c.values = collect()
	c.at = now
	return c.values
}

func collectResourceSnapshot() resourceValues {
	memMB := platformRSSMB()
	if memMB <= 0 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		memMB = int(ms.Sys / 1024 / 1024)
	}
	return resourceValues{
		cpuPercent: platformCPUPercent(),
		memoryMB:   memMB,
		goroutines: runtime.NumGoroutine(),
	}
}
