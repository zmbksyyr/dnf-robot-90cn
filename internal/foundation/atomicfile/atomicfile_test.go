package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileReplacesCompleteContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new content"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new content" {
		t.Fatalf("content=%q", got)
	}
}

func TestWriteFileIfMissingDoesNotClobberExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.ini")
	if err := os.WriteFile(path, []byte("user"), 0644); err != nil {
		t.Fatal(err)
	}
	created, err := WriteFileIfMissing(path, []byte("default"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("reported existing file as created")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "user" {
		t.Fatalf("content=%q, want user", got)
	}
}

func TestWriteFileReplacesReadOnlyTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_config.ini")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("new content"), 0600); err != nil {
		t.Fatalf("replace read-only target: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new content" {
		t.Fatalf("content=%q", got)
	}
}

func TestWriteFileKeepsSymlinkAndUpdatesTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "robot.log")
	link := filepath.Join(dir, "robot-link.log")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if err := WriteFile(link, []byte("new"), 0644); err != nil {
		t.Fatalf("write through symlink: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("write replaced the symbolic link instead of its target")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "new" {
		t.Fatalf("target content=%q", got)
	}
}

func TestWriteExclusiveCreatesOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exclusive.ini")
	created, err := writeExclusive(path, []byte("first"), 0644)
	if err != nil || !created {
		t.Fatalf("first create: created=%t err=%v", created, err)
	}
	created, err = writeExclusive(path, []byte("second"), 0644)
	if err != nil || created {
		t.Fatalf("second create: created=%t err=%v", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "first" {
		t.Fatalf("content=%q, want first", got)
	}
}

func TestWriteFileIfMissingConcurrentPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.ini")
	const writers = 24
	var wg sync.WaitGroup
	results := make(chan error, writers)
	created := make(chan bool, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			ok, err := WriteFileIfMissing(path, []byte(fmt.Sprintf("writer-%02d", index)), 0644)
			created <- ok
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(created)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	createdCount := 0
	for ok := range created {
		if ok {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want 1", createdCount)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != len("writer-00") {
		t.Fatalf("partial content %q", data)
	}
}
