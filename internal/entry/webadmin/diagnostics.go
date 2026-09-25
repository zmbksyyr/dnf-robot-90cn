package webadmin

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

type diagnosticsReport struct {
	OK        bool                 `json:"ok"`
	Generated string               `json:"generated_at"`
	Summary   diagnosticsSummary   `json:"summary"`
	Sections  []diagnosticsSection `json:"sections"`
}

type diagnosticsSummary struct {
	OK       int `json:"ok"`
	Warnings int `json:"warnings"`
	Errors   int `json:"errors"`
}

type diagnosticsSection struct {
	Name   string             `json:"name"`
	Status string             `json:"status"`
	Checks []diagnosticsCheck `json:"checks"`
}

type diagnosticsCheck struct {
	Name     string                 `json:"name"`
	Status   string                 `json:"status"`
	Message  string                 `json:"message"`
	Expected interface{}            `json:"expected,omitempty"`
	Observed interface{}            `json:"observed,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

type diagnosticsBuilder struct {
	cfg    *config.SysConfig
	server *Server
	report diagnosticsReport
}

const (
	diagOK    = "ok"
	diagWarn  = "warn"
	diagError = "error"
)

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.rejectUnsupportedCapability(w, shared.CapabilityDiagnostics) {
		return
	}
	writeJSON(w, s.buildDiagnostics())
}

func (s *Server) buildDiagnostics() diagnosticsReport {
	cfg := s.cfg
	if disk, err := s.loadDiskConfig(); err == nil {
		cfg = disk
	}
	b := diagnosticsBuilder{
		cfg:    cfg,
		server: s,
		report: diagnosticsReport{
			OK:        true,
			Generated: time.Now().Format(time.RFC3339),
		},
	}
	b.addRuntimeSection()
	b.addFileSection()
	b.addSkillSection()
	b.addLogSection()
	for _, section := range b.report.Sections {
		for _, check := range section.Checks {
			switch check.Status {
			case diagError:
				b.report.Summary.Errors++
			case diagWarn:
				b.report.Summary.Warnings++
			default:
				b.report.Summary.OK++
			}
		}
	}
	b.report.OK = b.report.Summary.Errors == 0
	return b.report
}

func (b *diagnosticsBuilder) addSection(name string, checks ...diagnosticsCheck) {
	status := diagOK
	for _, check := range checks {
		if check.Status == diagError {
			status = diagError
			break
		}
		if check.Status == diagWarn {
			status = diagWarn
		}
	}
	b.report.Sections = append(b.report.Sections, diagnosticsSection{Name: name, Status: status, Checks: checks})
}

func (b *diagnosticsBuilder) addRuntimeSection() {
	cfg := b.cfg
	checks := []diagnosticsCheck{
		buildInfoCheck(),
		fileCheck("robot binary", executablePath(), true),
		fileCheck("df_game_r binary", cfg.DFGameR, true),
	}
	ports := expectedProcessPorts(cfg)
	ss, ssErr := listeningProcessPorts()
	if ssErr != nil {
		checks = append(checks, diagnosticsCheck{Name: "listening ports", Status: diagWarn, Message: ssErr.Error()})
	} else {
		for _, p := range ports {
			entry := ss[p.port]
			status := diagError
			msg := "port is not listening"
			if entry.Port == p.port {
				status = diagOK
				msg = "port is listening"
				if p.process != "" && !strings.Contains(entry.Process, p.process) {
					status = diagWarn
					msg = "port is listening, but process name is unexpected"
				}
			}
			checks = append(checks, diagnosticsCheck{
				Name:     p.name + " port",
				Status:   status,
				Message:  msg,
				Expected: map[string]interface{}{"port": p.port, "process": p.process},
				Observed: entry,
			})
		}
		checks = append(checks, actualServicePortsCheck(ss))
	}
	if raw, err := callRobot(b.server.robotAddr, "systemStatus", nil, 5*time.Second, b.cfg.MaxResponseBytes); err == nil {
		checks = append(checks, diagnosticsCheck{Name: "robot api systemStatus", Status: diagOK, Message: "robot API responded", Observed: parseRobotResult(raw)})
	} else {
		checks = append(checks, diagnosticsCheck{Name: "robot api systemStatus", Status: diagError, Message: err.Error(), Expected: b.server.robotAddr})
	}
	b.addSection("Runtime / Ports", checks...)
}

func (b *diagnosticsBuilder) addFileSection() {
	configDir := b.cfg.ConfigDir
	runtimePaths := layout.New(configDir)
	gameDir := filepath.Dir(b.cfg.DFGameR)
	serviceRoot := b.cfg.ServiceRoot
	auctionItemInfo := filepath.Join(serviceRoot, "auction", "iteminfo.dat")
	pointItemInfo := filepath.Join(serviceRoot, "point", "iteminfo.dat")
	paths := []struct {
		name     string
		path     string
		required bool
	}{
		{"config.ini", runtimePaths.MainConfig(), true},
		{"robot_config.ini", runtimePaths.RobotConfig(), true},
		{"Script.pvf", filepath.Join(gameDir, "Script.pvf"), true},
		{"pvf_manifest.json", runtimePaths.PVFManifest(), true},
		{"equipment_catalog.json", runtimePaths.PVFEquipment(), true},
		{"stackable_catalog.json", runtimePaths.PVFStackable(), true},
		{"map_catalog.json", runtimePaths.PVFMaps(), true},
		{"skill_state_catalog.json", runtimePaths.PVFSkillStates(), true},
		{"level_exp_catalog.json", runtimePaths.PVFLevelExp(), true},
		{"iteminfo.dat", runtimePaths.PVFItemInfo(), true},
		{"auction iteminfo.dat", auctionItemInfo, true},
		{"point iteminfo.dat", pointItemInfo, true},
	}
	checks := make([]diagnosticsCheck, 0, len(paths)+3)
	for _, p := range paths {
		checks = append(checks, fileCheck(p.name, p.path, p.required))
	}
	checks = append(checks, b.pvfManifestCheck())
	checks = append(checks, compareFileHashCheck("auction iteminfo matches pvf export", runtimePaths.PVFItemInfo(), auctionItemInfo))
	checks = append(checks, compareFileHashCheck("point iteminfo matches pvf export", runtimePaths.PVFItemInfo(), pointItemInfo))
	b.addSection("Files / PVF / ItemInfo", checks...)
}

func (b *diagnosticsBuilder) addLogSection() {
	limit := int64(b.cfg.LogMaxSizeMB) * 1024 * 1024
	if limit <= 0 {
		limit = 100 * 1024 * 1024
	}
	checks := []diagnosticsCheck{}
	runtimePaths := layout.New(b.cfg.ConfigDir)
	paths := []string{runtimePaths.RobotLog(), runtimePaths.StdoutLog(), runtimePaths.StartErrorLog(), runtimePaths.MarketLog()}
	for _, path := range paths {
		checks = append(checks, logSizeCheck(path, limit))
	}
	checks = append(checks, recentLogPatternCheck("recent fatal log keywords", runtimePaths.RobotLog(), []string{"panic", "fatal", "too many open files", "cannot assign requested address", "message_queue_full", "timer_queue_overflow"}))
	b.addSection("Logs", checks...)
}

type expectedPort struct {
	name    string
	port    int
	process string
}

func expectedProcessPorts(cfg *config.SysConfig) []expectedPort {
	return []expectedPort{
		{"Robot API", cfg.RobotPort, "robot"},
		{"Web", cfg.WebPort, "robot"},
		{"Game", cfg.RobotGamePort, "df_game_r"},
		{"Monitor", cfg.MonitorPort, "df_monitor_r"},
		{"Auction", cfg.AuctionPort, "df_auction_r"},
		{"Point", cfg.PointPort, "df_point_r"},
		{"Relay", cfg.RelayPort, "df_relay_r"},
	}
}

func buildInfoCheck() diagnosticsCheck {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return diagnosticsCheck{Name: "robot build info", Status: diagWarn, Message: "build info is unavailable"}
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		if strings.HasPrefix(setting.Key, "vcs") || setting.Key == "GOOS" || setting.Key == "GOARCH" {
			settings[setting.Key] = setting.Value
		}
	}
	return diagnosticsCheck{Name: "robot build info", Status: diagOK, Message: "build info available", Observed: map[string]interface{}{"go": info.GoVersion, "module": info.Main.Path, "version": info.Main.Version, "settings": settings}}
}

type listeningPort struct {
	Port    int    `json:"port"`
	Process string `json:"process,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Line    string `json:"line,omitempty"`
}

