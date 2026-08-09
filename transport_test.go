package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  map[string]struct{}
		wantError bool
	}{
		{name: "empty", value: "", expected: map[string]struct{}{}},
		{name: "multiple", value: "HTTPS://ONE.EXAMPLE:443, http://LOCALHOST:3000", expected: map[string]struct{}{
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
		wantStatus        int
		wantAllowedOrigin string
	}{
		{name: "non-browser authenticated", method: http.MethodPost, token: token, wantStatus: http.StatusOK},
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
			if test.token != "" {
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
		})
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
