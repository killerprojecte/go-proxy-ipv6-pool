package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	proxyTypeHTTP   = "http"
	proxyTypeSocks5 = "socks5"
)

type App struct {
	mu sync.RWMutex
	wg sync.WaitGroup

	cfg         *Config
	configPath  string
	state       *State
	auth        *ProxyAuth
	fixedStore  *FixedIPStore
	sticky      *StickyManager
	runningPort map[int]string
}

func NewApp(cfg *Config) (*App, error) {
	state, err := LoadState(cfg.StateFilePath)
	if err != nil {
		return nil, err
	}
	if err := state.EnsureFixedPortIPs(cfg.Fixed.AllPorts(), cfg.CIDR); err != nil {
		return nil, err
	}
	if len(cfg.Fixed.AllPorts()) > 0 {
		if err := state.Save(cfg.StateFilePath); err != nil {
			return nil, err
		}
	}

	auth, err := NewProxyAuth(cfg.Auth, cfg.Whitelist)
	if err != nil {
		return nil, err
	}

	return &App{
		cfg:         cfg,
		configPath:  cfg.ConfigSource,
		state:       state,
		auth:        auth,
		fixedStore:  NewFixedIPStore(state),
		sticky:      NewStickyManager(cfg.CIDR),
		runningPort: make(map[int]string),
	}, nil
}

func (a *App) Start() error {
	if err := a.startHTTP(a.cfg.Dynamic.HTTPPort, newRandomOutboundSelector(a.cfg.CIDR), "dynamic-http", nil, 0); err != nil {
		return err
	}
	if err := a.startSocks5(a.cfg.Dynamic.Socks5Port, newRandomOutboundSelector(a.cfg.CIDR), "dynamic-socks5", nil, 0); err != nil {
		return err
	}
	if a.cfg.Sticky.HTTPPort != 0 {
		if err := a.startHTTP(a.cfg.Sticky.HTTPPort, newRandomOutboundSelector(a.cfg.CIDR), "sticky-http", a.sticky, a.cfg.Sticky.RotationDuration()); err != nil {
			return err
		}
	}
	if a.cfg.Sticky.Socks5Port != 0 {
		if err := a.startSocks5(a.cfg.Sticky.Socks5Port, newRandomOutboundSelector(a.cfg.CIDR), "sticky-socks5", a.sticky, a.cfg.Sticky.RotationDuration()); err != nil {
			return err
		}
	}

	for _, port := range a.cfg.Fixed.HTTPPorts {
		if err := a.startFixedPortLocked(port, proxyTypeHTTP); err != nil {
			return err
		}
	}
	for _, port := range a.cfg.Fixed.Socks5Ports {
		if err := a.startFixedPortLocked(port, proxyTypeSocks5); err != nil {
			return err
		}
	}

	if a.cfg.Admin.Enabled {
		if err := a.startAdmin(); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) Wait() {
	a.wg.Wait()
}

func (a *App) logStartup() {
	log.Println("server running ...")
	log.Printf("build: %s", versionString())
	log.Printf("config: %s", a.cfg.ConfigSource)
	log.Printf("state: %s", a.cfg.StateFilePath)
	log.Printf("ipv6 cidr: [%s]", a.cfg.CIDR)
	log.Printf("auth enabled: %v", a.auth.Enabled())
	log.Printf("whitelist enabled: %v", a.auth.WhitelistEnabled())
	log.Printf("admin enabled: %v", a.cfg.Admin.Enabled)
	if a.cfg.Admin.Enabled {
		log.Printf("admin listen: %s", a.cfg.Admin.Listen)
	}
	log.Printf("dynamic http: 0.0.0.0:%d", a.cfg.Dynamic.HTTPPort)
	log.Printf("dynamic socks5: 0.0.0.0:%d", a.cfg.Dynamic.Socks5Port)
	if a.cfg.Sticky.HTTPPort != 0 || a.cfg.Sticky.Socks5Port != 0 {
		log.Printf("sticky rotation: %d seconds", a.cfg.Sticky.RotationSeconds)
	}
	if a.cfg.Sticky.HTTPPort != 0 {
		log.Printf("sticky http: 0.0.0.0:%d", a.cfg.Sticky.HTTPPort)
	}
	if a.cfg.Sticky.Socks5Port != 0 {
		log.Printf("sticky socks5: 0.0.0.0:%d", a.cfg.Sticky.Socks5Port)
	}
	for _, port := range a.cfg.Fixed.HTTPPorts {
		ip, _ := a.fixedStore.Get(port)
		log.Printf("fixed http: 0.0.0.0:%d -> %s", port, ip)
	}
	for _, port := range a.cfg.Fixed.Socks5Ports {
		ip, _ := a.fixedStore.Get(port)
		log.Printf("fixed socks5: 0.0.0.0:%d -> %s", port, ip)
	}
}

func (a *App) startHTTP(port int, selector OutboundSelector, name string, sticky *StickyManager, rotation time.Duration) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("%s listen on port %d: %w", name, port, err)
	}
	server := &http.Server{
		Handler: newHTTPProxy(selector, a.auth, a.cfg.Verbose, name, sticky, rotation),
	}

	a.runningPort[port] = proxyTypeHTTP
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("%s server err: %v", name, err)
		}
	}()
	return nil
}

func (a *App) startSocks5(port int, selector OutboundSelector, name string, sticky *StickyManager, rotation time.Duration) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("%s listen on port %d: %w", name, port, err)
	}
	server, err := newSocks5Proxy(selector, a.auth, name, sticky, rotation)
	if err != nil {
		_ = listener.Close()
		return fmt.Errorf("%s init: %w", name, err)
	}

	a.runningPort[port] = proxyTypeSocks5
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := server.Serve(listener); err != nil {
			log.Printf("%s server err: %v", name, err)
		}
	}()
	return nil
}

func (a *App) startFixedPortLocked(port int, proxyType string) error {
	selector := a.fixedStore.Selector(port)
	switch proxyType {
	case proxyTypeHTTP:
		return a.startHTTP(port, selector, fmt.Sprintf("fixed-http-%d", port), nil, 0)
	case proxyTypeSocks5:
		return a.startSocks5(port, selector, fmt.Sprintf("fixed-socks5-%d", port), nil, 0)
	default:
		return fmt.Errorf("unsupported fixed proxy type %q", proxyType)
	}
}

func (a *App) saveConfigLocked() error {
	return SaveConfig(a.configPath, a.cfg)
}

func (a *App) saveStateLocked() error {
	return a.state.Save(a.cfg.StateFilePath)
}

func normalizeProxyType(proxyType string) (string, error) {
	switch proxyType {
	case proxyTypeHTTP:
		return proxyTypeHTTP, nil
	case proxyTypeSocks5, "socks":
		return proxyTypeSocks5, nil
	default:
		return "", fmt.Errorf("type must be %q or %q", proxyTypeHTTP, proxyTypeSocks5)
	}
}

func portExists(ports []int, port int) bool {
	for _, existing := range ports {
		if existing == port {
			return true
		}
	}
	return false
}

func appendUniquePort(ports []int, port int) []int {
	if portExists(ports, port) {
		return ports
	}
	ports = append(ports, port)
	sort.Ints(ports)
	return ports
}

func removeString(values []string, target string) []string {
	next := values[:0]
	for _, value := range values {
		if value != target {
			next = append(next, value)
		}
	}
	return next
}

func stateKey(port int) string {
	return strconv.Itoa(port)
}
