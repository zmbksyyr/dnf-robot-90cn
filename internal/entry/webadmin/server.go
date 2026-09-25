package webadmin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"robot/internal/foundation/config"
	"robot/internal/foundation/lockhub"
	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

type Server struct {
	cfg                     *config.SysConfig
	robotAddr               string
	webAddr                 string
	tokenMu                 lockhub.RWLocker
	tokens                  map[string]time.Time
	loginFailures           map[string]loginFailure
	partyCompatMu           lockhub.Locker
	partyCompatWake         chan struct{}
	mailboxGuardWake        chan struct{}
	mailboxGuardSnapshot    atomic.Pointer[mailboxGuardConfig]
	partyCompatSnapshot     atomic.Pointer[partyCompatConfig]
	partySkillSnapshot      atomic.Pointer[partySkillFileState]
	partyCompatFailures     int
	partyCompatFirstFailure time.Time
	partyCompatNextRetry    time.Time
	partyCompatLastError    string
	serverScriptMu          lockhub.Locker
	serverScript            serverScriptStatus
	serverScriptCancel      func()
	gameMaxUserMu           lockhub.Locker
	gameMaxUser             gameMaxUserCache
	backendSelectionMu      lockhub.Locker
	backend                 shared.BackendID
	backendInfo             shared.BackendInfo
	backendCatalog          []shared.BackendInfo
	recoveryMode            bool
	recoveryReason          string
}

// NewRecovery creates the backend-neutral Web surface used when the persisted
// backend cannot run on the current platform. It must not start any
// backend-specific watcher or supervisor before the operator selects a valid
// backend and restarts the process.
func NewRecovery(cfg *config.SysConfig, robotAddr, webAddr string, selected shared.BackendID, reason ...string) *Server {
	server := New(cfg, robotAddr, webAddr, selected)
	server.recoveryMode = true
	if len(reason) > 0 {
		server.recoveryReason = strings.TrimSpace(reason[0])
	}
	return server
}

func NewRecoveryWithCatalog(cfg *config.SysConfig, robotAddr, webAddr string, selected shared.BackendID, catalog []shared.BackendInfo, reason ...string) *Server {
	server := newServer(cfg, robotAddr, webAddr, selected, catalog)
	server.recoveryMode = true
	if len(reason) > 0 {
		server.recoveryReason = strings.TrimSpace(reason[0])
	}
	return server
}

type partySkillFileState struct {
	enabled bool
}

func New(cfg *config.SysConfig, robotAddr, webAddr string, backend ...shared.BackendID) *Server {
	selected := shared.DefaultBackendID()
	if len(backend) > 0 && backend[0] != "" {
		selected = backend[0]
	}
	info := shared.BackendInfo{ID: selected, DisplayName: string(selected), Selectable: true, Capabilities: shared.CapabilityMatrix(shared.CapabilityStatus{Enabled: true})}
	return newServer(cfg, robotAddr, webAddr, selected, []shared.BackendInfo{info})
}

func NewWithCatalog(cfg *config.SysConfig, robotAddr, webAddr string, backend shared.BackendID, catalog []shared.BackendInfo) *Server {
	return newServer(cfg, robotAddr, webAddr, backend, catalog)
}

func newServer(cfg *config.SysConfig, robotAddr, webAddr string, backend shared.BackendID, catalog []shared.BackendInfo) *Server {
	if robotAddr == "" {
		robotAddr = fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort)
	}
	if webAddr == "" {
		webAddr = fmt.Sprintf("0.0.0.0:%d", cfg.WebPort)
	}
	selectedBackend := backend
	if selectedBackend == "" {
		selectedBackend = shared.DefaultBackendID()
	}
	if len(catalog) == 0 {
		catalog = []shared.BackendInfo{{ID: selectedBackend, DisplayName: string(selectedBackend), Selectable: true, Capabilities: shared.CapabilityMatrix(shared.CapabilityStatus{Enabled: true})}}
	} else {
		catalog = append([]shared.BackendInfo(nil), catalog...)
	}
	selectedInfo := shared.BackendInfo{ID: selectedBackend, Capabilities: shared.CapabilityMatrix(shared.CapabilityStatus{})}
	for _, info := range catalog {
		if info.ID == selectedBackend {
			selectedInfo = info
			break
		}
	}
	return &Server{
		cfg:              cfg,
		robotAddr:        robotAddr,
		webAddr:          webAddr,
		tokens:           make(map[string]time.Time),
		loginFailures:    make(map[string]loginFailure),
		partyCompatWake:  make(chan struct{}, 1),
		mailboxGuardWake: make(chan struct{}, 1),
		backend:          selectedBackend,
		backendInfo:      selectedInfo,
		backendCatalog:   catalog,
	}
}

