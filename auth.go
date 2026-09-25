package main

import (
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ProxyAuth struct {
	mu        sync.RWMutex
	username  string
	password  string
	enabled   bool
	whitelist *IPWhitelist
}

func NewProxyAuth(cfg AuthConfig, whitelistEntries []string) (*ProxyAuth, error) {
	if err := validateAuthConfig(cfg); err != nil {
		return nil, err
	}
	whitelist, err := ParseWhitelist(whitelistEntries)
	if err != nil {
		return nil, err
	}
	return &ProxyAuth{
		username:  cfg.Username,
		password:  cfg.Password,
		enabled:   cfg.Username != "" || cfg.Password != "",
		whitelist: whitelist,
	}, nil
}

func (a *ProxyAuth) Enabled() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.enabled
}

func (a *ProxyAuth) Valid(username, password string) bool {
	if a == nil {
		return true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.enabled {
		return true
	}
	_, userOK := parseStickyUsername(a.username, username)
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	return userOK && passOK
}

func (a *ProxyAuth) AllowHTTPRequest(req *http.Request) bool {
	_, ok := a.HTTPIdentity(req)
	return ok
}

func (a *ProxyAuth) HTTPIdentity(req *http.Request) (StickyIdentity, bool) {
	identity := StickyIdentity{Key: clientStickyKey(req.RemoteAddr)}
	if a.ClientWhitelisted(req.RemoteAddr) {
		username, password, ok := parseProxyBasicAuth(req.Header.Get("Proxy-Authorization"))
		if ok {
			if parsed, valid := a.identity(username, password); valid {
				return parsed, true
			}
		}
		return identity, true
	}
	if !a.Enabled() {
		return identity, true
	}
	username, password, ok := parseProxyBasicAuth(req.Header.Get("Proxy-Authorization"))
	if !ok {
		return StickyIdentity{}, false
	}
	return a.identity(username, password)
}

func (a *ProxyAuth) IdentityForUsername(username string) (StickyIdentity, bool) {
	if a == nil {
		return StickyIdentity{Key: "user:" + username}, true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.enabled {
		return StickyIdentity{Key: "user:" + username}, true
	}
	return parseStickyUsername(a.username, username)
}

func (a *ProxyAuth) identity(username, password string) (StickyIdentity, bool) {
	if a == nil {
		return StickyIdentity{Key: "user:" + username}, true
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.enabled {
		return StickyIdentity{Key: "user:" + username}, true
	}
	identity, userOK := parseStickyUsername(a.username, username)
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	return identity, userOK && passOK
}

func stickyDuration(identity StickyIdentity, fallback time.Duration) time.Duration {
	if identity.Duration > 0 {
		return identity.Duration
	}
	return fallback
}

func (a *ProxyAuth) ClientWhitelisted(remoteAddr string) bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.whitelist.Contains(clientIPFromRemoteAddr(remoteAddr))
}

func (a *ProxyAuth) ClientWhitelistedAddr(addr net.Addr) bool {
	if addr == nil {
		return false
	}
	return a.ClientWhitelisted(addr.String())
}

func (a *ProxyAuth) WhitelistEnabled() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.whitelist.Empty()
}

func (a *ProxyAuth) SetWhitelist(whitelist *IPWhitelist) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.whitelist = whitelist
}

func parseProxyBasicAuth(header string) (string, string, bool) {
	const prefix = "Basic "
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", "", false
	}

	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(header[len(prefix):]))
	if err != nil {
		return "", "", false
	}
	username, password, ok := strings.Cut(string(decoded), ":")
	if !ok {
		return "", "", false
	}
	return username, password, true
}

func writeProxyAuthRequired(w http.ResponseWriter) {
	w.Header().Set("Proxy-Authenticate", `Basic realm="go-proxy-ipv6-pool"`)
	http.Error(w, "Proxy Authentication Required", http.StatusProxyAuthRequired)
}
