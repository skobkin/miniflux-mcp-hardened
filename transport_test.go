package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  map[string]struct{}
		wantError bool
	}{
		{name: "empty", value: "", expected: map[string]struct{}{}},
		{name: "multiple", value: "HTTPS://ONE.EXAMPLE:0443, http://LOCALHOST:03000", expected: map[string]struct{}{
			"https://one.example":   {},
			"http://localhost:3000": {},
		}},
		{name: "duplicate", value: "https://one.example,https://one.example", expected: map[string]struct{}{"https://one.example": {}}},
		{name: "empty element", value: "https://one.example,,https://two.example", wantError: true},
		{name: "relative", value: "/local", wantError: true},
		{name: "wrong scheme", value: "file://example.com", wantError: true},
		{name: "userinfo", value: "https://user@example.com", wantError: true},
		{name: "path", value: "https://example.com/path", wantError: true},
		{name: "query", value: "https://example.com?token=value", wantError: true},
		{name: "empty query", value: "https://example.com?", wantError: true},
		{name: "empty fragment", value: "https://example.com#", wantError: true},
		{name: "out of range port", value: "https://example.com:65536", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := parseAllowedOrigins(test.value)
			if test.wantError {
				if err == nil {
					t.Fatalf("parseAllowedOrigins(%q) succeeded, want error", test.value)
				}

				return
			}
			if err != nil {
				t.Fatalf("parseAllowedOrigins(%q): %v", test.value, err)
			}
			if !reflect.DeepEqual(actual, test.expected) {
				t.Fatalf("parseAllowedOrigins(%q) = %v, want %v", test.value, actual, test.expected)
			}
		})
	}
}

func TestLoadTransportConfigValidatesOriginsInHTTPMode(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", transportStreamableHTTP)
	t.Setenv("MCP_AUTH_TOKEN", "secret")
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://client.example")

	cfg, err := loadTransportConfig()
	if err != nil {
		t.Fatalf("loadTransportConfig: %v", err)
	}
	if _, ok := cfg.AllowedOrigins["https://client.example"]; !ok {
		t.Fatalf("allowed origins = %v, want configured origin", cfg.AllowedOrigins)
	}

	t.Setenv("MCP_ALLOWED_ORIGINS", "https://client.example/path")
	if _, err := loadTransportConfig(); err == nil {
		t.Fatal("loadTransportConfig accepted origin with path")
	}
}

func TestLoadTransportConfigRejectsInvalidHTTPPaths(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", transportStreamableHTTP)
	t.Setenv("MCP_AUTH_TOKEN", "secret")
	t.Setenv("MCP_ALLOWED_ORIGINS", "")

	for _, httpPath := range []string{"/mcp/{", "/mcp/{session}"} {
		t.Run(httpPath, func(t *testing.T) {
			t.Setenv("MCP_HTTP_PATH", httpPath)
			if _, err := loadTransportConfig(); err == nil {
				t.Fatalf("loadTransportConfig accepted invalid path %q", httpPath)
			}
		})
	}
}

func TestLoadTransportConfigRejectsMalformedAuthTokens(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", transportStreamableHTTP)
	t.Setenv("MCP_ALLOWED_ORIGINS", "")

	for _, token := range []string{" secret", "secret ", "secret\n", "sec\rret"} {
		t.Run(token, func(t *testing.T) {
			t.Setenv("MCP_AUTH_TOKEN", token)
			if _, err := loadTransportConfig(); err == nil {
				t.Fatalf("loadTransportConfig accepted malformed auth token %q", token)
			}
		})
	}
}

