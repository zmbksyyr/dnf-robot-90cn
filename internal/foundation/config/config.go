package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"robot/internal/foundation/atomicfile"
	"robot/internal/foundation/serviceinit"
)

const defaultDNFServiceRoot = "/home/neople"

var discoverInitialExternalPorts = serviceinit.DiscoverExternalPorts

// SysConfig holds all robot configuration from config.ini.
type SysConfig struct {
	RobotPort             int
	DBHost                string
	DBPort                int
	DBName                string
	DBUser                string
	DBPassword            string
	DFGameR               string
	ServiceRoot           string
	ServiceRunScript      string
	AuctionHost           string
	PointHost             string
	RelayHost             string
	ConfigDir             string
	RobotInnerIP          string
	RobotConnectIP        string
	RobotConnectIPSetting string
	RobotGamePort         int
	GameServerGroup       int
	MonitorPort           int
	AuctionPort           int
	PointPort             int
	RelayPort             int
	PartyRoute0Port       int
	DBInitSize            int
	DBMaxSize             int
	DBDialTimeoutSec      int
	DBReadTimeoutSec      int
	DBWriteTimeoutSec     int
	DBConnMaxLifetimeSec  int
	WebPort               int
	WebPassword           string
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
	cfg.RobotGamePort = dec.Int("Ports", "Game", 10011)
	cfg.MonitorPort = dec.Int("Ports", "Monitor", 30303)
	cfg.AuctionPort = dec.Int("Ports", "Auction", 30803)
	cfg.PointPort = dec.Int("Ports", "Point", 30603)
	cfg.RelayPort = dec.Int("Ports", "Relay", 7200)
	cfg.PartyRoute0Port = dec.Int("Ports", "PartyRoute0", 5063)

	// [Robot] section
	cfg.DFGameR = dec.String("Robot", "DfGameR", "/home/neople/game/df_game_r")
	cfg.RobotInnerIP = dec.String("Robot", "RobotInnerIp", "10.0.0.1")
	cfg.RobotConnectIPSetting = strings.TrimSpace(dec.String("Robot", "RobotConnectIp", "auto"))
	if cfg.RobotConnectIPSetting == "" {
		cfg.RobotConnectIPSetting = "auto"
	}
	cfg.RobotConnectIP = cfg.RobotConnectIPSetting
	cfg.GameServerGroup = dec.Int("Robot", "GameServerGroup", 3)

	// [Services] section
	cfg.ServiceRoot = dec.String("Services", "Root", defaultDNFServiceRoot)
	cfg.ServiceRunScript = dec.String("Services", "RunScript", serviceinit.DefaultRunScript)
	cfg.AuctionHost = dec.String("Services", "AuctionHost", "127.0.0.1")
	cfg.PointHost = dec.String("Services", "PointHost", "127.0.0.1")
	cfg.RelayHost = dec.String("Services", "RelayHost", "127.0.0.1")

	// [Web] section
	cfg.WebPassword = dec.String("Web", "WebPassword", "twadmin")

	// [db] section
	cfg.DBHost = dec.String("db", "db_host", "127.0.0.1")
	cfg.DBUser = dec.String("db", "db_user_name", "game")
	cfg.DBPassword = dec.String("db", "db_password", "uu5!^%jg")
	cfg.DBName = dec.String("db", "db_database_name", "d_taiwan")
	cfg.DBPort = dec.Int("db", "db_port", 3306)
	cfg.DBInitSize = dec.Int("db", "db_init_size", 4)
	cfg.DBMaxSize = dec.Int("db", "db_max_size", 64)
	cfg.DBDialTimeoutSec = dec.Int("db", "db_dial_timeout_sec", 5)
	cfg.DBReadTimeoutSec = dec.Int("db", "db_read_timeout_sec", 30)
	cfg.DBWriteTimeoutSec = dec.Int("db", "db_write_timeout_sec", 30)
	cfg.DBConnMaxLifetimeSec = dec.Int("db", "db_conn_max_lifetime_sec", 1800)

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
	// below the upper TCP port boundary.
	dec.Check("Ports", "Game", cfg.RobotGamePort >= 1 && cfg.RobotGamePort <= 64535, "must be between 1 and 64535")
	checkPort("Ports", "Monitor", cfg.MonitorPort)
	checkPort("Ports", "Auction", cfg.AuctionPort)
	checkPort("Ports", "Point", cfg.PointPort)
	checkPort("Ports", "Relay", cfg.RelayPort)
	checkPort("Ports", "PartyRoute0", cfg.PartyRoute0Port)
	dec.Check("Robot", "DfGameR", strings.TrimSpace(cfg.DFGameR) != "", "must not be empty")
	dec.Check("Robot", "RobotInnerIp", strings.TrimSpace(cfg.RobotInnerIP) != "", "must not be empty")
	dec.Check("Robot", "RobotConnectIp", strings.TrimSpace(cfg.RobotConnectIPSetting) != "", "must not be empty")
	dec.Check("Robot", "GameServerGroup", cfg.GameServerGroup >= 0 && uint64(cfg.GameServerGroup) <= uint64(^uint32(0)), "must be between 0 and 4294967295")
	dec.Check("Services", "Root", absoluteConfigPath(cfg.ServiceRoot), "must be an absolute path")
	dec.Check("Services", "RunScript", absoluteConfigPath(cfg.ServiceRunScript), "must be an absolute path")
	dec.Check("Services", "AuctionHost", strings.TrimSpace(cfg.AuctionHost) != "", "must not be empty")
	dec.Check("Services", "PointHost", strings.TrimSpace(cfg.PointHost) != "", "must not be empty")
	dec.Check("Services", "RelayHost", strings.TrimSpace(cfg.RelayHost) != "", "must not be empty")
	dec.Check("Web", "WebPassword", strings.TrimSpace(cfg.WebPassword) != "", "must not be empty")
	dec.Check("db", "db_host", strings.TrimSpace(cfg.DBHost) != "", "must not be empty")
	dec.Check("db", "db_user_name", strings.TrimSpace(cfg.DBUser) != "", "must not be empty")
	dec.Check("db", "db_database_name", strings.TrimSpace(cfg.DBName) != "", "must not be empty")
	checkPort("db", "db_port", cfg.DBPort)
	dec.Check("db", "db_init_size", cfg.DBInitSize >= 1, "must be positive")
	dec.Check("db", "db_max_size", cfg.DBMaxSize >= 1, "must be positive")
	dec.Check("db", "db_max_size", cfg.DBMaxSize >= cfg.DBInitSize, "must be greater than or equal to db_init_size")
	dec.Check("db", "db_dial_timeout_sec", cfg.DBDialTimeoutSec >= 1 && cfg.DBDialTimeoutSec <= 30, "must be between 1 and 30")
	dec.Check("db", "db_read_timeout_sec", cfg.DBReadTimeoutSec >= 1 && cfg.DBReadTimeoutSec <= 120, "must be between 1 and 120")
	dec.Check("db", "db_write_timeout_sec", cfg.DBWriteTimeoutSec >= 1 && cfg.DBWriteTimeoutSec <= 120, "must be between 1 and 120")
	dec.Check("db", "db_conn_max_lifetime_sec", cfg.DBConnMaxLifetimeSec >= 60 && cfg.DBConnMaxLifetimeSec <= 86400, "must be between 60 and 86400")
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

func usableLocalIPv4(ip net.IP) bool {
	ip = ip.To4()
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

func absoluteConfigPath(path string) bool {
	path = strings.TrimSpace(path)
	return filepath.IsAbs(path) || strings.HasPrefix(path, "/")
}

func generateDefaultConfig(path string) error {
	discovered := discoverInitialExternalPorts(serviceinit.DefaultRunScript, serviceinit.DefaultHomeRoot)
	portLines := []string{
		"RobotAPI = 8111",
		"Web = 8112",
	}
	portLines = append(portLines, initialPortConfigLine("Game", 10011, discovered.Game, 64535)...)
	portLines = append(portLines, "# Robot-owned UDP listener; not discovered from /root/run.", "PartyRoute0 = 5063")

	lines := []string{
		"# robot main config. Restart robot after editing.",
		"[Ports]",
	}
	lines = append(lines, portLines...)
	lines = append(lines,
		"",
		"[Robot]",
		"# df_game_r path, used for runtime self-check and PVF export.",
		"DfGameR = /home/neople/game/df_game_r",
		"# Inner game IP written into robot login data.",
		"RobotInnerIp = 10.0.0.1",
		"# Game connection host. Use auto to resolve the primary local IPv4 at runtime.",
		"RobotConnectIp = auto",
		"# S4A21 game server group used for cache invalidation packets.",
		"GameServerGroup = 3",
		"[Web]",
		"# Web login password.",
		"WebPassword = twadmin",
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

func initialPortConfigLine(name string, fallback int, discovered serviceinit.PortValue, max int) []string {
	if discovered.Port < 1 || discovered.Port > max {
		return []string{fmt.Sprintf("%s = %d", name, fallback)}
	}
	source := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(discovered.Source), "\r", " "), "\n", " ")
	if source == "" {
		source = serviceinit.DefaultRunScript
	}
	return []string{
		fmt.Sprintf("# discovered from %s", source),
		fmt.Sprintf("%s = %d", name, discovered.Port),
	}
}