func listeningProcessPorts() (map[int]listeningPort, error) {
	commands := [][]string{{"ss", "-lntp"}, {"netstat", "-lntp"}}
	var out []byte
	var err error
	for _, command := range commands {
		out, err = exec.Command(command[0], command[1:]...).Output()
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("ss/netstat unavailable: %w", err)
	}
	portRE := regexp.MustCompile(`[:.]([0-9]{1,5})\s`)
	result := map[int]listeningPort{}
	for _, line := range strings.Split(string(out), "\n") {
		pm := portRE.FindStringSubmatch(line)
		if len(pm) != 2 {
			continue
		}
		port, _ := strconv.Atoi(pm[1])
		if port <= 0 {
			continue
		}
		entry := listeningPort{Port: port, Line: strings.TrimSpace(line)}
		if name, pid, ok := parseSSProcess(line); ok {
			entry.Process = name
			entry.PID = pid
		} else if name, pid, ok := parseNetstatProcess(line); ok {
			entry.Process = name
			entry.PID = pid
		}
		result[port] = entry
	}
	return result, nil
}

var netstatProcessPattern = regexp.MustCompile(`([0-9]+)/([A-Za-z0-9_.-]+)`)

func parseNetstatProcess(line string) (string, int, bool) {
	match := netstatProcessPattern.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(match[1])
	return match[2], pid, err == nil && pid > 0
}