func TestHTTPOriginAndBearerProtection(t *testing.T) {
	const (
		token         = "correct-token"
		allowedOrigin = "https://client.example"
	)
	handler := validateOrigin(
		map[string]struct{}{allowedOrigin: {}},
		requireBearerToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})),
	)

	tests := []struct {
		name              string
		method            string
		originHeaders     []string
		token             string
		authorization     string
		wantStatus        int
		wantAllowedOrigin string
	}{
		{name: "non-browser authenticated", method: http.MethodPost, token: token, wantStatus: http.StatusOK},
		{name: "bearer whitespace", method: http.MethodPost, authorization: "  Bearer   correct-token  ", wantStatus: http.StatusOK},
		{name: "non-browser missing token", method: http.MethodPost, wantStatus: http.StatusUnauthorized},
		{name: "allowed origin authenticated", method: http.MethodPost, originHeaders: []string{allowedOrigin}, token: token, wantStatus: http.StatusOK, wantAllowedOrigin: allowedOrigin},
		{name: "equivalent origin authenticated", method: http.MethodPost, originHeaders: []string{"HTTPS://CLIENT.EXAMPLE:443"}, token: token, wantStatus: http.StatusOK, wantAllowedOrigin: "HTTPS://CLIENT.EXAMPLE:443"},
		{name: "allowed origin bad token", method: http.MethodPost, originHeaders: []string{allowedOrigin}, token: "wrong", wantStatus: http.StatusUnauthorized, wantAllowedOrigin: allowedOrigin},
		{name: "unapproved origin", method: http.MethodPost, originHeaders: []string{"https://evil.example"}, token: token, wantStatus: http.StatusForbidden},
		{name: "multiple origin headers", method: http.MethodPost, originHeaders: []string{allowedOrigin, "https://evil.example"}, token: token, wantStatus: http.StatusForbidden},
		{name: "approved preflight", method: http.MethodOptions, originHeaders: []string{allowedOrigin}, wantStatus: http.StatusNoContent, wantAllowedOrigin: allowedOrigin},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://server.example/mcp", nil)
			for _, origin := range test.originHeaders {
				request.Header.Add("Origin", origin)
			}
			if test.authorization != "" {
				request.Header.Set("Authorization", test.authorization)
			} else if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if actual := response.Header().Get("Access-Control-Allow-Origin"); actual != test.wantAllowedOrigin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q", actual, test.wantAllowedOrigin)
			}
			wantVary := ""
			if len(test.originHeaders) > 0 {
				wantVary = "Origin"
			}
			if actual := response.Header().Get("Vary"); actual != wantVary {
				t.Fatalf("Vary = %q, want %q", actual, wantVary)
			}
		})
	}
}

