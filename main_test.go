package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"miniflux.app/v2/client"
)

type fakeStartupClient struct {
	healthError error
	user        *client.User
	authError   error
	healthCtx   context.Context
	authCtx     context.Context
}

func (f *fakeStartupClient) HealthcheckContext(ctx context.Context) error {
	f.healthCtx = ctx

	return f.healthError
}

func (f *fakeStartupClient) MeContext(ctx context.Context) (*client.User, error) {
	f.authCtx = ctx

	return f.user, f.authError
}

func TestVerifyMinifluxStartupUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeStartupClient{healthError: ctx.Err()}

	if err := verifyMinifluxStartup(ctx, fake, testDiscardLogger()); err == nil {
		t.Fatal("verifyMinifluxStartup succeeded with canceled context")
	}
	if fake.healthCtx != ctx {
		t.Fatal("startup context was not passed to healthcheck")
	}
	if fake.authCtx != nil {
		t.Fatal("authentication ran after failed healthcheck")
	}
}

func TestVerifyMinifluxStartupRejectsNilUser(t *testing.T) {
	fake := &fakeStartupClient{}
	if err := verifyMinifluxStartup(context.Background(), fake, testDiscardLogger()); err == nil {
		t.Fatal("verifyMinifluxStartup accepted a nil user")
	}
}

func TestVerifyMinifluxStartupHidesBackendErrors(t *testing.T) {
	fake := &fakeStartupClient{healthError: errors.New("backend secret")}
	if err := verifyMinifluxStartup(context.Background(), fake, testDiscardLogger()); err == nil || err.Error() != "miniflux healthcheck failed" {
		t.Fatalf("error = %v, want stable healthcheck failure", err)
	}
}

func TestVerifyMinifluxStartupHidesAuthenticationErrors(t *testing.T) {
	fake := &fakeStartupClient{authError: errors.New("backend secret")}
	if err := verifyMinifluxStartup(context.Background(), fake, testDiscardLogger()); err == nil || err.Error() != "miniflux authentication failed" {
		t.Fatalf("error = %v, want stable authentication failure", err)
	}
}

func TestVerifyMinifluxStartupUsesInjectedLogger(t *testing.T) {
	previousDefault := slog.Default()
	var defaultOutput bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&defaultOutput, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previousDefault)
	})

	var injectedOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&injectedOutput, nil))
	fake := &fakeStartupClient{user: &client.User{}}
	if err := verifyMinifluxStartup(context.Background(), fake, logger); err != nil {
		t.Fatalf("verifyMinifluxStartup: %v", err)
	}
	if defaultOutput.Len() != 0 {
		t.Fatalf("startup checks used default logger: %s", defaultOutput.String())
	}
	lines := bytes.Split(bytes.TrimSpace(injectedOutput.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("startup log lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		record := decodeLogRecord(t, line)
		if record["msg"] != "miniflux startup check" || record["outcome"] != "success" {
			t.Fatalf("startup log record = %#v", record)
		}
	}
}

func TestVerifyMinifluxHealthDoesNotAuthenticate(t *testing.T) {
	fake := &fakeStartupClient{authError: errors.New("authentication must not run")}
	if err := verifyMinifluxHealth(context.Background(), fake, testDiscardLogger()); err != nil {
		t.Fatalf("verifyMinifluxHealth: %v", err)
	}
	if fake.healthCtx == nil {
		t.Fatal("healthcheck was not called")
	}
	if fake.authCtx != nil {
		t.Fatal("authentication ran during a health-only startup check")
	}
}

func TestDynamicServerStartupPerformsOnlyUnauthenticatedHealthcheck(t *testing.T) {
	var healthCalls, authenticationCalls int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(minifluxNativeTokenHeader) != "" || r.Header.Get("Authorization") != "" {
			t.Error("dynamic startup request included credentials")
		}
		switch r.URL.Path {
		case "/healthcheck":
			healthCalls++
			_, _ = w.Write([]byte("OK"))
		case "/v1/me":
			authenticationCalls++
			http.Error(w, "authentication must not run", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	minifluxServer, err := newMinifluxServerWithConfig(context.Background(), testDiscardLogger(), minifluxConfig{
		BaseURL:          backend.URL,
		CredentialSource: minifluxCredentialSourceHeader,
	})
	if err != nil {
		t.Fatalf("newMinifluxServerWithConfig: %v", err)
	}
	if healthCalls != 1 || authenticationCalls != 0 {
		t.Fatalf("startup calls: health = %d, authentication = %d", healthCalls, authenticationCalls)
	}
	if minifluxServer.toolHandlerMiddleware == nil {
		t.Fatal("dynamic server has no request-scoped client middleware")
	}
	if _, ok := minifluxServer.client.(requestScopedMinifluxClient); !ok {
		t.Fatalf("dynamic server client = %T, want requestScopedMinifluxClient", minifluxServer.client)
	}
}
