package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"strings"
)

type OutboundSelector func() (string, error)

func newRandomOutboundSelector(cidr string) OutboundSelector {
	return func() (string, error) {
		return generateRandomIPv6(cidr)
	}
}

func newFixedOutboundSelector(ip string) OutboundSelector {
	return func() (string, error) {
		return ip, nil
	}
}

func dialWithOutbound(ctx context.Context, network, addr string, selector OutboundSelector) (net.Conn, string, string, string, error) {
	selector = outboundSelectorFromContext(ctx, selector)
	outboundIP, err := selector()
	if err != nil {
		return nil, "", "", "", err
	}

	localAddr, err := net.ResolveTCPAddr("tcp6", "["+outboundIP+"]:0")
	if err != nil {
		return nil, outboundIP, "", "", err
	}

	finalNetwork := forceTCP6Network(network)
	finalAddr, err := forceTCP6Address(ctx, addr)
	if err != nil {
		return nil, outboundIP, finalNetwork, addr, err
	}

	dialer := net.Dialer{LocalAddr: localAddr}
	conn, err := dialer.DialContext(ctx, finalNetwork, finalAddr)
	return conn, outboundIP, finalNetwork, finalAddr, err
}

func forceTCP6Network(network string) string {
	if strings.HasPrefix(network, "tcp") {
		return "tcp6"
	}
	return network
}

func forceTCP6Address(ctx context.Context, addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}

	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() != nil {
			return "", fmt.Errorf("ipv4 target blocked in ipv6-only dialer: %s", addr)
		}
		return net.JoinHostPort(ip.String(), port), nil
	}

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip6", host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() == nil && ip.To16() != nil {
			return net.JoinHostPort(ip.String(), port), nil
		}
	}
	return "", fmt.Errorf("no ipv6 address found for %s", host)
}

func generateRandomIPv6(cidr string) (string, error) {
	networkIP, ipv6Net, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", err
	}

	base := networkIP.To16()
	if base == nil || networkIP.To4() != nil {
		return "", fmt.Errorf("%s is not an ipv6 cidr", cidr)
	}

	mask := ipv6Net.Mask
	if len(mask) != net.IPv6len {
		return "", fmt.Errorf("%s is not an ipv6 cidr", cidr)
	}

	randomIP := make([]byte, net.IPv6len)
	if _, err := rand.Read(randomIP); err != nil {
		return "", err
	}

	out := make([]byte, net.IPv6len)
	for i := 0; i < net.IPv6len; i++ {
		out[i] = (base[i] & mask[i]) | (randomIP[i] & ^mask[i])
	}
	return net.IP(out).String(), nil
}

func cidrContainsIP(cidr string, ipText string) bool {
	ip := net.ParseIP(ipText)
	if ip == nil {
		return false
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return ipNet.Contains(ip)
}
