package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

const (
	transportStdio          = "stdio"
	transportStreamableHTTP = "streamable-http"
	defaultHTTPAddr         = ":8080"
	defaultHTTPPath         = "/mcp"
	httpReadTimeout         = 30 * time.Second
	httpReadHeaderTimeout   = 5 * time.Second
	httpIdleTimeout         = 2 * time.Minute
	httpMaxHeaderBytes      = 32 << 10
)

type transportConfig struct {
	Transport      string
	HTTPAddr       string
	HTTPPath       string
	AuthToken      string
	AllowedOrigins map[string]struct{}
}

func loadTransportConfig() (transportConfig, error) {
	cfg := transportConfig{
		Transport: envOrDefault("MCP_TRANSPORT", transportStdio),
		HTTPAddr:  envOrDefault("MCP_HTTP_ADDR", defaultHTTPAddr),
		HTTPPath:  envOrDefault("MCP_HTTP_PATH", defaultHTTPPath),
		AuthToken: os.Getenv("MCP_AUTH_TOKEN"),
	}

	switch cfg.Transport {
	case transportStdio:
		return cfg, nil
	case transportStreamableHTTP:
		if cfg.AuthToken == "" {
			return transportConfig{}, fmt.Errorf("MCP_AUTH_TOKEN is required when MCP_TRANSPORT=%s", transportStreamableHTTP)
		}
		if err := validateHTTPPath(cfg.HTTPPath); err != nil {
			return transportConfig{}, err
		}
		allowedOrigins, err := parseAllowedOrigins(os.Getenv("MCP_ALLOWED_ORIGINS"))
		if err != nil {
			return transportConfig{}, err
		}
		cfg.AllowedOrigins = allowedOrigins
		return cfg, nil
	default:
		return transportConfig{}, fmt.Errorf("unsupported MCP_TRANSPORT %q (supported: %s, %s)", cfg.Transport, transportStdio, transportStreamableHTTP)
	}
}

func validateHTTPPath(httpPath string) (err error) {
	if !strings.HasPrefix(httpPath, "/") || httpPath == "/" {
		return fmt.Errorf("MCP_HTTP_PATH must start with / and cannot be /")
	}
	if httpPath == "/healthz" {
		return fmt.Errorf("MCP_HTTP_PATH cannot be /healthz")
	}
	if strings.ContainsAny(httpPath, "{}") {
		return fmt.Errorf("MCP_HTTP_PATH must be a literal path without wildcards")
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("MCP_HTTP_PATH is not a valid HTTP path")
		}
	}()
	http.NewServeMux().Handle(httpPath, http.NotFoundHandler())
	return nil
}

func parseAllowedOrigins(value string) (map[string]struct{}, error) {
	allowed := make(map[string]struct{})
	if strings.TrimSpace(value) == "" {
		return allowed, nil
	}

	for _, configuredOrigin := range strings.Split(value, ",") {
		origin := strings.TrimSpace(configuredOrigin)
		if origin == "" {
			return nil, fmt.Errorf("MCP_ALLOWED_ORIGINS contains an empty origin")
		}
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			return nil, fmt.Errorf("MCP_ALLOWED_ORIGINS contains invalid origin %q; expected http(s)://host[:port]", origin)
		}
		allowed[normalized] = struct{}{}
	}
	return allowed, nil
}

func normalizeOrigin(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || strings.Contains(origin, "#") {
		return "", fmt.Errorf("invalid origin")
	}

	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", fmt.Errorf("invalid origin")
	}
	port := parsed.Port()
	if scheme == "http" && port == "80" || scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		hostname = net.JoinHostPort(hostname, port)
	} else if strings.Contains(hostname, ":") {
		hostname = "[" + hostname + "]"
	}
	return scheme + "://" + hostname, nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func serveMCP(mcpServer *server.MCPServer, cfg transportConfig) error {
	switch cfg.Transport {
	case transportStdio:
		return server.ServeStdio(mcpServer)
	case transportStreamableHTTP:
		return serveStreamableHTTP(mcpServer, cfg)
	default:
		return fmt.Errorf("unsupported MCP transport %q", cfg.Transport)
	}
}

func serveStreamableHTTP(mcpServer *server.MCPServer, cfg transportConfig) error {
	mcpHandler := server.NewStreamableHTTPServer(
		mcpServer,
		server.WithStateLess(true),
	)

	mux := http.NewServeMux()
	mux.Handle(cfg.HTTPPath, validateOrigin(cfg.AllowedOrigins, requireBearerToken(cfg.AuthToken, mcpHandler)))
	mux.HandleFunc("/healthz", healthcheckHTTP)

	httpServer := newHTTPServer(cfg.HTTPAddr, mux)
	return httpServer.ListenAndServe()
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadTimeout:       httpReadTimeout,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
		// Streamable HTTP may use long-lived SSE responses, so a server-wide
		// WriteTimeout would terminate valid streams.
	}
}

func healthcheckHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func validateOrigin(allowedOrigins map[string]struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHeaders := r.Header.Values("Origin")
		if len(originHeaders) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(originHeaders) != 1 {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		origin := originHeaders[0]
		normalized, err := normalizeOrigin(origin)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if _, ok := allowedOrigins[normalized]; !ok {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}

		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, Mcp-Protocol-Version, Mcp-Session-Id")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	expectedTokenHash := sha256.Sum256([]byte(token))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, providedToken, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
		scheme = strings.TrimSpace(scheme)
		providedToken = strings.TrimSpace(providedToken)
		providedTokenHash := sha256.Sum256([]byte(providedToken))
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(providedTokenHash[:], expectedTokenHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
