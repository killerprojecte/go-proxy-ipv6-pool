package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	socks5 "github.com/armon/go-socks5"
	xcontext "golang.org/x/net/context"
)

type Socks5Proxy struct {
	name           string
	auth           *ProxyAuth
	noAuthServer   *socks5.Server
	userPassServer *socks5.Server
}

func newSocks5Proxy(selector OutboundSelector, auth *ProxyAuth, name string, sticky *StickyManager, rotation time.Duration) (*Socks5Proxy, error) {
	noAuthServer, err := socks5.New(newSocks5Config(selector, auth, true, name, sticky, rotation))
	if err != nil {
		return nil, err
	}

	var userPassServer *socks5.Server
	if auth.Enabled() {
		userPassServer, err = socks5.New(newSocks5Config(selector, auth, false, name, sticky, rotation))
		if err != nil {
			return nil, err
		}
	}

	return &Socks5Proxy{
		name:           name,
		auth:           auth,
		noAuthServer:   noAuthServer,
		userPassServer: userPassServer,
	}, nil
}

func newSocks5Config(selector OutboundSelector, auth *ProxyAuth, noAuth bool, name string, sticky *StickyManager, rotation time.Duration) *socks5.Config {
	conf := &socks5.Config{
		Resolver: ipv6Resolver{name: name},
		Rewriter: ipv6Rewriter{name: name, sticky: sticky, rotation: rotation, auth: auth},
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, outboundIP, finalNetwork, finalAddr, err := dialWithOutbound(ctx, network, addr, selector)
			if err != nil {
				log.Printf("[%s] dial network=%s addr=%s final_network=%s final_addr=%s via=%s error=%v", name, network, addr, finalNetwork, finalAddr, outboundIP, err)
				return nil, err
			}
			log.Printf("[%s] dial network=%s addr=%s final_network=%s final_addr=%s via=%s", name, network, addr, finalNetwork, finalAddr, outboundIP)
			return conn, nil
		},
	}

	if noAuth {
		conf.AuthMethods = []socks5.Authenticator{&socks5.NoAuthAuthenticator{}}
		if auth != nil && auth.Enabled() {
			conf.AuthMethods = append(conf.AuthMethods, &socks5.UserPassAuthenticator{
				Credentials: auth,
			})
		}
	} else if auth != nil && auth.Enabled() {
		conf.AuthMethods = []socks5.Authenticator{
			&socks5.UserPassAuthenticator{
				Credentials: auth,
			},
		}
	}

	return conf
}

func (p *Socks5Proxy) ListenAndServe(network, addr string) error {
	listener, err := net.Listen(network, addr)
	if err != nil {
		return err
	}
	return p.Serve(listener)
}

func (p *Socks5Proxy) Serve(listener net.Listener) error {
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		go p.serveConn(conn)
	}
}

func (p *Socks5Proxy) serveConn(conn net.Conn) {
	if !p.auth.Enabled() || p.auth.ClientWhitelistedAddr(conn.RemoteAddr()) {
		if p.auth.Enabled() {
			log.Printf("[%s] whitelist no-auth client=%s", p.name, conn.RemoteAddr())
		}
		if err := p.noAuthServer.ServeConn(conn); err != nil {
			log.Printf("[%s] no-auth client=%s error=%v", p.name, conn.RemoteAddr(), err)
		}
		return
	}

	if p.userPassServer == nil {
		_ = conn.Close()
		return
	}
	if err := p.userPassServer.ServeConn(conn); err != nil {
		log.Printf("[%s] user-pass client=%s error=%v", p.name, conn.RemoteAddr(), err)
	}
}

type ipv6Resolver struct {
	name string
}

func (r ipv6Resolver) Resolve(ctx xcontext.Context, name string) (xcontext.Context, net.IP, error) {
	ip, err := lookupIPv6(ctx, name)
	if err != nil {
		log.Printf("[%s] resolve domain=%s error=%v", r.name, name, err)
		return ctx, nil, err
	}

	log.Printf("[%s] resolve domain=%s ipv6=%s", r.name, name, ip)
	return ctx, ip, nil
}

type ipv6Rewriter struct {
	name     string
	sticky   *StickyManager
	rotation time.Duration
	auth     *ProxyAuth
}

func (r ipv6Rewriter) Rewrite(ctx xcontext.Context, request *socks5.Request) (xcontext.Context, *socks5.AddrSpec) {
	if r.sticky != nil {
		identity := StickyIdentity{Key: "socks-client"}
		if request != nil && request.AuthContext != nil {
			if username := request.AuthContext.Payload["Username"]; username != "" && r.auth != nil {
				if parsed, ok := r.auth.IdentityForUsername(username); ok {
					identity = parsed
				}
			}
		}
		if request != nil && request.RemoteAddr != nil && request.RemoteAddr.IP != nil && identity.Key == "socks-client" {
			identity.Key = "client:" + request.RemoteAddr.IP.String()
		}
		ctx = withOutboundSelector(ctx, r.sticky.Selector(identity.Key, stickyDuration(identity, r.rotation)))
	}
	dest := request.DestAddr
	if dest == nil || dest.FQDN == "" {
		if dest != nil && dest.IP != nil && dest.IP.To4() != nil {
			log.Printf("[%s] reject ipv4 target=%s", r.name, dest.Address())
		}
		return ctx, dest
	}

	ip, err := lookupIPv6(ctx, dest.FQDN)
	if err != nil {
		log.Printf("[%s] rewrite domain=%s error=%v", r.name, dest.FQDN, err)
		return ctx, dest
	}

	rewritten := &socks5.AddrSpec{
		FQDN: dest.FQDN,
		IP:   ip,
		Port: dest.Port,
	}
	log.Printf("[%s] rewrite domain=%s ipv6=%s port=%d", r.name, dest.FQDN, ip, dest.Port)
	return ctx, rewritten
}

func lookupIPv6(ctx xcontext.Context, name string) (net.IP, error) {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip6", name)
	if err != nil {
		return nil, err
	}

	for _, ip := range ips {
		if ip.To4() == nil && ip.To16() != nil {
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no ipv6 address found for %s", name)
}