type servicePortCandidate struct {
	Port       int    `json:"port"`
	Process    string `json:"process,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Line       string `json:"line,omitempty"`
	Confidence string `json:"confidence"`
}

type servicePortDiscovery struct {
	Key            string                 `json:"key"`
	Process        string                 `json:"process"`
	ConfiguredPort int                    `json:"configured_port"`
	Candidates     []servicePortCandidate `json:"candidates"`
	State          string                 `json:"state"`
}

func discoverServicePorts(cfg *config.SysConfig) ([]servicePortDiscovery, error) {
	ports, err := listeningProcessPorts()
	if err != nil {
		return nil, err
	}
	targets := []struct {
		key, process string
		configured   int
	}{
		{"game", "df_game_r", cfg.RobotGamePort},
		{"monitor", "df_monitor_r", cfg.MonitorPort},
		{"auction", "df_auction_r", cfg.AuctionPort},
		{"point", "df_point_r", cfg.PointPort},
		{"relay", "df_relay_r", cfg.RelayPort},
	}
	result := make([]servicePortDiscovery, 0, len(targets))
	for _, target := range targets {
		item := servicePortDiscovery{Key: target.key, Process: target.process, ConfiguredPort: target.configured, State: "not_running"}
		for _, entry := range ports {
			if !strings.Contains(strings.ToLower(entry.Process), strings.ToLower(target.process)) {
				continue
			}
			item.Candidates = append(item.Candidates, servicePortCandidate{Port: entry.Port, Process: entry.Process, PID: entry.PID, Line: entry.Line, Confidence: "high"})
		}
		sort.Slice(item.Candidates, func(i, j int) bool { return item.Candidates[i].Port < item.Candidates[j].Port })
		switch len(item.Candidates) {
		case 1:
			if item.Candidates[0].Port == target.configured {
				item.State = "matched"
			} else {
				item.State = "mismatch"
			}
		default:
			item.State = "multiple"
		}
		result = append(result, item)
	}
	return result, nil
}

func actualServicePortsCheck(ports map[int]listeningPort) diagnosticsCheck {
	targets := []string{"df_game_r", "df_monitor_r", "df_auction_r", "df_point_r", "df_relay_r", "robot"}
	observed := map[string][]int{}
	for _, entry := range ports {
		for _, target := range targets {
			if strings.Contains(entry.Process, target) {
				observed[target] = append(observed[target], entry.Port)
			}
		}
	}
	for name := range observed {
		sort.Ints(observed[name])
	}
	status := diagOK
	msg := "service process ports collected"
	if len(observed) == 0 {
		status = diagWarn
		msg = "no known service process ports found"
	}
	return diagnosticsCheck{Name: "actual service ports", Status: status, Message: msg, Observed: observed}
}

func executablePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

func fileCheck(name, path string, required bool) diagnosticsCheck {
	info, err := os.Stat(path)
	if err != nil {
		status := diagWarn
		if required {
			status = diagError
		}
		return diagnosticsCheck{Name: name, Status: status, Message: err.Error(), Expected: path}
	}
	return diagnosticsCheck{
		Name:    name,
		Status:  diagOK,
		Message: "file exists",
		Observed: map[string]interface{}{
			"path":     path,
			"size":     info.Size(),
			"mod_time": info.ModTime().Format(time.RFC3339),
		},
	}
}

func compareFileHashCheck(name, a, b string) diagnosticsCheck {
	ha, ia, errA := fileSHA256(a)
	hb, ib, errB := fileSHA256(b)
	if errA != nil || errB != nil {
		return diagnosticsCheck{Name: name, Status: diagError, Message: fmt.Sprintf("read failed: %v %v", errA, errB), Expected: []string{a, b}}
	}
	status := diagOK
	msg := "files match"
	if ha != hb || ia.Size() != ib.Size() {
		status = diagError
		msg = "files differ"
	}
	return diagnosticsCheck{Name: name, Status: status, Message: msg, Observed: map[string]interface{}{
		"source": map[string]interface{}{"path": a, "size": ia.Size(), "sha256": ha, "mod_time": ia.ModTime().Format(time.RFC3339)},
		"target": map[string]interface{}{"path": b, "size": ib.Size(), "sha256": hb, "mod_time": ib.ModTime().Format(time.RFC3339)},
	}}
}

func fileSHA256(path string) (string, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), info, nil
}

func (b *diagnosticsBuilder) pvfManifestCheck() diagnosticsCheck {
	manifestPath := layout.New(b.cfg.ConfigDir).PVFManifest()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return diagnosticsCheck{Name: "pvf manifest freshness", Status: diagError, Message: err.Error(), Expected: manifestPath}
	}
	var manifest struct {
		Version int    `json:"version"`
		Source  string `json:"source"`
		Size    int64  `json:"size"`
		ModTime int64  `json:"mod_time"`
		MD5     string `json:"md5"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return diagnosticsCheck{Name: "pvf manifest freshness", Status: diagError, Message: err.Error(), Expected: manifestPath}
	}
	info, err := os.Stat(manifest.Source)
	if err != nil {
		return diagnosticsCheck{Name: "pvf manifest freshness", Status: diagError, Message: err.Error(), Observed: manifest}
	}
	if manifest.MD5 == "" {
		return diagnosticsCheck{Name: "pvf manifest freshness", Status: diagError, Message: "PVF manifest has no source digest", Observed: manifest}
	}
	sourceMD5, err := md5File(manifest.Source)
	if err != nil {
		return diagnosticsCheck{Name: "pvf manifest freshness", Status: diagError, Message: err.Error(), Observed: manifest}
	}
	status := diagOK
	msg := "PVF export matches source content"
	if info.Size() != manifest.Size || info.ModTime().Unix() != manifest.ModTime || sourceMD5 != manifest.MD5 {
		status = diagWarn
		msg = "PVF source content changed; export is stale"
	}
	return diagnosticsCheck{Name: "pvf manifest freshness", Status: status, Message: msg, Observed: manifest, Details: map[string]interface{}{"source_size": info.Size(), "source_mod_time": info.ModTime().Format(time.RFC3339), "source_md5": sourceMD5}}
}

