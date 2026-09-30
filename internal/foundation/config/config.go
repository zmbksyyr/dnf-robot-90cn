package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"robot/internal/foundation/atomicfile"
)

// SysConfig holds all robot configuration from config.ini.
type SysConfig struct {
	RobotPort             int
	ServerDirectory       string
	ConfigDir             string
	RobotInnerIP          string
	RobotConnectIP        string
	RobotConnectIPSetting string
	RobotGamePort         int
	WebPort               int
	WebPassword           string
	WebPasswordHash       string
	WebTrustedProxies     []string
	WebAllowedOrigins     []string
	WebAllowNullOrigin    bool
	LogMaxSizeMB          int
	LogMaxBackups         int
	MaxResponseBytes      int
}

// LoadConfig reads config.ini and returns a populated SysConfig.
// If the config file does not exist, a default one is generated first.
func LoadConfig(path string) (*SysConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("empty config path")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := generateDefaultConfig(path); err != nil {
			return nil, fmt.Errorf("generate default config: %w", err)
		}
	}

	ini, err := Load(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return decodeSysConfig(ini)
}

// ParseConfig validates and decodes a complete main configuration before it
// is published to disk.
func ParseConfig(text string) (*SysConfig, error) {
	ini, err := LoadFromString(text)
	if err != nil {
		return nil, err
	}
	return decodeSysConfig(ini)
}

func decodeSysConfig(ini *INIConfig) (*SysConfig, error) {
	dec := NewDecoder(ini, "main config")

	cfg := &SysConfig{}

	// [Ports] section
	cfg.RobotPort = dec.Int("Ports", "RobotAPI", 8111)
	cfg.WebPort = dec.Int("Ports", "Web", 8112)
	// Game port 0 means "resolve the channel port from the selected adapter's
	// server instance"; an operator override stays an explicit positive value.
	cfg.RobotGamePort = dec.Int("Ports", "Game", 0)
	// Deprecated: the legacy native-server route-0 UDP port. A config.ini
	// carried over from that profile must still load, but the current adapter
	// has no route-0 plane, so the value is intentionally ignored.
	_ = dec.Int("Ports", "PartyRoute0", 0)

	// [Robot] section
	cfg.ServerDirectory = strings.TrimSpace(dec.String("Robot", "ServerDirectory", ""))
	cfg.RobotInnerIP = dec.String("Robot", "RobotInnerIp", "10.0.0.1")
	cfg.RobotConnectIPSetting = strings.TrimSpace(dec.String("Robot", "RobotConnectIp", "auto"))
	if cfg.RobotConnectIPSetting == "" {
		cfg.RobotConnectIPSetting = "auto"
	}
	cfg.RobotConnectIP = cfg.RobotConnectIPSetting

	// [Web] section
	cfg.WebPassword = dec.String("Web", "WebPassword", "twadmin")
	cfg.WebPasswordHash = strings.TrimSpace(dec.String("Web", "WebPasswordHash", ""))
	trustedProxies, err := parseTrustedProxies(dec.String("Web", "TrustedProxies", ""))
	if err != nil {
		return nil, err
	}
	cfg.WebTrustedProxies = trustedProxies
	allowedOrigins, err := parseAllowedOrigins(dec.String("Web", "AllowedOrigins", ""))
	if err != nil {
		return nil, err
	}
	cfg.WebAllowedOrigins = allowedOrigins
	// Null origins come from embedded shells, WebView launchers and local
	// proxy stacks that cannot send a real origin; they are accepted by
	// default so a stock build works without extra configuration. Set the key
	// to false to reject them for a stricter deployment.
	cfg.WebAllowNullOrigin = dec.Bool("Web", "AllowNullOrigin", true)

	// [system] section
	cfg.LogMaxSizeMB = dec.Int("system", "log_max_size_mb", 100)
	cfg.LogMaxBackups = dec.Int("system", "log_max_backups", 5)
	cfg.MaxResponseBytes = dec.Int("system", "max_response_bytes", 4*1024*1024)

	checkPort := func(section, key string, port int) {
		dec.Check(section, key, port >= 1 && port <= 65535, "must be between 1 and 65535")
	}
	checkPort("Ports", "RobotAPI", cfg.RobotPort)
	checkPort("Ports", "Web", cfg.WebPort)
	// The cache invalidation UDP port is Game+1000, so Game must leave room
	// below the upper TCP port boundary when it is configured explicitly.
	dec.Check("Ports", "Game", cfg.RobotGamePort >= 0 && cfg.RobotGamePort <= 64535, "must be 0 or between 1 and 64535")
	dec.Check("Robot", "RobotInnerIp", strings.TrimSpace(cfg.RobotInnerIP) != "", "must not be empty")
	dec.Check("Robot", "RobotConnectIp", strings.TrimSpace(cfg.RobotConnectIPSetting) != "", "must not be empty")
	dec.Check("Web", "WebPassword", strings.TrimSpace(cfg.WebPassword) != "" || cfg.WebPasswordHash != "", "must not be empty unless WebPasswordHash is set")
	dec.Check("system", "log_max_size_mb", cfg.LogMaxSizeMB >= 1, "must be positive")
	dec.Check("system", "log_max_backups", cfg.LogMaxBackups >= 1, "must be positive")
	dec.Check("system", "max_response_bytes", cfg.MaxResponseBytes >= 1, "must be positive")
	if err := dec.Validate(); err != nil {
		return nil, err
	}

	// Resolve the explicit auto marker for runtime consumers while preserving
	// the configured value for diagnostics and restart comparisons.
	if strings.EqualFold(cfg.RobotConnectIPSetting, "auto") {
		cfg.RobotConnectIP = preferredLocalIPv4()
	}

	return cfg, nil
}

