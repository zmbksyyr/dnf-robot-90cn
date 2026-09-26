package webadmin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"robot/internal/foundation/config"
)

func TestLoginPeerUsesForwardedForOnlyForTrustedProxies(t *testing.T) {
	trusted := &Server{cfg: &config.SysConfig{WebTrustedProxies: []string{"10.0.0.0/8"}}}
	proxied := httptest.NewRequest(http.MethodPost, "/login", nil)
	proxied.RemoteAddr = "10.0.0.5:1234"
	proxied.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.7")
	if got := trusted.loginPeer(proxied); got != "203.0.113.9" {
		t.Fatalf("trusted proxy peer = %q, want the rightmost untrusted address", got)
	}

	direct := &Server{}
	untrusted := httptest.NewRequest(http.MethodPost, "/login", nil)
	untrusted.RemoteAddr = "198.51.100.4:1234"
	untrusted.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := direct.loginPeer(untrusted); got != "198.51.100.4" {
		t.Fatalf("direct peer = %q, want the socket address", got)
	}
}

func TestLoginGlobalFailuresBlockAddressRotation(t *testing.T) {
	server := &Server{loginFailures: make(map[string]loginFailure)}
	now := time.Now()
	for i := 0; i < maxGlobalLoginFailures; i++ {
		server.recordLoginFailure(fmt.Sprintf("203.0.113.%d", i), now)
	}
	if retryAfter, blocked := server.loginBlocked("198.51.100.9", now); !blocked || retryAfter <= 0 {
		t.Fatalf("global limit did not block a fresh peer: blocked=%t retryAfter=%s", blocked, retryAfter)
	}
}

func TestSameOriginRejectsForeignOrigin(t *testing.T) {
	handler := (&Server{}).requireSameOrigin(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	foreign := httptest.NewRequest(http.MethodPost, "/api/call", nil)
	foreign.Host = "127.0.0.1:8112"
	foreign.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	handler(rec, foreign)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", rec.Code)
	}

	same := httptest.NewRequest(http.MethodPost, "/api/call", nil)
	same.Host = "127.0.0.1:8112"
	same.Header.Set("Origin", "http://127.0.0.1:8112")
	rec = httptest.NewRecorder()
	handler(rec, same)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("same origin status = %d, want pass-through", rec.Code)
	}

	// Non-browser clients without an Origin header keep working.
	plain := httptest.NewRequest(http.MethodPost, "/api/call", nil)
	rec = httptest.NewRecorder()
	handler(rec, plain)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("origin-less status = %d, want pass-through", rec.Code)
	}
}

func TestLogoutRequiresPost(t *testing.T) {
	server := &Server{tokens: make(map[string]time.Time)}
	get := httptest.NewRequest(http.MethodGet, "/logout", nil)
	rec := httptest.NewRecorder()
	server.handleLogout(rec, get)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /logout status = %d, want 405", rec.Code)
	}
	post := httptest.NewRequest(http.MethodPost, "/logout", nil)
	rec = httptest.NewRecorder()
	server.handleLogout(rec, post)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /logout status = %d, want 303", rec.Code)
	}
}

func TestStoreSessionTokenKeepsBoundedRecentSessions(t *testing.T) {
	server := &Server{tokens: make(map[string]time.Time)}
	base := time.Now()
	for i := 0; i < maxWebSessions; i++ {
		server.storeSessionTokenLocked(fmt.Sprintf("token-%d", i), base.Add(time.Duration(i)*time.Minute))
	}
	server.storeSessionTokenLocked("new-token", base.Add(24*time.Hour))

	if got := len(server.tokens); got != maxWebSessions {
		t.Fatalf("session count got %d want %d", got, maxWebSessions)
	}
	if _, ok := server.tokens["token-0"]; ok {
		t.Fatal("oldest session was not evicted")
	}
	if _, ok := server.tokens["new-token"]; !ok {
		t.Fatal("new session was not retained")
	}
}
