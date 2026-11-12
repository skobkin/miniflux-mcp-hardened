package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"miniflux.app/v2/client"
)

// sentinelSecretServer builds a server whose fake backend hides sentinel
// secrets in every credential-bearing backend field.
func sentinelSecretServer() (*fakeMinifluxClient, *MinifluxServer) {
	fake := &fakeMinifluxClient{
		feeds: client.Feeds{secretEntry().Feed},
		entries: &client.EntryResultSet{
			Total:   1,
			Entries: client.Entries{secretEntry()},
		},
		entry: secretEntry(),
	}

	return fake, &MinifluxServer{client: fake}
}

// TestStructuredResultsCarryNoPayloadInTextContent pins the data-minimization
// contract of the structured-only transport: the text content channel holds
// only the pointer notice, never a copy of the result payload.
func TestStructuredResultsCarryNoPayloadInTextContent(t *testing.T) {
	entry := secretEntry()
	entry.Content = "visible article body that must not reach the text channel"
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"entry_id": float64(1)}}}

	result, err := server.GetEntry(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetEntry = %#v, %v", result, err)
	}
	assertStructuredNotice(t, result)
	text := resultText(t, result)
	if strings.Contains(text, "visible article body") || strings.Contains(text, `"content"`) || strings.Contains(text, `{`) {
		t.Fatalf("text content duplicates the structured payload: %s", text)
	}

	digestFake := &fakeMinifluxClient{entries: &client.EntryResultSet{Total: 1, Entries: client.Entries{entry}}}
	digestResult, err := (&MinifluxServer{client: digestFake}).GetUnreadDigest(context.Background(), mcp.CallToolRequest{})
	if err != nil || digestResult.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", digestResult, err)
	}
	assertStructuredNotice(t, digestResult)
	digestText := resultText(t, digestResult)
	if strings.Contains(digestText, "visible article body") || strings.Contains(digestText, `"entries"`) || strings.Contains(digestText, `{`) {
		t.Fatalf("digest text content duplicates the structured payload: %s", digestText)
	}
}

// TestSentinelSecretsAbsentFromSerializedStructuredResults drives every
// structured tool against sentinel-laden backend fixtures and asserts the
// sentinel values are absent from the fully serialized wire form of the
// result, exactly as a client receives it.
func TestSentinelSecretsAbsentFromSerializedStructuredResults(t *testing.T) {
	fake, server := sentinelSecretServer()
	entry := secretEntry()
	entry.Content = "harmless article body"
	fake.entry = entry
	fake.entries.Entries = client.Entries{entry}

	tests := []struct {
		name string
		call func() (*mcp.CallToolResult, error)
	}{
		{name: "get_feeds", call: func() (*mcp.CallToolResult, error) {
			return server.GetFeeds(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "get_feed", call: func() (*mcp.CallToolResult, error) {
			return server.GetFeed(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"feed_id": float64(42)}}})
		}},
		{name: "get_feed_entries", call: func() (*mcp.CallToolResult, error) {
			return server.GetFeedEntries(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"feed_id": float64(42)}}})
		}},
		{name: "get_feed_entry", call: func() (*mcp.CallToolResult, error) {
			return server.GetFeedEntry(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"feed_id": float64(42), "entry_id": float64(1)}}})
		}},
		{name: "get_entries", call: func() (*mcp.CallToolResult, error) {
			return server.GetEntries(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "get_unread_digest", call: func() (*mcp.CallToolResult, error) {
			return server.GetUnreadDigest(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "get_entry", call: func() (*mcp.CallToolResult, error) {
			return server.GetEntry(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"entry_id": float64(1)}}})
		}},
		{name: "get_categories", call: func() (*mcp.CallToolResult, error) {
			return server.GetCategories(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "get_category_feeds", call: func() (*mcp.CallToolResult, error) {
			return server.GetCategoryFeeds(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"category_id": float64(7)}}})
		}},
		{name: "get_category_entries", call: func() (*mcp.CallToolResult, error) {
			return server.GetCategoryEntries(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"category_id": float64(7)}}})
		}},
		{name: "get_category_entry", call: func() (*mcp.CallToolResult, error) {
			return server.GetCategoryEntry(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"category_id": float64(7), "entry_id": float64(1)}}})
		}},
		{name: "get_version", call: func() (*mcp.CallToolResult, error) {
			return server.GetVersion(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "fetch_counters", call: func() (*mcp.CallToolResult, error) {
			return server.FetchCounters(context.Background(), mcp.CallToolRequest{})
		}},
		{name: "update_entries_status", call: func() (*mcp.CallToolResult, error) {
			return server.UpdateEntriesStatus(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}, "status": "read"}}})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call()
			if err != nil {
				t.Fatalf("tool returned error: %v", err)
			}
			if result == nil || result.IsError {
				t.Fatalf("tool returned an error result: %#v", result)
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatalf("encode tool result: %v", err)
			}
			if strings.Contains(string(encoded), sentinelSecret) {
				t.Fatalf("serialized result leaked sentinel secret: %s", encoded)
			}
		})
	}
}
