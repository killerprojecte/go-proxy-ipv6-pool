package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/elazarl/goproxy"
)

func newHTTPProxy(selector OutboundSelector, auth *ProxyAuth, verbose bool, name string, sticky *StickyManager, rotation time.Duration) http.Handler {
	proxy := goproxy.NewProxyHttpServer()
	proxy.Verbose = verbose
	proxy.NonproxyHandler = http.HandlerFunc(handleNonProxyRequest)
	proxy.Tr = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, outboundIP, finalNetwork, finalAddr, err := dialWithOutbound(ctx, network, addr, selector)
			if err != nil {
				log.Printf("[%s] dial network=%s addr=%s final_network=%s final_addr=%s via=%s error=%v", name, network, addr, finalNetwork, finalAddr, outboundIP, err)
				return nil, err
			}
			if verbose {
				log.Printf("[%s] dial network=%s addr=%s final_network=%s final_addr=%s via=%s", name, network, addr, finalNetwork, finalAddr, outboundIP)
			}
			return conn, nil
		},
		DisableKeepAlives: true,
	}
	proxy.ConnectDialWithReq = func(req *http.Request, network, addr string) (net.Conn, error) {
		conn, outboundIP, finalNetwork, finalAddr, err := dialWithOutbound(req.Context(), network, addr, selector)
		if err != nil {
			log.Printf("[%s] connect network=%s addr=%s final_network=%s final_addr=%s via=%s error=%v", name, network, addr, finalNetwork, finalAddr, outboundIP, err)
			return nil, err
		}
		if verbose {
			log.Printf("[%s] CONNECT network=%s addr=%s final_network=%s final_addr=%s via=%s", name, network, addr, finalNetwork, finalAddr, outboundIP)
		}
		return conn, nil
	}

	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if isNonProxyRequest(req) {
			proxy.ServeHTTP(w, req)
			return
		}
		identity, ok := auth.HTTPIdentity(req)
		if !ok {
			writeProxyAuthRequired(w)
			return
		}
		if sticky != nil {
			req = req.WithContext(withOutboundSelector(req.Context(), sticky.Selector(identity.Key, stickyDuration(identity, rotation))))
		}
		proxy.ServeHTTP(w, req)
	})
}

func isNonProxyRequest(req *http.Request) bool {
	return req.Method != http.MethodConnect && !req.URL.IsAbs()
}

func handleNonProxyRequest(w http.ResponseWriter, req *http.Request) {
	clientIP := clientIPFromRemoteAddr(req.RemoteAddr)
	ipText := ""
	if clientIP != nil {
		ipText = clientIP.String()
	}

	switch req.URL.Path {
	case "/ip":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ipText + "\n"))
	case "/whoami":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"ip":          ipText,
			"remote_addr": req.RemoteAddr,
		})
	default:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("This is a proxy server. Use /ip to view your client IP.\n"))
	}
}
