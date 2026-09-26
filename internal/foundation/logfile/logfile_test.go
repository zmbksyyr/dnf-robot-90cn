package logfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareBoundsExistingFilesAndPrunesBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "robot.log")
	mustWriteFile(t, path, "0123456789ABC")
	mustWriteFile(t, path+".1", "abcdefghijkl")
	mustWriteFile(t, path+".2", "obsolete")

	if err := Prepare(path, 8, 1); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, path, "56789ABC")
	assertFileText(t, path+".1", "efghijkl")
	if _, err := os.Stat(path + ".2"); !os.IsNotExist(err) {
		t.Fatalf("extra backup still exists: %v", err)
	}
}

func TestAppendRotatesBeforeRecordWouldExceedLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := Append(path, []byte("abcd"), 8, 2); err != nil {
		t.Fatal(err)
	}
	if err := Append(path, []byte("efghi"), 8, 2); err != nil {
		t.Fatal(err)
	}
	assertFileText(t, path, "efghi")
	assertFileText(t, path+".1", "abcd")
}

func TestAppendRejectsRecordLargerThanLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := Append(path, []byte("oversized"), 4, 1); err == nil {
		t.Fatal("oversized record was accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized record created active log: %v", err)
	}
}

func TestTruncateToTailWorksWhileFileIsHeldOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "robot.log")
	mustWriteFile(t, path, "0123456789ABC")

	hold, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()

	if err := TruncateToTail(path, 8); err != nil {
		t.Fatalf("truncate while held: %v", err)
	}
	assertFileText(t, path, "56789ABC")

	if err := TruncateToTail(path, 8); err != nil {
		t.Fatalf("second truncate: %v", err)
	}
	assertFileText(t, path, "56789ABC")

	if err := Append(path, []byte("!"), 64, 1); err != nil {
		t.Fatalf("write after truncate: %v", err)
	}
	assertFileText(t, path, "56789ABC!")
}

func mustWriteFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func assertFileText(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
