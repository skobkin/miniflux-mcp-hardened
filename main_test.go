package main

import (
	"context"
	"errors"
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

	if err := verifyMinifluxStartup(ctx, fake); err == nil {
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
	if err := verifyMinifluxStartup(context.Background(), fake); err == nil {
		t.Fatal("verifyMinifluxStartup accepted a nil user")
	}
}

func TestVerifyMinifluxStartupHidesBackendErrors(t *testing.T) {
	fake := &fakeStartupClient{healthError: errors.New("backend secret")}
	if err := verifyMinifluxStartup(context.Background(), fake); err == nil || err.Error() != "miniflux healthcheck failed" {
		t.Fatalf("error = %v, want stable healthcheck failure", err)
	}
}

func TestVerifyMinifluxStartupHidesAuthenticationErrors(t *testing.T) {
	fake := &fakeStartupClient{authError: errors.New("backend secret")}
	if err := verifyMinifluxStartup(context.Background(), fake); err == nil || err.Error() != "miniflux authentication failed" {
		t.Fatalf("error = %v, want stable authentication failure", err)
	}
}
