package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/moodiness/rivune/clients/tv-installer/internal/install"
	"github.com/moodiness/rivune/clients/tv-installer/internal/release"
)

//go:embed static/*
var content embed.FS

type Server struct {
	service *install.Service
	version string
	token   string
	host    string
	origin  string
	close   func()
	mu      sync.Mutex
	running bool
}

type status struct {
	Version    string              `json:"version"`
	LocalIPs   []string            `json:"localIps"`
	Devices    []map[string]string `json:"webosDevices"`
	Tools      install.ToolStatus  `json:"tools"`
	Repository string              `json:"repository"`
}

type operationResult struct {
	OK    bool     `json:"ok"`
	Logs  []string `json:"logs"`
	Error string   `json:"error,omitempty"`
}

func New(service *install.Service, version, token, address string, close func()) http.Handler {
	return &Server{service: service, version: version, token: token, host: address, origin: "http://" + address, close: close}
}

func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'self'")
	if request.Host != server.host {
		http.Error(writer, "invalid host", http.StatusBadRequest)
		return
	}
	prefix := "/" + server.token + "/"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && !server.validMutation(request) {
		http.Error(writer, "forbidden", http.StatusForbidden)
		return
	}
	relative := strings.TrimPrefix(request.URL.Path, prefix)
	switch relative {
	case "api/status":
		server.status(writer, request)
	case "api/release":
		server.release(writer, request)
	case "api/run":
		server.run(writer, request)
	case "api/close":
		server.closeCompanion(writer, request)
	default:
		server.static(writer, request, relative)
	}
}

func (server *Server) validMutation(request *http.Request) bool {
	return request.Method == http.MethodPost && request.Header.Get("Origin") == server.origin && request.Header.Get("X-Rivune-Token") == server.token && request.Header.Get("Sec-Fetch-Site") != "cross-site"
}

func (server *Server) status(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	respond(writer, status{Version: server.version, LocalIPs: install.LocalIPv4Addresses(), Devices: install.ReadWebOSDevices(), Tools: server.service.Status(), Repository: release.Repository})
}

func (server *Server) release(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	value, err := server.service.Source.Latest(request.Context())
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	respond(writer, value)
}

func (server *Server) run(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	server.mu.Lock()
	if server.running {
		server.mu.Unlock()
		http.Error(writer, "another operation is running", http.StatusConflict)
		return
	}
	server.running = true
	server.mu.Unlock()
	defer func() { server.mu.Lock(); server.running = false; server.mu.Unlock() }()
	request.Body = http.MaxBytesReader(writer, request.Body, 16*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var input install.Request
	if err := decoder.Decode(&input); err != nil {
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	input.Passphrase = strings.TrimSpace(input.Passphrase)
	var logs []string
	ctx, cancel := install.OperationContext(request.Context())
	defer cancel()
	err := server.service.Execute(ctx, input, func(message string) { logs = append(logs, message) })
	input.Passphrase = ""
	result := operationResult{OK: err == nil, Logs: logs}
	if err != nil {
		result.Error = err.Error()
	}
	respond(writer, result)
}

func (server *Server) closeCompanion(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	respond(writer, map[string]bool{"ok": true})
	go server.close()
}

func (server *Server) static(writer http.ResponseWriter, request *http.Request, relative string) {
	if request.Method != http.MethodGet {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if relative == "" {
		relative = "index.html"
	}
	if strings.Contains(relative, "..") {
		http.NotFound(writer, request)
		return
	}
	sub, _ := fs.Sub(content, "static")
	value, err := fs.ReadFile(sub, relative)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	switch {
	case strings.HasSuffix(relative, ".html"):
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(relative, ".css"):
		writer.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(relative, ".js"):
		writer.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	default:
		writer.Header().Set("Content-Type", "application/octet-stream")
	}
	_, _ = writer.Write(value)
}

func respond(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fmt.Fprintln(writer, "{}")
	}
}
