//go:build !windows

package atomicfile

// HardenPrivate is a no-op on platforms where the file mode already carries the
// protection intent (0600 for owner-only files).
func HardenPrivate(path string) error { return nil }
