package logfile

import (
	"bytes"
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

func TestAppenderRotatesWholeRecordsAndCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	appender, err := OpenAppender(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := appender.Append([]byte("abcd")); err != nil {
		t.Fatal(err)
	}
	if err := appender.Append([]byte("efghi")); err != nil {
		t.Fatal(err)
	}
	if err := appender.Close(); err != nil {
		t.Fatal(err)
	}
	if err := appender.Append([]byte("x")); err == nil {
		t.Fatal("closed appender accepted a record")
	}
	assertFileText(t, path, "efghi")
	assertFileText(t, path+".1", "abcd")
}

func TestCopyRotatingDrainsInputAndBoundsEveryFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	src := bytes.NewReader([]byte("0123456789abcdefghijklmnopqrst"))
	if err := CopyRotating(path, src, 8, 2); err != nil {
		t.Fatal(err)
	}
	if src.Len() != 0 {
		t.Fatalf("source was not drained: %d bytes remain", src.Len())
	}
	for _, candidate := range []string{path, path + ".1", path + ".2"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatalf("stat %s: %v", candidate, err)
		}
		if info.Size() > 8 {
			t.Fatalf("%s exceeds limit: %d", candidate, info.Size())
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}
}

func TestContainsAnyTailIsCaseInsensitiveAndBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service.log")
	mustWriteFile(t, path, "FaTaL\n"+string(bytes.Repeat([]byte{'x'}, 32)))
	found, err := ContainsAnyTail(path, 8, "fatal")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("search escaped the configured tail")
	}
	found, err = ContainsAnyTail(path, 64, "fatal")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("case-insensitive marker was not found")
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

	appender, err := OpenAppender(path, 64, 1)
	if err != nil {
		t.Fatalf("append after truncate: %v", err)
	}
	if err := appender.Append([]byte("!")); err != nil {
		t.Fatalf("write after truncate: %v", err)
	}
	if err := appender.Close(); err != nil {
		t.Fatal(err)
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
