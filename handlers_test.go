package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

func TestToggleStarredUsesStarEndpoint(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/v1/entries/42/star" {
			t.Errorf("path = %s, want /v1/entries/42/star", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer apiServer.Close()

	minifluxServer := &MinifluxServer{client: client.NewClient(apiServer.URL, "test-api-key")}
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{"entry_id": float64(42)},
		},
	}

	result, err := minifluxServer.ToggleStarred(context.Background(), request)
	if err != nil {
		t.Fatalf("ToggleStarred returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("ToggleStarred returned tool error: %#v", result.Content)
	}
}

func TestUpdateEntryStatusRejectsRemoved(t *testing.T) {
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"entry_id": float64(42),
				"status":   "removed",
			},
		},
	}

	result, err := (&MinifluxServer{}).UpdateEntryStatus(context.Background(), request)
	if err != nil {
		t.Fatalf("UpdateEntryStatus returned error: %v", err)
	}
	if !result.IsError {
		t.Fatal("UpdateEntryStatus accepted removed status")
	}
}
