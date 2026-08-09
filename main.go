package main

import (
	"context"
	"errors"
	"log"
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
	baseURL := os.Getenv("MINIFLUX_URL")
	if baseURL == "" {
		return nil, errors.New("MINIFLUX_URL environment variable is required")
	}

	apiKey := os.Getenv("MINIFLUX_API_KEY")
	username := os.Getenv("MINIFLUX_USERNAME")
	password := os.Getenv("MINIFLUX_PASSWORD")
	if apiKey == "" && (username == "" || password == "") {
		return nil, errors.New("either MINIFLUX_API_KEY or both MINIFLUX_USERNAME and MINIFLUX_PASSWORD must be set")
	}

	var minifluxClient *client.Client
	if apiKey != "" {
		minifluxClient = client.NewClient(baseURL, apiKey)
	} else {
		minifluxClient = client.NewClient(baseURL, username, password)
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
	log.Printf("Healthcheck passed")

	user, err := miniflux.MeContext(ctx)
	if err != nil || user == nil {
		return errors.New("miniflux authentication failed")
	}
	log.Printf("Auth passed")

	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	transport, err := loadTransportConfig()
	if err != nil {
		stop()
		log.Fatalf("Invalid transport configuration: %v", err)
	}
	enabledWrites, err := parseWriteTools(os.Getenv(writeToolsEnvironmentVariable))
	if err != nil {
		stop()
		log.Fatalf("Invalid write-tool configuration: %v", err)
	}
	log.Printf("Starting miniflux-mcp version=%s revision=%s build_date=%s", Version, Revision, BuildDate)

	startupCtx, cancelStartup := context.WithTimeout(ctx, minifluxStartupTimeout)
	minifluxServer, err := NewMinifluxServer(startupCtx)
	cancelStartup()
	if err != nil {
		stop()
		log.Fatal(err)
	}
	mcpServer := server.NewMCPServer(
		"miniflux-mcp",
		Version,
		server.WithLogging(),
	)
	minifluxServer.RegisterTools(mcpServer, enabledWrites)

	if err := serveMCP(ctx, mcpServer, transport); err != nil {
		stop()
		log.Fatalf("Server failed: %v", err)
	}
	stop()
}