func (s *Server) rejectUnsupportedCapability(w http.ResponseWriter, operation shared.BackendCapability) bool {
	if s != nil && s.supportsBackendCapability(operation) {
		return false
	}
	backend := shared.BackendID("")
	reason := "Web operation is unavailable for the selected backend"
	if s != nil {
		backend = s.backend
		if status, ok := s.backendInfo.Capabilities[operation]; ok && status.Reason != "" {
			reason = status.Reason
		}
	}
	writeJSON(w, map[string]interface{}{
		"ok":    false,
		"error": shared.UnsupportedCapabilityError{Backend: backend, Operation: operation, Reason: reason}.Error(),
	})
	return true
}

func (s *Server) supportsBackendCapability(operation shared.BackendCapability) bool {
	if s == nil || s.recoveryMode {
		return false
	}
	return s.backendInfo.Supports(operation)
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer s.stopServerScript()
	stopRuntimeFiles := s.startRuntimeFileWatcher()
	defer stopRuntimeFiles()
	stopPartyCompat := func() {}
	stopMailboxGuard := func() {}
	if s.supportsBackendCapability(shared.CapabilityPartyCompatibility) {
		stopPartyCompat = s.startPartyCompatSupervisor()
		defer stopPartyCompat()
	}
	if s.supportsBackendCapability(shared.CapabilityMailboxGuard) {
		stopMailboxGuard = s.startMailboxGuardSupervisor()
		defer stopMailboxGuard()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/api/call", s.requireAuth(s.handleCall))
	mux.HandleFunc("/api/game-port", s.requireAuth(s.handleGamePort))
	mux.HandleFunc("/api/game-endpoint", s.requireAuth(s.handleGameEndpoint))
	mux.HandleFunc("/api/service-ports", s.requireAuth(s.handleServicePorts))
	mux.HandleFunc("/api/restart-robot", s.requireAuth(s.handleRestartRobot))
	mux.HandleFunc("/api/max-user", s.requireAuth(s.handleMaxUser))
	mux.HandleFunc("/api/server-script", s.requireAuth(s.handleServerScript))
	mux.HandleFunc("/api/monitor-service", s.requireAuth(s.handleMonitorService))
	mux.HandleFunc("/api/relay-service", s.requireAuth(s.handleRelayService))
	mux.HandleFunc("/api/party-compat", s.requireAuth(s.handlePartyCompat))
	mux.HandleFunc("/api/compat", s.requireAuth(s.handleCompat))
	mux.HandleFunc("/api/diagnostics", s.requireAuth(s.handleDiagnostics))
	mux.HandleFunc("/api/backend", s.requireAuth(s.handleBackend))
	mux.HandleFunc("/api/keypair-download", s.requireAuth(s.handleKeypairDownload))
	server := &http.Server{
		Addr:              s.webAddr,
		Handler:           s.withSecurityHeaders(s.withDiagnostics(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	foundationlog.Robotf("WEB_SERVER_LISTENING addr=%s robot_addr=%s pid=%d sessions=%d\n", s.webAddr, s.robotAddr, os.Getpid(), s.sessionCount())
	if strings.TrimSpace(s.cfg.WebPassword) == "twadmin" {
		foundationlog.Robotf("WEB_SECURITY_WARNING reason=default_password\n")
	}
	if host, _, err := net.SplitHostPort(s.webAddr); err == nil && (host == "" || host == "0.0.0.0" || host == "::") {
		foundationlog.Robotf("WEB_SECURITY_WARNING reason=all_interfaces addr=%s\n", s.webAddr)
	}
	serveDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.stopServerScript()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				foundationlog.Robotf("WEB_SERVER_SHUTDOWN_FAILED phase=graceful err=%v\n", err)
				if closeErr := server.Close(); closeErr != nil {
					foundationlog.Robotf("WEB_SERVER_SHUTDOWN_FAILED phase=forced err=%v\n", closeErr)
				}
			}
		case <-serveDone:
		}
	}()
	err := server.ListenAndServe()
	close(serveDone)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
