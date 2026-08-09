package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mark3labs/mcp-go/server"
	"miniflux.app/v2/client"
)

type MinifluxServer struct {
	client minifluxClient
}

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

func NewMinifluxServer() *MinifluxServer {
	baseURL := os.Getenv("MINIFLUX_URL")
	if baseURL == "" {
		log.Fatal("MINIFLUX_URL environment variable is required")
	}

	apiKey := os.Getenv("MINIFLUX_API_KEY")
	username := os.Getenv("MINIFLUX_USERNAME")
	password := os.Getenv("MINIFLUX_PASSWORD")
	if apiKey == "" && (username == "" || password == "") {
		log.Fatal("Either MINIFLUX_API_KEY or both MINIFLUX_USERNAME and MINIFLUX_PASSWORD must be set")
	}

	var minifluxClient *client.Client
	if apiKey != "" {
		minifluxClient = client.NewClient(baseURL, apiKey)
	} else {
		minifluxClient = client.NewClient(baseURL, username, password)
	}

	if err := minifluxClient.Healthcheck(); err != nil {
		log.Fatalf("Healthcheck failed: %v", err)
	}
	log.Printf("Healthcheck passed")

	if _, err := minifluxClient.Me(); err != nil {
		log.Fatalf("Auth failed: %v", err)
	}
	log.Printf("Auth passed")

	return &MinifluxServer{client: minifluxClient}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	transport, err := loadTransportConfig()
	if err != nil {
		log.Fatalf("Invalid transport configuration: %v", err)
	}
	enabledWrites, err := parseWriteTools(os.Getenv(writeToolsEnvironmentVariable))
	if err != nil {
		log.Fatalf("Invalid write-tool configuration: %v", err)
	}
	log.Printf("Starting miniflux-mcp version=%s revision=%s build_date=%s", Version, Revision, BuildDate)

	minifluxServer := NewMinifluxServer()
	mcpServer := server.NewMCPServer(
		"miniflux-mcp",
		Version,
		server.WithLogging(),
	)
	minifluxServer.RegisterTools(mcpServer, enabledWrites)

	if err := serveMCP(ctx, mcpServer, transport); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