func md5File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func portDialCheck(name, host string, port int) diagnosticsCheck {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return diagnosticsCheck{Name: name, Status: diagError, Message: err.Error(), Expected: addr}
	}
	_ = conn.Close()
	return diagnosticsCheck{Name: name, Status: diagOK, Message: "tcp port is reachable", Observed: addr}
}

func udpListeningCheck(name string, port int) diagnosticsCheck {
	out, err := exec.Command("ss", "-lunp").Output()
	if err != nil {
		return diagnosticsCheck{Name: name, Status: diagWarn, Message: err.Error(), Expected: port}
	}
	pattern := regexp.MustCompile(`[:.]` + regexp.QuoteMeta(strconv.Itoa(port)) + `\s`)
	for _, line := range strings.Split(string(out), "\n") {
		if pattern.MatchString(line) {
			return diagnosticsCheck{Name: name, Status: diagOK, Message: "udp port is listening", Observed: strings.TrimSpace(line)}
		}
	}
	return diagnosticsCheck{Name: name, Status: diagError, Message: "udp port is not listening", Expected: port}
}

func recentLogPatternCheck(name, path string, patterns []string) diagnosticsCheck {
	text, err := tailText(path, 1024*1024)
	if err != nil {
		return diagnosticsCheck{Name: name, Status: diagWarn, Message: err.Error(), Expected: path}
	}
	hits := map[string]int{}
	total := 0
	lower := strings.ToLower(text)
	for _, pattern := range patterns {
		count := strings.Count(lower, strings.ToLower(pattern))
		if count > 0 {
			hits[pattern] = count
			total += count
		}
	}
	if total > 0 {
		return diagnosticsCheck{Name: name, Status: diagWarn, Message: fmt.Sprintf("found %d recent keyword hits", total), Observed: hits}
	}
	return diagnosticsCheck{Name: name, Status: diagOK, Message: "no recent keyword hits", Observed: path}
}

