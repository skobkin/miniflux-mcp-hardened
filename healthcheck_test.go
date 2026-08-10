package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLocalHealthcheckURL(t *testing.T) {
	tests := map[string]string{
		":8080":          "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:8080":      "http://127.0.0.1:8080/healthz",
		"127.0.0.1:7000": "http://127.0.0.1:7000/healthz",
	}
	for address, expected := range tests {
		actual, err := localHealthcheckURL(address)
		if err != nil {
			t.Errorf("localHealthcheckURL(%q): %v", address, err)
		} else if actual != expected {
			t.Errorf("localHealthcheckURL(%q) = %q, want %q", address, actual, expected)
		}
	}
	if _, err := localHealthcheckURL("8080"); err == nil {
		t.Fatal("localHealthcheckURL accepted address without port separator")
	}
}

func TestRunHealthcheckStreamableHTTP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(healthcheckHTTP),
		ReadHeaderTimeout: time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})
	t.Setenv("MCP_TRANSPORT", transportStreamableHTTP)
	t.Setenv("MCP_HTTP_ADDR", listener.Addr().String())
	t.Setenv("MCP_AUTH_TOKEN", "token")
	t.Setenv("MCP_ALLOWED_ORIGINS", "")

	if err := runHealthcheck(context.Background()); err != nil {
		t.Fatalf("runHealthcheck: %v", err)
	}
}

func TestRunHealthcheckStdio(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthcheck" {
			t.Errorf("path = %s, want /healthcheck", r.URL.Path)
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer apiServer.Close()
	t.Setenv("MCP_TRANSPORT", transportStdio)
	t.Setenv("MINIFLUX_URL", apiServer.URL)
	t.Setenv("MINIFLUX_API_KEY", "api-key")
	t.Setenv("MINIFLUX_PROXY_URL", "")

	if err := runHealthcheck(context.Background()); err != nil {
		t.Fatalf("runHealthcheck: %v", err)
	}
}

func TestRunHealthcheckStdioHidesBackendFailure(t *testing.T) {
	const secret = "SENTINEL-HEALTH-SECRET"
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, secret, http.StatusInternalServerError)
	}))
	defer apiServer.Close()
	t.Setenv("MCP_TRANSPORT", transportStdio)
	t.Setenv("MINIFLUX_URL", apiServer.URL)
	t.Setenv("MINIFLUX_API_KEY", "api-key")
	t.Setenv("MINIFLUX_PROXY_URL", "")

	err := runHealthcheck(context.Background())
	if err == nil {
		t.Fatal("runHealthcheck succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("healthcheck error leaked backend response: %v", err)
	}
}

func TestProbeHTTPHealthUsesCallerDeadline(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer apiServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := probeHTTPHealth(ctx, apiServer.URL); err == nil {
		t.Fatal("probeHTTPHealth succeeded after deadline")
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	logger := testDiscardLogger()
	err := run(context.Background(), []string{"unknown"}, logger)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("run error = %v", err)
	}
}
