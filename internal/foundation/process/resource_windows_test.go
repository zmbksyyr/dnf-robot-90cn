//go:build windows

package process

import "testing"

func TestWindowsResourceSampling(t *testing.T) {
	if rss := platformRSSMB(); rss <= 0 {
		t.Fatalf("platformRSSMB() = %d, want a positive working set", rss)
	}
	// The first CPU sample only establishes a baseline.
	_ = platformCPUPercent()
	if cpu := platformCPUPercent(); cpu < 0 {
		t.Fatalf("platformCPUPercent() = %f, want a non-negative value", cpu)
	}
}
