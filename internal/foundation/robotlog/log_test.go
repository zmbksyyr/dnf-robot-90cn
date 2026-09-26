package robotlog

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintfHelpersWritePlainTextToRedirectedOutput(t *testing.T) {
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	PrintfGreen("hello %s", "world")
	PrintfRed("count=%d", 2)
	PrintfBlue("done")
	_ = writer.Close()
	os.Stdout = old

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"hello world", "count=2", "done"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("ANSI colors leaked into redirected output: %q", text)
	}
}
