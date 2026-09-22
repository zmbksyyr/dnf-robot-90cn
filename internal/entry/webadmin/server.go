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
}

type partySkillFileState struct {
	enabled bool
}

func New(cfg *config.SysConfig, robotAddr, webAddr string) *Server {
	if robotAddr == "" {
		robotAddr = fmt.Sprintf("127.0.0.1:%d", cfg.RobotPort)
	}
	if webAddr == "" {
		webAddr = fmt.Sprintf("0.0.0.0:%d", cfg.WebPort)
	}
	return &Server{
		cfg:              cfg,
		robotAddr:        robotAddr,
		webAddr:          webAddr,
		tokens:           make(map[string]time.Time),
		loginFailures:    make(map[string]loginFailure),
		partyCompatWake:  make(chan struct{}, 1),
		mailboxGuardWake: make(chan struct{}, 1),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer s.stopServerScript()
	stopRuntimeFiles := s.startRuntimeFileWatcher()
	defer stopRuntimeFiles()
	stopPartyCompat := s.startPartyCompatSupervisor()
	defer stopPartyCompat()
	stopMailboxGuard := s.startMailboxGuardSupervisor()
	defer stopMailboxGuard()
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
