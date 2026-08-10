package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const containerHealthcheckTimeout = 3 * time.Second

func runHealthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, containerHealthcheckTimeout)
	defer cancel()

	transport, err := loadTransportConfig()
	if err != nil {
		return fmt.Errorf("healthcheck configuration is invalid")
	}
	if transport.Transport == transportStreamableHTTP {
		healthURL, err := localHealthcheckURL(transport.HTTPAddr)
		if err != nil {
			return fmt.Errorf("healthcheck configuration is invalid")
		}
		if err := probeHTTPHealth(ctx, healthURL); err != nil {
			return fmt.Errorf("streamable HTTP healthcheck failed")
		}

		return nil
	}

	cfg, err := loadMinifluxConfig()
	if err != nil {
		return fmt.Errorf("healthcheck configuration is invalid")
	}
	minifluxClient, err := newMinifluxAPIClient(cfg)
	if err != nil {
		return fmt.Errorf("healthcheck configuration is invalid")
	}
	if err := minifluxClient.HealthcheckContext(ctx); err != nil {
		return fmt.Errorf("stdio healthcheck failed")
	}

	return nil
}

func localHealthcheckURL(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port == "" {
		return "", fmt.Errorf("invalid HTTP listen address")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/healthz"}).String(), nil
}

func probeHTTPHealth(ctx context.Context, endpoint string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	httpClient := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		Timeout:   containerHealthcheckTimeout,
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected health status")
	}

	return nil
}
