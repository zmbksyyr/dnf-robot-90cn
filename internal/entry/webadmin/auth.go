package webadmin

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"robot/internal/foundation/config"
	foundationlog "robot/internal/foundation/log"
)

const maxWebSessions = 64

const (
	loginFailureWindow = 5 * time.Minute
	loginBlockDuration = time.Minute
	maxLoginFailures   = 5
	maxLoginPeers      = 1024
	// maxGlobalLoginFailures bounds total failures per window so source-address
	// rotation cannot raise the effective guessing rate without limit.
	maxGlobalLoginFailures = 30
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
	peer := s.loginPeer(r)
	if retryAfter, blocked := s.loginBlocked(peer, time.Now()); blocked {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(retryAfter.Seconds()))))
		http.Error(w, "too many login failures", http.StatusTooManyRequests)
		return
	}
	password := r.Form.Get("password")
	if strings.TrimSpace(s.cfg.WebPassword) == "" && strings.TrimSpace(s.cfg.WebPasswordHash) == "" {
		s.writeLogin(w, "web password is not configured")
		return
	}
	if config.VerifyWebPassword(s.cfg.WebPassword, s.cfg.WebPasswordHash, password) {
		s.clearLoginFailures(peer)
		token, err := randomToken()
		if err != nil {
			http.Error(w, "session token generation failed", http.StatusInternalServerError)
			return
		}
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

// loginPeer resolves the client address used for login rate limiting. When the
// direct peer is a configured trusted proxy, the rightmost untrusted address
// from X-Forwarded-For is used so a reverse proxy does not collapse every
// client into one rate-limit bucket.
func (s *Server) loginPeer(r *http.Request) string {
	host := remoteHost(r.RemoteAddr)
	if s == nil || s.cfg == nil || len(s.cfg.WebTrustedProxies) == 0 || !trustedProxy(host, s.cfg.WebTrustedProxies) {
		return host
	}
	forwarded := splitForwardedFor(r.Header.Get("X-Forwarded-For"))
	for i := len(forwarded) - 1; i >= 0; i-- {
		candidate := forwarded[i]
		if candidate == "" || trustedProxy(candidate, s.cfg.WebTrustedProxies) {
			continue
		}
		return candidate
	}
	return host
}

func remoteHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(remoteAddr)
}

func splitForwardedFor(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func trustedProxy(host string, trusted []string) bool {
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	for _, entry := range trusted {
		if candidate := net.ParseIP(entry); candidate != nil {
			if candidate.Equal(ip) {
				return true
			}
			continue
		}
		if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) loginBlocked(peer string, now time.Time) (time.Duration, bool) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.cleanupLoginFailuresLocked(now)
	if now.Before(s.loginGlobalBlockedUntil) {
		return s.loginGlobalBlockedUntil.Sub(now), true
	}
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
	if s.loginGlobalWindow.IsZero() || now.Sub(s.loginGlobalWindow) >= loginFailureWindow {
		s.loginGlobalWindow = now
		s.loginGlobalCount = 0
	}
	s.loginGlobalCount++
	if s.loginGlobalCount >= maxGlobalLoginFailures {
		s.loginGlobalBlockedUntil = now.Add(loginBlockDuration)
		s.loginGlobalCount = 0
		s.loginGlobalWindow = time.Time{}
	}
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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, err := r.Cookie("tw_web_token"); err == nil {
		s.tokenMu.Lock()
		delete(s.tokens, c.Value)
		s.tokenMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "tw_web_token", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// requireSameOrigin rejects state-changing requests whose Origin header names a
// different host. Requests without Origin (non-browser clients) still rely on
// the SameSite cookie policy, which already blocks cross-site cookie delivery.
// Configured Web.AllowedOrigins entries are accepted as-is, and a configured
// trusted reverse proxy may supply the original host through X-Forwarded-Host
// when it rewrites the Host header.
func (s *Server) requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && !s.originAllowed(origin, r) {
			foundationlog.Robotf("WEB_ORIGIN_REJECTED origin=%q host=%q forwarded_host=%q peer=%s\n",
				origin, r.Host, strings.TrimSpace(r.Header.Get("X-Forwarded-Host")), r.RemoteAddr)
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) originAllowed(origin string, r *http.Request) bool {
	if sameOriginHost(origin, r.Host) {
		return true
	}
	if s == nil || s.cfg == nil {
		return false
	}
	// A null origin comes from sandboxed or embedded shells that cannot send a
	// real origin. It is accepted by default so a stock build serves those
	// clients without configuration; the SameSite=Lax session cookie remains
	// the primary CSRF defence because it is not attached to cross-site POST
	// requests. Deployments that want stricter behaviour set
	// Web.AllowNullOrigin = false.
	if origin == "null" {
		return s.cfg.WebAllowNullOrigin
	}
	for _, allowed := range s.cfg.WebAllowedOrigins {
		if sameConfiguredOrigin(origin, allowed) {
			return true
		}
	}
	if !trustedProxy(remoteHost(r.RemoteAddr), s.cfg.WebTrustedProxies) {
		return false
	}
	for _, forwarded := range splitForwardedHost(r.Header.Get("X-Forwarded-Host")) {
		if sameOriginHost(origin, forwarded) {
			return true
		}
	}
	return false
}

func sameOriginHost(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host)
}

// sameConfiguredOrigin compares a browser Origin with a configured origin.
// Both carry a scheme and host; the comparison is case-insensitive and ignores
// a trailing slash on the configured value.
func sameConfiguredOrigin(origin, allowed string) bool {
	parsedOrigin, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || parsedOrigin.Host == "" {
		return false
	}
	parsedAllowed, err := url.Parse(strings.TrimSpace(allowed))
	if err != nil || parsedAllowed.Host == "" {
		return false
	}
	return strings.EqualFold(parsedOrigin.Scheme, parsedAllowed.Scheme) &&
		strings.EqualFold(parsedOrigin.Host, parsedAllowed.Host)
}

func splitForwardedHost(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
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
	if strings.TrimSpace(s.cfg.WebPassword) == "" && strings.TrimSpace(s.cfg.WebPasswordHash) == "" {
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

func randomToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("webadmin random token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