func preferredLocalIPv4() string {
	if conn, err := net.Dial("udp4", "198.18.0.1:9"); err == nil {
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && usableLocalIPv4(addr.IP) {
			_ = conn.Close()
			return addr.IP.String()
		}
		_ = conn.Close()
	}
	interfaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, addrErr := iface.Addrs()
			if addrErr != nil {
				continue
			}
			for _, addr := range addrs {
				ip, _, parseErr := net.ParseCIDR(addr.String())
				if parseErr == nil && usableLocalIPv4(ip) {
					return ip.String()
				}
			}
		}
	}
	return "127.0.0.1"
}

// parseTrustedProxies splits a comma-separated list of IPs or CIDR blocks that
// may supply X-Forwarded-For. Empty entries are ignored; invalid entries fail
// the configuration load instead of being silently dropped.
func parseTrustedProxies(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	values := make([]string, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if net.ParseIP(entry) != nil {
			values = append(values, entry)
			continue
		}
		if _, _, err := net.ParseCIDR(entry); err == nil {
			values = append(values, entry)
			continue
		}
		return nil, fmt.Errorf("Web.TrustedProxies entry %q is not an IP or CIDR block", entry)
	}
	return values, nil
}

// parseAllowedOrigins splits a comma-separated list of extra origins that may
// call the state-changing Web endpoints, for example a reverse-proxy or tunnel
// front end. Entries must be absolute http(s) origins without a path, query, or
// fragment; invalid entries fail the configuration load instead of being
// silently dropped.
func parseAllowedOrigins(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	values := make([]string, 0)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parsed, err := url.Parse(entry)
		if err != nil || parsed.Host == "" || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) {
			return nil, fmt.Errorf("Web.AllowedOrigins entry %q must be an absolute http(s) origin", entry)
		}
		if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("Web.AllowedOrigins entry %q must not contain a path, query, or fragment", entry)
		}
		values = append(values, strings.ToLower(parsed.Scheme)+"://"+strings.ToLower(parsed.Host))
	}
	return values, nil
}

func usableLocalIPv4(ip net.IP) bool {
	ip = ip.To4()
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

func generateDefaultConfig(path string) error {
	portLines := []string{
		"RobotAPI = 8111",
		"Web = 8112",
	}
	portLines = append(portLines, "Game = 0")

	lines := []string{
		"# robot main config. Restart robot after editing.",
		"[Ports]",
	}
	lines = append(lines, portLines...)
	lines = append(lines,
		"",
		"[Robot]",
		"# Selected adapter server directory. The Web setup writes the effective value.",
		"ServerDirectory =",
		"# Inner game IP written into robot login data.",
		"RobotInnerIp = 10.0.0.1",
		"# Game connection host. Use auto to resolve the primary local IPv4 at runtime.",
		"RobotConnectIp = auto",
		"[Web]",
		"# Web login password.",
		"WebPassword = twadmin",
		"# Reverse-proxy IPs or CIDR blocks trusted for X-Forwarded-For and X-Forwarded-Host.",
		"#TrustedProxies = 127.0.0.1",
		"# Extra origins allowed for state-changing Web requests (reverse proxy or tunnel front ends).",
		"#AllowedOrigins = https://panel.example.com",
		"# Accept requests whose Origin header is null (embedded WebView or launcher shells).",
		"# Set to false to reject them for a stricter deployment.",
		"#AllowNullOrigin = true",
		"",
		"[system]",
		"log_max_size_mb = 100",
		"log_max_backups = 5",
		"max_response_bytes = 4194304",
		"",
	)
	data := strings.Join(lines, "\n")
	_, err := atomicfile.WriteFileIfMissing(path, []byte(data), 0600)
	return err
}
