package robotlog

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPrintfHelpersWritePlainTextToRedirectedOutput(t *testing.T) {
	// Console output is queued so a frozen console cannot block callers; give
	// any earlier queued lines a moment to drain before swapping stdout.
	time.Sleep(50 * time.Millisecond)

	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() { os.Stdout = old }()
	PrintfGreen("hello %s", "world")
	PrintfRed("count=%d", 2)
	PrintfBlue("done")

	chunks := make(chan string, 16)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 512)
		for {
			n, readErr := reader.Read(buf)
			if n > 0 {
				select {
				case chunks <- string(buf[:n]):
				default:
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// The polling goroutine is the only reader of the channel; the builder
	// stays on the test goroutine so no lock is needed.
	var text strings.Builder
	collect := func() {
		for {
			select {
			case chunk := <-chunks:
				text.WriteString(chunk)
			default:
				return
			}
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		collect()
		if strings.Contains(text.String(), "done") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("console output %q missing queued messages", text.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = writer.Close()
	<-readDone
	collect()

	got := text.String()
	for _, want := range []string{"hello world", "count=2", "done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("ANSI colors leaked into redirected output: %q", got)
	}
}
