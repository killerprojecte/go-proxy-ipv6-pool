package main

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

func (a *App) fixedPortInfosLocked() []fixedPortInfo {
	infos := make([]fixedPortInfo, 0, len(a.cfg.Fixed.HTTPPorts)+len(a.cfg.Fixed.Socks5Ports))
	for _, port := range a.cfg.Fixed.HTTPPorts {
		ip, _ := a.fixedStore.Get(port)
		infos = append(infos, fixedPortInfo{Port: port, Type: proxyTypeHTTP, IP: ip, Running: a.runningPort[port] == proxyTypeHTTP})
	}
	for _, port := range a.cfg.Fixed.Socks5Ports {
		ip, _ := a.fixedStore.Get(port)
		infos = append(infos, fixedPortInfo{Port: port, Type: proxyTypeSocks5, IP: ip, Running: a.runningPort[port] == proxyTypeSocks5})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Port < infos[j].Port
	})
	return infos
}

func (a *App) AddFixedPorts(payload addFixedPortsRequest) ([]fixedPortInfo, error) {
	specs, err := expandFixedPortSpecs(payload)
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no fixed ports provided")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cfg := cloneConfig(a.cfg)
	state := cloneState(a.state)
	used := usedFixedIPs(state, cfg.CIDR)
	added := make([]fixedPortInfo, 0, len(specs))

	for _, spec := range specs {
		if err := a.validateNewFixedPortLocked(cfg, spec.Port); err != nil {
			return nil, err
		}
		if err := ensurePortAvailable(spec.Port); err != nil {
			return nil, err
		}

		ip, err := generateUniqueIPv6(cfg.CIDR, used)
		if err != nil {
			return nil, err
		}
		used[ip] = true
		state.FixedPorts[stateKey(spec.Port)] = ip

		switch spec.Type {
		case proxyTypeHTTP:
			cfg.Fixed.HTTPPorts = appendUniquePort(cfg.Fixed.HTTPPorts, spec.Port)
		case proxyTypeSocks5:
			cfg.Fixed.Socks5Ports = appendUniquePort(cfg.Fixed.Socks5Ports, spec.Port)
		default:
			return nil, fmt.Errorf("unsupported type %q", spec.Type)
		}
		added = append(added, fixedPortInfo{Port: spec.Port, Type: spec.Type, IP: ip, Running: false})
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := SaveConfig(a.configPath, cfg); err != nil {
		return nil, err
	}
	if err := state.Save(cfg.StateFilePath); err != nil {
		return nil, err
	}

	a.cfg = cfg
	a.state = state
	for i := range added {
		a.fixedStore.Set(added[i].Port, added[i].IP)
		if err := a.startFixedPortLocked(added[i].Port, added[i].Type); err != nil {
			return added, err
		}
		added[i].Running = true
	}
	return added, nil
}

func (a *App) ResetFixedPortIP(port int) (*resetIPResponse, error) {
	if err := validatePort(port, "port"); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	proxyType, ok := a.fixedPortTypeLocked(port)
	if !ok {
		return nil, fmt.Errorf("fixed port %d does not exist", port)
	}
	oldIP := a.state.FixedPorts[stateKey(port)]
	used := usedFixedIPs(a.state, a.cfg.CIDR)
	delete(used, oldIP)

	newIP, err := generateUniqueIPv6(a.cfg.CIDR, used)
	if err != nil {
		return nil, err
	}

	a.state.FixedPorts[stateKey(port)] = newIP
	if err := a.saveStateLocked(); err != nil {
		a.state.FixedPorts[stateKey(port)] = oldIP
		return nil, err
	}
	a.fixedStore.Set(port, newIP)

	return &resetIPResponse{
		Port:  port,
		Type:  proxyType,
		OldIP: oldIP,
		NewIP: newIP,
	}, nil
}

func (a *App) AddWhitelistEntries(entries []string) ([]string, error) {
	normalized, err := normalizeWhitelistEntries(entries)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("no whitelist entries provided")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cfg := cloneConfig(a.cfg)
	seen := make(map[string]bool)
	for _, entry := range cfg.Whitelist {
		seen[entry] = true
	}
	for _, entry := range normalized {
		if !seen[entry] {
			cfg.Whitelist = append(cfg.Whitelist, entry)
			seen[entry] = true
		}
	}
	sort.Strings(cfg.Whitelist)

	whitelist, err := ParseWhitelist(cfg.Whitelist)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := SaveConfig(a.configPath, cfg); err != nil {
		return nil, err
	}

	a.cfg = cfg
	a.auth.SetWhitelist(whitelist)
	return append([]string(nil), a.cfg.Whitelist...), nil
}

func (a *App) DeleteWhitelistEntries(entries []string) ([]string, error) {
	normalized, err := normalizeWhitelistEntries(entries)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return nil, fmt.Errorf("no whitelist entries provided")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cfg := cloneConfig(a.cfg)
	for _, entry := range normalized {
		cfg.Whitelist = removeString(cfg.Whitelist, entry)
	}

	whitelist, err := ParseWhitelist(cfg.Whitelist)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := SaveConfig(a.configPath, cfg); err != nil {
		return nil, err
	}

	a.cfg = cfg
	a.auth.SetWhitelist(whitelist)
	return append([]string(nil), a.cfg.Whitelist...), nil
}

func expandFixedPortSpecs(payload addFixedPortsRequest) ([]fixedPortSpec, error) {
	specs := make([]fixedPortSpec, 0, len(payload.Ports))
	seen := make(map[int]bool)

	addSpec := func(port int, proxyType string) error {
		if err := validatePort(port, "port"); err != nil {
			return err
		}
		normalizedType, err := normalizeProxyType(proxyType)
		if err != nil {
			return err
		}
		if seen[port] {
			return fmt.Errorf("port %d is duplicated in request", port)
		}
		seen[port] = true
		specs = append(specs, fixedPortSpec{Port: port, Type: normalizedType})
		return nil
	}

	for _, spec := range payload.Ports {
		if err := addSpec(spec.Port, spec.Type); err != nil {
			return nil, err
		}
	}
	for _, portRange := range payload.Ranges {
		if portRange.Start > portRange.End {
			return nil, fmt.Errorf("range start must be <= end")
		}
		for port := portRange.Start; port <= portRange.End; port++ {
			if err := addSpec(port, portRange.Type); err != nil {
				return nil, err
			}
		}
	}
	return specs, nil
}

func (a *App) validateNewFixedPortLocked(cfg *Config, port int) error {
	if port == cfg.Dynamic.HTTPPort {
		return fmt.Errorf("port %d conflicts with dynamic http port", port)
	}
	if port == cfg.Dynamic.Socks5Port {
		return fmt.Errorf("port %d conflicts with dynamic socks5 port", port)
	}
	if port == cfg.Sticky.HTTPPort {
		return fmt.Errorf("port %d conflicts with sticky http port", port)
	}
	if port == cfg.Sticky.Socks5Port {
		return fmt.Errorf("port %d conflicts with sticky socks5 port", port)
	}
	if portExists(cfg.Fixed.HTTPPorts, port) || portExists(cfg.Fixed.Socks5Ports, port) {
		return fmt.Errorf("fixed port %d already exists", port)
	}
	if _, running := a.runningPort[port]; running {
		return fmt.Errorf("port %d is already running", port)
	}
	return nil
}

func (a *App) fixedPortTypeLocked(port int) (string, bool) {
	if portExists(a.cfg.Fixed.HTTPPorts, port) {
		return proxyTypeHTTP, true
	}
	if portExists(a.cfg.Fixed.Socks5Ports, port) {
		return proxyTypeSocks5, true
	}
	return "", false
}

func ensurePortAvailable(port int) error {
	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return fmt.Errorf("port %d is not available: %w", port, err)
	}
	return listener.Close()
}

func usedFixedIPs(state *State, cidr string) map[string]bool {
	used := make(map[string]bool)
	if state == nil {
		return used
	}
	for _, ip := range state.FixedPorts {
		if ip != "" && cidrContainsIP(cidr, ip) {
			used[ip] = true
		}
	}
	return used
}

func normalizeWhitelistEntries(entries []string) ([]string, error) {
	normalized := make([]string, 0, len(entries))
	for _, entry := range entries {
		value := strings.TrimSpace(entry)
		if value == "" {
			continue
		}
		if _, err := ParseWhitelist([]string{value}); err != nil {
			return nil, err
		}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func cloneConfig(cfg *Config) *Config {
	next := *cfg
	next.Whitelist = append([]string(nil), cfg.Whitelist...)
	next.Fixed.HTTPPorts = append([]int(nil), cfg.Fixed.HTTPPorts...)
	next.Fixed.Socks5Ports = append([]int(nil), cfg.Fixed.Socks5Ports...)
	return &next
}

func cloneState(state *State) *State {
	next := &State{FixedPorts: make(map[string]string)}
	if state == nil {
		return next
	}
	for port, ip := range state.FixedPorts {
		next.FixedPorts[port] = ip
	}
	return next
}
