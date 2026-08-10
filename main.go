package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"miniflux.app/v2/client"
)

type MinifluxServer struct {
	client minifluxClient
}

const minifluxStartupTimeout = 15 * time.Second

type minifluxClient interface {
	HealthcheckContext(context.Context) error
	VersionContext(context.Context) (*client.VersionResponse, error)
	CategoriesContext(context.Context) (client.Categories, error)
	CategoryFeedsContext(context.Context, int64) (client.Feeds, error)
	FeedsContext(context.Context) (client.Feeds, error)
	FeedContext(context.Context, int64) (*client.Feed, error)
	RefreshFeedContext(context.Context, int64) error
	FeedEntryContext(context.Context, int64, int64) (*client.Entry, error)
	CategoryEntryContext(context.Context, int64, int64) (*client.Entry, error)
	EntryContext(context.Context, int64) (*client.Entry, error)
	EntriesContext(context.Context, *client.Filter) (*client.EntryResultSet, error)
	FeedEntriesContext(context.Context, int64, *client.Filter) (*client.EntryResultSet, error)
	CategoryEntriesContext(context.Context, int64, *client.Filter) (*client.EntryResultSet, error)
	UpdateEntriesContext(context.Context, []int64, string) error
	ToggleStarredContext(context.Context, int64) error
	FetchCountersContext(context.Context) (*client.FeedCounters, error)
}

type minifluxStartupClient interface {
	HealthcheckContext(context.Context) error
	MeContext(context.Context) (*client.User, error)
}

func NewMinifluxServer(ctx context.Context) (*MinifluxServer, error) {
	cfg, err := loadMinifluxConfig()
	if err != nil {
		return nil, err
	}
	minifluxClient, err := newMinifluxAPIClient(cfg)
	if err != nil {
		return nil, err
	}

	if err := verifyMinifluxStartup(ctx, minifluxClient); err != nil {
		return nil, err
	}

	return &MinifluxServer{client: minifluxClient}, nil
}

func verifyMinifluxStartup(ctx context.Context, miniflux minifluxStartupClient) error {
	if err := miniflux.HealthcheckContext(ctx); err != nil {
		return errors.New("miniflux healthcheck failed")
	}
	slog.Info("miniflux startup check", "check", "health", "outcome", "success")

	user, err := miniflux.MeContext(ctx)
	if err != nil || user == nil {
		return errors.New("miniflux authentication failed")
	}
	slog.Info("miniflux startup check", "check", "authentication", "outcome", "success")

	return nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if err := run(ctx, os.Args[1:], logger); err != nil {
		stop()
		logger.Error("miniflux-mcp stopped", "error", err)
		os.Exit(1)
	}
	stop()
}

func run(ctx context.Context, args []string, logger *slog.Logger) error {
	if len(args) == 1 && args[0] == "healthcheck" {
		return runHealthcheck(ctx)
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: miniflux-mcp [healthcheck]")
	}

	transport, err := loadTransportConfig()
	if err != nil {
		return fmt.Errorf("invalid transport configuration: %w", err)
	}
	enabledWrites, err := parseWriteTools(os.Getenv(writeToolsEnvironmentVariable))
	if err != nil {
		return fmt.Errorf("invalid write-tool configuration: %w", err)
	}
	logger.Info("starting miniflux-mcp", "version", Version, "revision", Revision, "build_date", BuildDate)

	startupCtx, cancelStartup := context.WithTimeout(ctx, minifluxStartupTimeout)
	minifluxServer, err := NewMinifluxServer(startupCtx)
	cancelStartup()
	if err != nil {
		return err
	}
	mcpServer := server.NewMCPServer(
		"miniflux-mcp",
		Version,
		server.WithLogging(),
		server.WithToolHandlerMiddleware(newToolCallLoggingMiddleware(logger)),
	)
	minifluxServer.RegisterTools(mcpServer, enabledWrites)

	if err := serveMCP(ctx, mcpServer, transport); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}

	return nil
}
