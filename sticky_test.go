package main

import (
	"encoding/base64"
	"net/http"
	"testing"
	"time"
)

func TestParseStickyUsername(t *testing.T) {
	identity, ok := parseStickyUsername("proxy_user", "proxy_user-120")
	if !ok {
		t.Fatal("parseStickyUsername rejected a valid duration")
	}
	if identity.Key != "user:proxy_user-120" {
		t.Fatalf("identity key = %q", identity.Key)
	}
	if identity.Duration != 120*time.Second {
		t.Fatalf("identity duration = %s, want 120s", identity.Duration)
	}

	if _, ok := parseStickyUsername("proxy_user", "proxy_user-0"); ok {
		t.Fatal("parseStickyUsername accepted zero duration")
	}
	if _, ok := parseStickyUsername("proxy_user", "other-120"); ok {
		t.Fatal("parseStickyUsername accepted an unrelated username")
	}
}

func TestStickySelectorReusesAndExpiresSession(t *testing.T) {
	manager := NewStickyManager("2001:db8::/64")
	now := time.Unix(100, 0)
	manager.now = func() time.Time { return now }
	selector := manager.Selector("user:proxy_user-2", 2*time.Second)

	first, err := selector()
	if err != nil {
		t.Fatalf("first selection failed: %v", err)
	}
	second, err := selector()
	if err != nil {
		t.Fatalf("second selection failed: %v", err)
	}
	if first != second {
		t.Fatalf("sticky selector changed before expiry: %s != %s", first, second)
	}

	now = now.Add(3 * time.Second)
	if _, err := selector(); err != nil {
		t.Fatalf("expired selection failed: %v", err)
	}
	if manager.sessions["user:proxy_user-2"].expiresAt != now.Add(2*time.Second) {
		t.Fatal("sticky selector did not renew the session after expiry")
	}
}

func TestHTTPIdentityAcceptsStickyDuration(t *testing.T) {
	auth, err := NewProxyAuth(AuthConfig{Username: "proxy_user", Password: "pass"}, nil)
	if err != nil {
		t.Fatalf("NewProxyAuth returned error: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatalf("http.NewRequest returned error: %v", err)
	}
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("proxy_user-500:pass")))

	identity, ok := auth.HTTPIdentity(req)
	if !ok {
		t.Fatal("HTTPIdentity rejected a valid sticky username")
	}
	if identity.Duration != 500*time.Second {
		t.Fatalf("identity duration = %s, want 500s", identity.Duration)
	}
}
