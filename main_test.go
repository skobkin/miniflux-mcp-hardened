package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
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
