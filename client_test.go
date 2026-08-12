package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewMinifluxHTTPClientDefaults(t *testing.T) {
	httpClient, err := newMinifluxHTTPClient("")
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	if httpClient.Timeout != minifluxRequestTimeout {
		t.Fatalf("timeout = %s, want %s", httpClient.Timeout, minifluxRequestTimeout)
	}
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok || transport == http.DefaultTransport {
		t.Fatalf("transport = %T, want cloned *http.Transport", httpClient.Transport)
	}
}

func TestMinifluxProxySchemes(t *testing.T) {
	for _, value := range []string{
		"http://proxy.example:8080",
		"https://user:password@proxy.example:8443/",
		"socks5://proxy.example:1080",
		"socks5h://proxy.example:1080",
	} {
		t.Run(strings.Split(value, ":")[0], func(t *testing.T) {
			if _, err := newMinifluxHTTPClient(value); err != nil {
				t.Fatalf("newMinifluxHTTPClient(%q): %v", value, err)
			}
		})
	}
}

func TestMinifluxProxyValidationHidesCredentials(t *testing.T) {
	const secret = "SENTINEL-PROXY-PASSWORD"
	for _, value := range []string{
		"ftp://user:" + secret + "@proxy.example",
		"http://user:" + secret + "@[::1",
		"http://",
		"http://proxy.example/path",
		"http://proxy.example?password=" + secret,
		"http://proxy.example#" + secret,
	} {
		_, err := newMinifluxHTTPClient(value)
		if err == nil {
			t.Errorf("newMinifluxHTTPClient(%q) succeeded", value)

			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked proxy credential: %v", err)
		}
	}
}

func TestExplicitMinifluxProxyRoutesRequests(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	requests := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	defer proxy.Close()

	httpClient, err := newMinifluxHTTPClient(proxy.URL)
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://miniflux.invalid/v1/healthcheck", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	proxied := <-requests
	if proxied.URL.Host != "miniflux.invalid" || proxied.URL.Path != "/v1/healthcheck" {
		t.Fatalf("proxied URL = %s", proxied.URL.String())
	}
}

func TestMinifluxAPIClientUsesConfiguredProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	requests := make(chan *http.Request, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		_, _ = w.Write([]byte("OK"))
	}))
	defer proxy.Close()

	minifluxClient, err := newMinifluxAPIClient(minifluxConfig{
		BaseURL:  "http://miniflux.invalid",
		APIKey:   "api-key",
		ProxyURL: proxy.URL,
	})
	if err != nil {
		t.Fatalf("newMinifluxAPIClient: %v", err)
	}
	if err := minifluxClient.HealthcheckContext(context.Background()); err != nil {
		t.Fatalf("HealthcheckContext: %v", err)
	}
	proxied := <-requests
	if proxied.URL.Host != "miniflux.invalid" || proxied.URL.Path != "/healthcheck" {
		t.Fatalf("proxied Miniflux URL = %s", proxied.URL.String())
	}
}

func TestExplicitMinifluxProxyHonorsNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "miniflux.example")
	t.Setenv("no_proxy", "")
	httpClient, err := newMinifluxHTTPClient("http://proxy.example:8080")
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	transport := httpClient.Transport.(*http.Transport)
	request, err := http.NewRequest(http.MethodGet, "https://miniflux.example/v1/feeds", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	proxyURL, err := transport.Proxy(request)
	if err != nil {
		t.Fatalf("select proxy: %v", err)
	}
	if proxyURL != nil {
		t.Fatalf("proxy = %s, want direct connection", proxyURL)
	}
}

func TestLoadMinifluxConfigValidatesProxy(t *testing.T) {
	t.Setenv("MINIFLUX_URL", "https://miniflux.example")
	t.Setenv("MINIFLUX_API_KEY", "api-key")
	t.Setenv("MINIFLUX_PROXY_URL", "file://proxy.example")
	if _, err := loadMinifluxConfig(); err == nil {
		t.Fatal("loadMinifluxConfig accepted unsupported proxy")
	}
}

func TestLoadMinifluxConfigCredentialSources(t *testing.T) {
	tests := []struct {
		name             string
		credentialSource string
		apiKey           string
		username         string
		password         string
		wantSource       string
		wantError        bool
	}{
		{name: "default configured API key", apiKey: "api-key", wantSource: minifluxCredentialSourceConfig},
		{name: "configured basic", credentialSource: minifluxCredentialSourceConfig, username: "user", password: "password", wantSource: minifluxCredentialSourceConfig},
		{name: "header", credentialSource: minifluxCredentialSourceHeader, wantSource: minifluxCredentialSourceHeader},
		{name: "header rejects API key", credentialSource: minifluxCredentialSourceHeader, apiKey: "api-key", wantError: true},
		{name: "header rejects username", credentialSource: minifluxCredentialSourceHeader, username: "user", wantError: true},
		{name: "header rejects password", credentialSource: minifluxCredentialSourceHeader, password: "password", wantError: true},
		{name: "configured missing credentials", credentialSource: minifluxCredentialSourceConfig, wantError: true},
		{name: "configured incomplete basic", credentialSource: minifluxCredentialSourceConfig, username: "user", wantError: true},
		{name: "unknown", credentialSource: "dynamic", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("MINIFLUX_URL", "https://miniflux.example")
			t.Setenv(minifluxCredentialSourceEnvironmentVariable, test.credentialSource)
			t.Setenv("MINIFLUX_API_KEY", test.apiKey)
			t.Setenv("MINIFLUX_USERNAME", test.username)
			t.Setenv("MINIFLUX_PASSWORD", test.password)
			t.Setenv("MINIFLUX_PROXY_URL", "")

			cfg, err := loadMinifluxConfig()
			if test.wantError {
				if err == nil {
					t.Fatalf("loadMinifluxConfig succeeded: %#v", cfg)
				}

				return
			}
			if err != nil {
				t.Fatalf("loadMinifluxConfig: %v", err)
			}
			if cfg.CredentialSource != test.wantSource {
				t.Fatalf("credential source = %q, want %q", cfg.CredentialSource, test.wantSource)
			}
		})
	}
}

func TestValidateMinifluxCredentialTransport(t *testing.T) {
	tests := []struct {
		name             string
		credentialSource string
		transport        string
		wantError        bool
	}{
		{name: "configured stdio", credentialSource: minifluxCredentialSourceConfig, transport: transportStdio},
		{name: "configured HTTP", credentialSource: minifluxCredentialSourceConfig, transport: transportStreamableHTTP},
		{name: "header HTTP", credentialSource: minifluxCredentialSourceHeader, transport: transportStreamableHTTP},
		{name: "header rejects stdio", credentialSource: minifluxCredentialSourceHeader, transport: transportStdio, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateMinifluxCredentialTransport(
				minifluxConfig{CredentialSource: test.credentialSource},
				transportConfig{Transport: test.transport},
			)
			if (err != nil) != test.wantError {
				t.Fatalf("validateMinifluxCredentialTransport error = %v, wantError = %t", err, test.wantError)
			}
		})
	}
}