func TestHTTPProtectionPrecedenceIncludesDynamicCredentials(t *testing.T) {
	const (
		mcpToken      = "mcp-token"
		minifluxToken = "miniflux-token"
		allowedOrigin = "https://client.example"
	)
	handler := validateOrigin(
		map[string]struct{}{allowedOrigin: {}},
		requireBearerToken(mcpToken,
			enforceMinifluxCredentialHeaders(minifluxCredentialSourceHeader,
				limitMCPRequestBody(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				})),
			),
		),
	)

	tests := []struct {
		name          string
		origin        string
		mcpToken      string
		minifluxToken string
		bodySize      int
		wantStatus    int
	}{
		{name: "origin first", origin: "https://evil.example", wantStatus: http.StatusForbidden},
		{name: "MCP bearer second", origin: allowedOrigin, wantStatus: http.StatusUnauthorized},
		{name: "Miniflux header third", origin: allowedOrigin, mcpToken: mcpToken, wantStatus: http.StatusBadRequest},
		{name: "body limit fourth", origin: allowedOrigin, mcpToken: mcpToken, minifluxToken: minifluxToken, bodySize: httpMaximumRequestBody + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "accepted", origin: allowedOrigin, mcpToken: mcpToken, minifluxToken: minifluxToken, bodySize: 32, wantStatus: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://server.example/mcp", bytes.NewReader(bytes.Repeat([]byte("x"), test.bodySize)))
			request.Header.Set("Origin", test.origin)
			if test.mcpToken != "" {
				request.Header.Set("Authorization", "Bearer "+test.mcpToken)
			}
			if test.minifluxToken != "" {
				request.Header.Set(minifluxTokenHeader, test.minifluxToken)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestDynamicCredentialCORSPreflight(t *testing.T) {
	handler := validateOrigin(map[string]struct{}{"https://client.example": {}}, http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodOptions, "http://server.example/mcp", nil)
	request.Header.Set("Origin", "https://client.example")
	request.Header.Set("Access-Control-Request-Headers", "authorization,x-miniflux-token")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
	if allowed := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(allowed, minifluxTokenHeader) {
		t.Fatalf("Access-Control-Allow-Headers = %q, want %s", allowed, minifluxTokenHeader)
	}
}

func TestDefaultOriginPolicyRejectsBrowserRequests(t *testing.T) {
	handler := validateOrigin(map[string]struct{}{}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://server.example/mcp", nil)
	request.Header.Set("Origin", "https://client.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestHealthcheckResponseIsMinimal(t *testing.T) {
	response := httptest.NewRecorder()
	healthcheckHTTP(response, httptest.NewRequest(http.MethodGet, "http://server.example/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "ok\n" {
		t.Fatalf("body = %q, want %q", response.Body.String(), "ok\n")
	}
	if response.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func TestMCPRequestBodyLimit(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		size          int
		unknownLength bool
		wantStatus    int
	}{
		{name: "ordinary", method: http.MethodPost, size: 128, wantStatus: http.StatusNoContent},
		{name: "exact limit", method: http.MethodPost, size: httpMaximumRequestBody, wantStatus: http.StatusNoContent},
		{name: "oversized content length", method: http.MethodPost, size: httpMaximumRequestBody + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "oversized chunked", method: http.MethodPost, size: httpMaximumRequestBody + 1, unknownLength: true, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "oversized put", method: http.MethodPut, size: httpMaximumRequestBody + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "oversized patch", method: http.MethodPatch, size: httpMaximumRequestBody + 1, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "oversized delete", method: http.MethodDelete, size: httpMaximumRequestBody + 1, wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), test.size)
			handler := limitMCPRequestBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read accepted body: %v", err)
				}
				if !bytes.Equal(body, payload) {
					t.Errorf("accepted body changed: got %d bytes, want %d", len(body), len(payload))
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(test.method, "http://server.example/mcp", bytes.NewReader(payload))
			if test.unknownLength {
				request.ContentLength = -1
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestMCPRequestBodyLimitPreservesAuthAndOriginPrecedence(t *testing.T) {
	const token = "correct-token"
	handler := validateOrigin(
		map[string]struct{}{"https://client.example": {}},
		requireBearerToken(token, limitMCPRequestBody(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))),
	)
	payload := bytes.NewReader(bytes.Repeat([]byte("x"), httpMaximumRequestBody+1))

	unauthorized := httptest.NewRequest(http.MethodPost, "http://server.example/mcp", payload)
	unauthorizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorizedResponse.Code)
	}

	badOrigin := httptest.NewRequest(http.MethodPost, "http://server.example/mcp", bytes.NewReader(bytes.Repeat([]byte("x"), httpMaximumRequestBody+1)))
	badOrigin.Header.Set("Authorization", "Bearer "+token)
	badOrigin.Header.Set("Origin", "https://evil.example")
	badOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(badOriginResponse, badOrigin)
	if badOriginResponse.Code != http.StatusForbidden {
		t.Fatalf("bad-origin status = %d, want 403", badOriginResponse.Code)
	}
}

func TestMCPRequestBodyLimitDoesNotAffectHealthcheck(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/mcp", limitMCPRequestBody(http.NotFoundHandler()))
	mux.HandleFunc("/healthz", healthcheckHTTP)
	request := httptest.NewRequest(http.MethodPost, "http://server.example/healthz", bytes.NewReader(bytes.Repeat([]byte("x"), httpMaximumRequestBody+1)))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "ok\n" {
		t.Fatalf("health response = %d %q", response.Code, response.Body.String())
	}
}

func TestHTTPServerBoundsRequestReads(t *testing.T) {
	server := newHTTPServer(":0", http.NotFoundHandler())

	if server.ReadTimeout != httpReadTimeout {
		t.Errorf("ReadTimeout = %s, want %s", server.ReadTimeout, httpReadTimeout)
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, httpReadHeaderTimeout)
	}
	if server.IdleTimeout != httpIdleTimeout {
		t.Errorf("IdleTimeout = %s, want %s", server.IdleTimeout, httpIdleTimeout)
	}
	if server.MaxHeaderBytes != httpMaxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, httpMaxHeaderBytes)
	}
	if server.WriteTimeout != 0 {
		t.Errorf("WriteTimeout = %s, want zero for SSE support", server.WriteTimeout)
	}
}

func TestHTTPServerShutsDownCleanly(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	requestHandled := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestHandled)
		w.WriteHeader(http.StatusOK)
	})
	server := newHTTPServer(listener.Addr().String(), handler)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveHTTPServer(ctx, server, listener)
	}()

	response, err := (&http.Client{Timeout: time.Second}).Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("request server: %v", err)
	}
	_ = response.Body.Close()
	<-requestHandled
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTPServer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not shut down")
	}
}
