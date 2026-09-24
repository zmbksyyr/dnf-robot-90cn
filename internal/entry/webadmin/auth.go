package webadmin

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	foundationlog "robot/internal/foundation/log"
	"strings"
	"time"
)

const maxWebSessions = 64

const (
	loginFailureWindow = 5 * time.Minute
	loginBlockDuration = time.Minute
	maxLoginFailures   = 5
	maxLoginPeers      = 1024
)

type loginFailure struct {
	count        int
	windowStart  time.Time
	blockedUntil time.Time
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if !s.authed(r) {
		s.writeLogin(w, "")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cleanIndexTemplate.Execute(w, map[string]interface{}{
		"RobotAddr": s.robotAddr,
		"WebAddr":   s.webAddr,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeLogin(w, "")
		return
	}
	_ = r.ParseForm()
	peer := loginPeer(r)
	if retryAfter, blocked := s.loginBlocked(peer, time.Now()); blocked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
		http.Error(w, "too many login failures", http.StatusTooManyRequests)
		return
	}
	password := r.Form.Get("password")
	if strings.TrimSpace(s.cfg.WebPassword) == "" {
		s.writeLogin(w, "web password is not configured")
		return
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(s.cfg.WebPassword)) == 1 {
		s.clearLoginFailures(peer)
		token := randomToken()
		s.tokenMu.Lock()
		now := time.Now()
		s.cleanupExpiredTokensLocked(now)
		s.storeSessionTokenLocked(token, now.Add(12*time.Hour))
		active := len(s.tokens)
		s.tokenMu.Unlock()
		foundationlog.Robotf("WEB_SESSION_CREATED pid=%d active=%d remote=%s\n", os.Getpid(), active, r.RemoteAddr)
		http.SetCookie(w, &http.Cookie{Name: "tw_web_token", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.recordLoginFailure(peer, time.Now())
	s.writeLogin(w, "password error")
}

func loginPeer(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) loginBlocked(peer string, now time.Time) (time.Duration, bool) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.cleanupLoginFailuresLocked(now)
	failure, ok := s.loginFailures[peer]
	if !ok || !now.Before(failure.blockedUntil) {
		return 0, false
	}
	return failure.blockedUntil.Sub(now), true
}

func (s *Server) recordLoginFailure(peer string, now time.Time) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.cleanupLoginFailuresLocked(now)
	failure := s.loginFailures[peer]
	if failure.windowStart.IsZero() || now.Sub(failure.windowStart) >= loginFailureWindow {
		failure = loginFailure{windowStart: now}
	}
	failure.count++
	if failure.count >= maxLoginFailures {
		failure.blockedUntil = now.Add(loginBlockDuration)
	}
	if len(s.loginFailures) >= maxLoginPeers {
		for candidate := range s.loginFailures {
			delete(s.loginFailures, candidate)
			break
		}
	}
	s.loginFailures[peer] = failure
}

func (s *Server) clearLoginFailures(peer string) {
	s.tokenMu.Lock()
	delete(s.loginFailures, peer)
	s.tokenMu.Unlock()
}

func (s *Server) cleanupLoginFailuresLocked(now time.Time) {
	for peer, failure := range s.loginFailures {
		if !failure.blockedUntil.IsZero() && now.Before(failure.blockedUntil) {
			continue
		}
		if now.Sub(failure.windowStart) >= loginFailureWindow {
			delete(s.loginFailures, peer)
		}
	}
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("tw_web_token"); err == nil {
		s.tokenMu.Lock()
		delete(s.tokens, c.Value)
		s.tokenMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "tw_web_token", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) writeLogin(w http.ResponseWriter, errText string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cleanLoginTemplate.Execute(w, map[string]interface{}{"Error": errText, "Recovery": s.recoveryMode}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) authed(r *http.Request) bool {
	if strings.TrimSpace(s.cfg.WebPassword) == "" {
		return false
	}
	c, err := r.Cookie("tw_web_token")
	if err != nil {
		return false
	}
	if c.Value == "" {
		foundationlog.Robotf("WEB_AUTH_REJECTED pid=%d reason=empty_token path=%s remote=%s\n", os.Getpid(), r.URL.Path, r.RemoteAddr)
		return false
	}
	now := time.Now()
	s.tokenMu.Lock()
	expires, ok := s.tokens[c.Value]
	if ok && now.After(expires) {
		delete(s.tokens, c.Value)
		ok = false
	}
	if ok {
		s.tokens[c.Value] = now.Add(12 * time.Hour)
	}
	s.cleanupExpiredTokensLocked(now)
	active := len(s.tokens)
	s.tokenMu.Unlock()
	if !ok {
		foundationlog.Robotf("WEB_AUTH_REJECTED pid=%d reason=unknown_or_expired_token active=%d path=%s remote=%s\n", os.Getpid(), active, r.URL.Path, r.RemoteAddr)
	}
	return ok
}

func (s *Server) sessionCount() int {
	s.tokenMu.RLock()
	defer s.tokenMu.RUnlock()
	return len(s.tokens)
}

func (s *Server) cleanupExpiredTokensLocked(now time.Time) {
	for token, expires := range s.tokens {
		if !now.Before(expires) {
			delete(s.tokens, token)
		}
	}
}

func (s *Server) storeSessionTokenLocked(token string, expires time.Time) {
	if len(s.tokens) >= maxWebSessions {
		var oldestToken string
		var oldestExpiry time.Time
		for candidate, candidateExpiry := range s.tokens {
			if oldestToken == "" || candidateExpiry.Before(oldestExpiry) {
				oldestToken = candidate
				oldestExpiry = candidateExpiry
			}
		}
		delete(s.tokens, oldestToken)
	}
	s.tokens[token] = expires
}

func randomToken() string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(fmt.Sprintf("webadmin random token: %v", err))
	}
	return hex.EncodeToString(raw[:])
}