func tailText(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		buf.WriteString(scanner.Text())
		buf.WriteByte('\n')
	}
	return buf.String(), scanner.Err()
}

func logSizeCheck(path string, limit int64) diagnosticsCheck {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return diagnosticsCheck{Name: filepath.Base(path), Status: diagOK, Message: "log file does not exist yet", Observed: path}
	}
	if err != nil {
		return diagnosticsCheck{Name: filepath.Base(path), Status: diagWarn, Message: err.Error(), Observed: path}
	}
	status := diagOK
	msg := "log size is within configured limit"
	if info.Size() > limit {
		status = diagWarn
		msg = "log file exceeds configured per-file limit"
	}
	return diagnosticsCheck{Name: filepath.Base(path), Status: status, Message: msg, Expected: limit, Observed: map[string]interface{}{"path": path, "size": info.Size(), "mod_time": info.ModTime().Format(time.RFC3339)}}
}

func boolCheck(name string, ok bool, err error, okMsg, badMsg string, observed interface{}) diagnosticsCheck {
	if err != nil {
		return diagnosticsCheck{Name: name, Status: diagError, Message: err.Error(), Observed: observed}
	}
	if !ok {
		return diagnosticsCheck{Name: name, Status: diagError, Message: badMsg, Observed: observed}
	}
	return diagnosticsCheck{Name: name, Status: diagOK, Message: okMsg, Observed: observed}
}
