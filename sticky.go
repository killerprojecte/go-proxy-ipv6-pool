package main

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"
)

type StickyIdentity struct {
	Key      string
	Duration time.Duration
}

type stickySelectorContextKey struct{}

type stickySession struct {
	ip        string
	expiresAt time.Time
}

type StickyManager struct {
	cidr     string
	now      func() time.Time
	mu       sync.Mutex
	sessions map[string]stickySession
}

func NewStickyManager(cidr string) *StickyManager {
	return &StickyManager{
		cidr:     cidr,
		now:      time.Now,
		sessions: make(map[string]stickySession),
	}
}

func (m *StickyManager) Selector(key string, duration time.Duration) OutboundSelector {
	return func() (string, error) {
		if duration <= 0 {
			return generateRandomIPv6(m.cidr)
		}

		now := m.now()
		m.mu.Lock()
		defer m.mu.Unlock()

		if session, ok := m.sessions[key]; ok && now.Before(session.expiresAt) {
			return session.ip, nil
		}

		ip, err := generateRandomIPv6(m.cidr)
		if err != nil {
			return "", err
		}
		m.sessions[key] = stickySession{ip: ip, expiresAt: now.Add(duration)}
		return ip, nil
	}
}

func withOutboundSelector(ctx context.Context, selector OutboundSelector) context.Context {
	return context.WithValue(ctx, stickySelectorContextKey{}, selector)
}

func outboundSelectorFromContext(ctx context.Context, fallback OutboundSelector) OutboundSelector {
	if selector, ok := ctx.Value(stickySelectorContextKey{}).(OutboundSelector); ok && selector != nil {
		return selector
	}
	return fallback
}

func parseStickyUsername(configured, supplied string) (StickyIdentity, bool) {
	if configured == "" || supplied == configured {
		if supplied == configured && configured != "" {
			return StickyIdentity{Key: "user:" + supplied}, true
		}
		return StickyIdentity{}, false
	}

	prefix := configured + "-"
	if !strings.HasPrefix(supplied, prefix) {
		return StickyIdentity{}, false
	}
	seconds, err := strconv.ParseInt(strings.TrimPrefix(supplied, prefix), 10, 64)
	if err != nil || seconds <= 0 || seconds > int64((time.Duration(1<<63-1))/time.Second) {
		return StickyIdentity{}, false
	}
	return StickyIdentity{
		Key:      "user:" + supplied,
		Duration: time.Duration(seconds) * time.Second,
	}, true
}

func clientStickyKey(remoteAddr string) string {
	ip := clientIPFromRemoteAddr(remoteAddr)
	if ip == nil {
		return "client:" + remoteAddr
	}
	return "client:" + ip.String()
}
