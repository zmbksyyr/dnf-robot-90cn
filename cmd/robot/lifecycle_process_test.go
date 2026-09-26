package main

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// TestRobotProcessLifecycle builds the real binary, starts it against the
// configured S4A21 fixture, drives the Web admin login and stop action, and
// verifies the clean exit plus the shutdown log order. It requires the live
// fixture paths because the adapter refuses to start without a readable PVF.
func TestRobotProcessLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the distribution gate keeps the process on Windows")
	}
	pvfPath := os.Getenv("S4A21_TEST_PVF")
	dbPath := os.Getenv("S4A21_TEST_DB")
	if pvfPath == "" || dbPath == "" {
		t.Skip("S4A21_TEST_PVF and S4A21_TEST_DB are required for the lifecycle test")
	}
	if _, err := os.Stat(pvfPath); err != nil {
		t.Fatalf("PVF fixture: %v", err)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database fixture: %v", err)
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "robot.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build robot binary: %v\n%s", err, output)
	}

	clone := filepath.Join(dir, "inventory-clone.db")
	cloneDatabase(t, dbPath, clone)

	robotPort := freeTCPPort(t)
	webPort := freeTCPPort(t)
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "conf"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(configDir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	mainConfig := fmt.Sprintf(`[Ports]
RobotAPI = %d
Web = %d
Game = 10011
PartyRoute0 = 15063

[Robot]
ServerDirectory = %s
RobotInnerIp = 127.0.0.1
RobotConnectIp = 127.0.0.1

[Web]
WebPassword = lifecycle-test-password

[system]
log_max_size_mb = 20
log_max_backups = 2
max_response_bytes = 4194304
`, robotPort, webPort, pvfPath)
	if err := os.WriteFile(filepath.Join(configDir, "conf", "config.ini"), []byte(mainConfig), 0600); err != nil {
		t.Fatal(err)
	}
	selection := fmt.Sprintf(`{
  "backend_id": "sim_a21",
  "config_generation": 1,
  "selected_at": "2026-09-27T00:00:00Z",
  "settings": {
    "server_directory": %q,
    "server_host": "127.0.0.1",
    "game_port": "10011",
    "database_path": %q
  }
}
`, pvfPath, clone)
	if err := os.WriteFile(filepath.Join(configDir, "state", "backend_selection.json"), []byte(selection), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe)
	cmd.Dir = dir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start robot: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	})

	webBase := fmt.Sprintf("http://127.0.0.1:%d", webPort)
	if !waitForWeb(webBase+"/", 60*time.Second) {
		logTail := ""
		if data, err := os.ReadFile(filepath.Join(configDir, "logs", "robot.log")); err == nil {
			logTail = string(data)
		}
		t.Fatalf("web server did not become ready\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", stdout.String(), stderr.String(), logTail)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	login, err := client.PostForm(webBase+"/login", url.Values{"password": {"lifecycle-test-password"}})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = login.Body.Close()
	if login.StatusCode != http.StatusOK && login.StatusCode != http.StatusSeeOther {
		t.Fatalf("login status = %d", login.StatusCode)
	}
	stop, err := client.Post(webBase+"/api/stop-robot", "application/json", nil)
	if err != nil {
		t.Fatalf("stop robot: %v", err)
	}
	body, _ := io.ReadAll(stop.Body)
	_ = stop.Body.Close()
	if stop.StatusCode != http.StatusOK || !strings.Contains(string(body), "stop requested") {
		t.Fatalf("stop status=%d body=%s", stop.StatusCode, body)
	}

	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("robot exited with %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("robot did not exit after the stop action\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	logData, err := os.ReadFile(filepath.Join(configDir, "logs", "robot.log"))
	if err != nil {
		t.Fatalf("read robot log: %v", err)
	}
	logText := string(logData)
	for _, want := range []string{"ROBOT_VERSION", "BACKEND_SELECTED", "ROBOT_STARTED", "ROBOT_CYCLE_END"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("robot log missing %q:\n%s", want, logText)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(logText), "LOG END") {
		t.Fatalf("log did not close last:\n%s", logText)
	}
}

func cloneDatabase(t *testing.T, sourcePath, targetPath string) {
	t.Helper()
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	quoted := strings.ReplaceAll(filepath.ToSlash(targetPath), "'", "''")
	if _, err := source.Exec(`VACUUM INTO '` + quoted + `'`); err != nil {
		t.Fatalf("clone database: %v", err)
	}
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func waitForWeb(address string, timeout time.Duration) bool {
	hostPort := strings.TrimSuffix(strings.TrimPrefix(address, "http://"), "/")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", hostPort, time.Second)
		if err == nil {
			_ = conn.Close()
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
