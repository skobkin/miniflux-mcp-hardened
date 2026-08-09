package main

import (
	"log"
	"os"

	"github.com/mark3labs/mcp-go/server"
	"miniflux.app/v2/client"
)

type MinifluxServer struct {
	client *client.Client
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
	transport, err := loadTransportConfig()
	if err != nil {
		log.Fatalf("Invalid transport configuration: %v", err)
	}
	log.Printf("Starting miniflux-mcp version=%s revision=%s build_date=%s", Version, Revision, BuildDate)

	minifluxServer := NewMinifluxServer()
	mcpServer := server.NewMCPServer(
		"miniflux-mcp",
		Version,
		server.WithLogging(),
	)
	minifluxServer.RegisterAllTools(mcpServer)

	if err := serveMCP(mcpServer, transport); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
