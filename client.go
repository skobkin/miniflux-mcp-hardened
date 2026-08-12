package main

import (
	"errors"
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

const (
	minifluxCredentialSourceEnvironmentVariable = "MINIFLUX_CREDENTIAL_SOURCE" // #nosec G101 -- environment variable name, not a credential value
	minifluxCredentialSourceConfig              = "config"
	minifluxCredentialSourceHeader              = "header"
)

var errMinifluxRedirect = errors.New("miniflux API redirects are not allowed")

type minifluxConfig struct {
	BaseURL          string
	APIKey           string
	Username         string
	Password         string
	ProxyURL         string
	CredentialSource string
}

func loadMinifluxConfig() (minifluxConfig, error) {
	cfg := minifluxConfig{
		BaseURL:          os.Getenv("MINIFLUX_URL"),
		APIKey:           os.Getenv("MINIFLUX_API_KEY"),
		Username:         os.Getenv("MINIFLUX_USERNAME"),
		Password:         os.Getenv("MINIFLUX_PASSWORD"),
		ProxyURL:         os.Getenv("MINIFLUX_PROXY_URL"),
		CredentialSource: envOrDefault(minifluxCredentialSourceEnvironmentVariable, minifluxCredentialSourceConfig),
	}
	if cfg.BaseURL == "" {
		return minifluxConfig{}, fmt.Errorf("MINIFLUX_URL environment variable is required")
	}
	switch cfg.CredentialSource {
	case minifluxCredentialSourceConfig:
		if cfg.APIKey == "" && (cfg.Username == "" || cfg.Password == "") {
			return minifluxConfig{}, fmt.Errorf("either MINIFLUX_API_KEY or both MINIFLUX_USERNAME and MINIFLUX_PASSWORD must be set when MINIFLUX_CREDENTIAL_SOURCE=%s", minifluxCredentialSourceConfig)
		}
	case minifluxCredentialSourceHeader:
		if cfg.APIKey != "" || cfg.Username != "" || cfg.Password != "" {
			return minifluxConfig{}, fmt.Errorf("MINIFLUX_API_KEY, MINIFLUX_USERNAME, and MINIFLUX_PASSWORD must be unset when MINIFLUX_CREDENTIAL_SOURCE=%s", minifluxCredentialSourceHeader)
		}
	default:
		return minifluxConfig{}, fmt.Errorf("unsupported MINIFLUX_CREDENTIAL_SOURCE %q (supported: %s, %s)", cfg.CredentialSource, minifluxCredentialSourceConfig, minifluxCredentialSourceHeader)
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

	return newMinifluxAPIClientWithHTTPClient(cfg, httpClient), nil
}

func newMinifluxAPIClientWithHTTPClient(cfg minifluxConfig, httpClient *http.Client) *client.Client {
	options := []client.Option{client.WithHTTPClient(httpClient)}
	if cfg.APIKey != "" {
		options = append(options, client.WithAPIKey(cfg.APIKey))
	} else if cfg.Username != "" && cfg.Password != "" {
		options = append(options, client.WithCredentials(cfg.Username, cfg.Password))
	}

	return client.NewClientWithOptions(cfg.BaseURL, options...)
}

func newMinifluxAPIKeyClient(baseURL, apiKey string, httpClient *http.Client) *client.Client {
	return client.NewClientWithOptions(baseURL, client.WithHTTPClient(httpClient), client.WithAPIKey(apiKey))
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

	return &http.Client{
		Transport: transport,
		Timeout:   minifluxRequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errMinifluxRedirect
		},
	}, nil
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
