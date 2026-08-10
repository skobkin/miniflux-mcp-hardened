package main

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	"miniflux.app/v2/client"
)

const minifluxRequestTimeout = 30 * time.Second

type minifluxConfig struct {
	BaseURL  string
	APIKey   string
	Username string
	Password string
	ProxyURL string
}

func loadMinifluxConfig() (minifluxConfig, error) {
	cfg := minifluxConfig{
		BaseURL:  os.Getenv("MINIFLUX_URL"),
		APIKey:   os.Getenv("MINIFLUX_API_KEY"),
		Username: os.Getenv("MINIFLUX_USERNAME"),
		Password: os.Getenv("MINIFLUX_PASSWORD"),
		ProxyURL: os.Getenv("MINIFLUX_PROXY_URL"),
	}
	if cfg.BaseURL == "" {
		return minifluxConfig{}, fmt.Errorf("MINIFLUX_URL environment variable is required")
	}
	if cfg.APIKey == "" && (cfg.Username == "" || cfg.Password == "") {
		return minifluxConfig{}, fmt.Errorf("either MINIFLUX_API_KEY or both MINIFLUX_USERNAME and MINIFLUX_PASSWORD must be set")
	}
	if cfg.ProxyURL != "" {
		if err := validateMinifluxProxyURL(cfg.ProxyURL); err != nil {
			return minifluxConfig{}, err
		}
	}

	return cfg, nil
}

func newMinifluxAPIClient(cfg minifluxConfig) (*client.Client, error) {
	httpClient, err := newMinifluxHTTPClient(cfg.ProxyURL)
	if err != nil {
		return nil, err
	}
	options := []client.Option{client.WithHTTPClient(httpClient)}
	if cfg.APIKey != "" {
		options = append(options, client.WithAPIKey(cfg.APIKey))
	} else {
		options = append(options, client.WithCredentials(cfg.Username, cfg.Password))
	}

	return client.NewClientWithOptions(cfg.BaseURL, options...), nil
}

func newMinifluxHTTPClient(proxyValue string) (*http.Client, error) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("default HTTP transport is unavailable")
	}
	transport := defaultTransport.Clone()
	if proxyValue != "" {
		if err := validateMinifluxProxyURL(proxyValue); err != nil {
			return nil, err
		}
		proxyConfig := httpproxy.FromEnvironment()
		proxyConfig.HTTPProxy = proxyValue
		proxyConfig.HTTPSProxy = proxyValue
		proxyForURL := proxyConfig.ProxyFunc()
		transport.Proxy = func(request *http.Request) (*url.URL, error) {
			return proxyForURL(request.URL)
		}
	}

	return &http.Client{Transport: transport, Timeout: minifluxRequestTimeout}, nil
}

func validateMinifluxProxyURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("MINIFLUX_PROXY_URL is malformed")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return fmt.Errorf("MINIFLUX_PROXY_URL uses an unsupported scheme")
	}
	if parsed.Host == "" {
		return fmt.Errorf("MINIFLUX_PROXY_URL must include a host")
	}
	if (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery {
		return fmt.Errorf("MINIFLUX_PROXY_URL must not include a path, query, or fragment")
	}

	return nil
}
