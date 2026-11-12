package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"miniflux.app/v2/client"
)

// newValidatingToolServer wires the real tool definitions into an MCP server
// with output schema validation enabled, mirroring main.go.
func newValidatingToolServer(fake *fakeMinifluxClient) *server.MCPServer {
	mcpServer := server.NewMCPServer("miniflux-mcp-test", "test", server.WithOutputSchemaValidation())
	(&MinifluxServer{client: fake}).RegisterTools(mcpServer, allWriteTools())

	return mcpServer
}

func callRegisteredTool(t *testing.T, mcpServer *server.MCPServer, name string, arguments map[string]interface{}) *mcp.CallToolResult {
	t.Helper()
	params := map[string]interface{}{"name": name}
	if arguments != nil {
		params["arguments"] = arguments
	}
	message, err := json.Marshal(mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      mcp.NewRequestId(int64(1)),
		Request: mcp.Request{Method: "tools/call"},
		Params:  params,
	})
	if err != nil {
		t.Fatalf("marshal tools/call request: %v", err)
	}
	response := mcpServer.HandleMessage(context.Background(), message)
	jsonrpcResponse, ok := response.(mcp.JSONRPCResponse)
	if !ok {
		t.Fatalf("tools/call(%s) returned %#v, want a JSON-RPC response", name, response)
	}
	resultBytes, err := json.Marshal(jsonrpcResponse.Result)
	if err != nil {
		t.Fatalf("encode tools/call(%s) result envelope: %v", name, err)
	}
	var result mcp.CallToolResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("decode tools/call(%s) result: %v", name, err)
	}

	return &result
}

// TestStructuredResultsValidateAgainstOutputSchemas drives every structured
// tool through a real server with output schema validation enabled. A DTO
// that drifts from its advertised schema turns the call into a tool error
// ("output schema validation failed: ..."), which fails here.
func TestStructuredResultsValidateAgainstOutputSchemas(t *testing.T) {
	checkedAt := time.Date(2026, time.November, 12, 9, 0, 0, 0, time.UTC)
	fake := &fakeMinifluxClient{
		feeds: client.Feeds{{
			ID:        42,
			Title:     "Example",
			SiteURL:   "https://example.com/",
			CheckedAt: checkedAt,
			Category:  &client.Category{ID: 7, Title: "News"},
		}},
		entries: &client.EntryResultSet{Total: 1, Entries: client.Entries{{
			ID:      1,
			Title:   "Entry",
			Date:    checkedAt,
			Content: "<p>article body</p>",
			Feed:    &client.Feed{ID: 42, Title: "Example"},
		}}},
		entry: func() *client.Entry {
			entry := &client.Entry{ID: 1, Title: "Entry", Date: checkedAt, Content: "<p>article body</p>"}
			entry.Feed = &client.Feed{ID: 42, Title: "Example"}

			return entry
		}(),
	}
	mcpServer := newValidatingToolServer(fake)

	tests := []struct {
		name      string
		tool      string
		arguments map[string]interface{}
	}{
		{name: "feeds", tool: "get_feeds"},
		{name: "feed", tool: "get_feed", arguments: map[string]interface{}{"feed_id": float64(42)}},
		{name: "feed entries", tool: "get_feed_entries", arguments: map[string]interface{}{"feed_id": float64(42)}},
		{name: "feed entry", tool: "get_feed_entry", arguments: map[string]interface{}{"feed_id": float64(42), "entry_id": float64(1)}},
		{name: "entries", tool: "get_entries"},
		{name: "unread digest", tool: "get_unread_digest"},
		{name: "entry", tool: "get_entry", arguments: map[string]interface{}{"entry_id": float64(1)}},
		{name: "categories", tool: "get_categories"},
		{name: "category feeds", tool: "get_category_feeds", arguments: map[string]interface{}{"category_id": float64(7)}},
		{name: "category entries", tool: "get_category_entries", arguments: map[string]interface{}{"category_id": float64(7)}},
		{name: "category entry", tool: "get_category_entry", arguments: map[string]interface{}{"category_id": float64(7), "entry_id": float64(1)}},
		{name: "version", tool: "get_version"},
		{name: "counters", tool: "fetch_counters"},
		{name: "bulk status", tool: "update_entries_status", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}, "status": "read"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := callRegisteredTool(t, mcpServer, test.tool, test.arguments)
			if result.IsError {
				content, _ := mcp.AsTextContent(result.Content[0])
				t.Fatalf("tool %s returned an error result: %s", test.tool, content.Text)
			}
			if result.StructuredContent == nil {
				t.Fatalf("tool %s returned no structured content", test.tool)
			}
			payload, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatalf("encode structured content: %v", err)
			}
			if len(payload) == 0 {
				t.Fatalf("tool %s structured content is empty", test.tool)
			}
			assertStructuredNotice(t, result)
		})
	}
}

// TestOutputSchemaValidationRejectsDriftedDTO proves the enabled validation
// actually rejects structured content that violates the declared schema,
// by registering a handler that knowingly returns mismatched content.
func TestOutputSchemaValidationRejectsDriftedDTO(t *testing.T) {
	mcpServer := server.NewMCPServer("miniflux-mcp-test", "test", server.WithOutputSchemaValidation())
	drifted := structuredObjectTool[MCPVersion]("get_version", "test tool", map[string]interface{}{})
	mcpServer.AddTool(drifted, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// MCPVersion requires the version field; omit it to force a
		// validation failure.
		return structuredToolResult(map[string]interface{}{})
	})

	result := callRegisteredTool(t, mcpServer, "get_version", nil)
	if !result.IsError {
		t.Fatal("drifted structured content passed validation, want tool error")
	}
	content, _ := mcp.AsTextContent(result.Content[0])
	if !strings.Contains(content.Text, "output schema validation failed") {
		t.Fatalf("validation error text = %q, want an output schema validation failure", content.Text)
	}
}
