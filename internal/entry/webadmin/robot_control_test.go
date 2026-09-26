package webadmin

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildRobotRestartScriptRestartsSingleRobotProcess(t *testing.T) {
	script := buildRobotRestartScript("/root/robot", "/root/config")
	for _, want := range []string{
		shellQuote(filepath.Join("/root/config", "logs", "stdout.log")),
		`nohup "$exe" >>`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("restart script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "bounded-log-sink") || strings.Contains(script, "web-admin") {
		t.Fatalf("single-process restart script retains child modes: %s", script)
	}
}

func TestRestartHelperRequestedRequiresFirstExactArgument(t *testing.T) {
	if !RestartHelperRequested([]string{"--restart-helper", "--restart-parent-pid", "1"}) {
		t.Fatal("restart helper argument was not recognized")
	}
	for _, args := range [][]string{
		nil,
		{"--web-admin", "--restart-helper"},
		{"--restart-helper=true"},
	} {
		if RestartHelperRequested(args) {
			t.Fatalf("unexpected restart helper match for %v", args)
		}
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellQuote("/root/robot's/bin")
	want := "'/root/robot'\"'\"'s/bin'"
	if got != want {
		t.Fatalf("quote = %q, want %q", got, want)
	}
}
