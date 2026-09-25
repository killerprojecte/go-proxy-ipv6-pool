package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type fixedPortInfo struct {
	Port    int    `json:"port"`
	Type    string `json:"type"`
	IP      string `json:"ip"`
	Running bool   `json:"running"`
}

type fixedPortSpec struct {
	Port int    `json:"port"`
	Type string `json:"type"`
}

type fixedPortRange struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Type  string `json:"type"`
}

type addFixedPortsRequest struct {
	Ports  []fixedPortSpec  `json:"ports"`
	Ranges []fixedPortRange `json:"ranges"`
}

type whitelistRequest struct {
	Entries []string `json:"entries"`
}

type resetIPResponse struct {
	Port  int    `json:"port"`
	Type  string `json:"type"`
	OldIP string `json:"old_ip"`
	NewIP string `json:"new_ip"`
}

func (a *App) startAdmin() error {
	listener, err := net.Listen("tcp", a.cfg.Admin.Listen)
	if err != nil {
		return fmt.Errorf("admin listen on %s: %w", a.cfg.Admin.Listen, err)
	}
	server := &http.Server{Handler: a.adminHandler()}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("admin server err: %v", err)
		}
	}()
	return nil
}

func (a *App) adminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", a.handleStatus)
	mux.HandleFunc("/api/fixed-ports", a.handleFixedPorts)
	mux.HandleFunc("/api/fixed-ports/", a.handleFixedPortAction)
	mux.HandleFunc("/api/whitelist/add", a.handleWhitelistAdd)
	mux.HandleFunc("/api/whitelist/delete", a.handleWhitelistDelete)
	return a.requireAdminAuth(mux)
}

func (a *App) requireAdminAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		a.mu.RLock()
		adminToken := a.cfg.Admin.Token
		a.mu.RUnlock()

		token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
		if token == "" || token != adminToken {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, req)
	})
}

func (a *App) handleStatus(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":           versionString(),
		"cidr":              a.cfg.CIDR,
		"config":            a.configPath,
		"state":             a.cfg.StateFilePath,
		"auth_enabled":      a.auth.Enabled(),
		"whitelist_enabled": a.auth.WhitelistEnabled(),
		"whitelist":         append([]string(nil), a.cfg.Whitelist...),
		"admin_listen":      a.cfg.Admin.Listen,
		"dynamic": map[string]int{
			"http_port":   a.cfg.Dynamic.HTTPPort,
			"socks5_port": a.cfg.Dynamic.Socks5Port,
		},
		"sticky": map[string]any{
			"http_port":        a.cfg.Sticky.HTTPPort,
			"socks5_port":      a.cfg.Sticky.Socks5Port,
			"rotation_seconds": a.cfg.Sticky.RotationSeconds,
		},
		"fixed_count": len(a.cfg.Fixed.AllPorts()),
		"fixed_ports": a.fixedPortInfosLocked(),
	})
}

func (a *App) handleFixedPorts(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		a.mu.RLock()
		defer a.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"fixed_ports": a.fixedPortInfosLocked(),
		})
	case http.MethodPost:
		var payload addFixedPortsRequest
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid json")
			return
		}
		added, err := a.AddFixedPorts(payload)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"added": added})
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleFixedPortAction(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rest := strings.TrimPrefix(req.URL.Path, "/api/fixed-ports/")
	portText, action, ok := strings.Cut(rest, "/")
	if !ok || action != "reset-ip" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	port, err := strconv.Atoi(portText)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid port")
		return
	}

	result, err := a.ResetFixedPortIP(port)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *App) handleWhitelistAdd(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload whitelistRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	entries, err := a.AddWhitelistEntries(payload.Entries)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"whitelist": entries})
}

func (a *App) handleWhitelistDelete(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload whitelistRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	entries, err := a.DeleteWhitelistEntries(payload.Entries)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"whitelist": entries})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
