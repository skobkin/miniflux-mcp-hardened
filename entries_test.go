package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

func TestGetEntriesFilters(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/entries" {
			t.Errorf("path = %s, want /v1/entries", r.URL.Path)
		}

		query := r.URL.Query()
		expectedValues := map[string]string{
			"feed_id":          "42",
			"category_id":      "7",
			"limit":            "1",
			"offset":           "2",
			"published_after":  "1700000000",
			"published_before": "1700003600",
			"changed_after":    "1700000100",
			"changed_before":   "1700003500",
			"after_entry_id":   "10",
			"before_entry_id":  "100",
			"search":           "weather test",
			"starred":          client.FilterOnlyStarred,
			"order":            "published_at",
			"direction":        "desc",
			"globally_visible": "true",
		}
		for name, expected := range expectedValues {
			if actual := query.Get(name); actual != expected {
				t.Errorf("%s = %q, want %q", name, actual, expected)
			}
		}
		if actual := query["status"]; !reflect.DeepEqual(actual, []string{"read", "unread"}) {
			t.Errorf("status = %#v, want read and unread", actual)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(&client.EntryResultSet{Total: 12}); err != nil {
			t.Errorf("encode response body: %v", err)
		}
	}))
	defer apiServer.Close()

	minifluxServer := &MinifluxServer{client: client.NewClient(apiServer.URL, "test-api-key")}
	request := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Arguments: map[string]interface{}{
				"status":           "removed",
				"statuses":         []interface{}{"read", "unread"},
				"feed_id":          float64(42),
				"category_id":      float64(7),
				"limit":            float64(1),
				"offset":           float64(2),
				"published_after":  float64(1700000000),
				"published_before": float64(1700003600),
				"changed_after":    float64(1700000100),
				"changed_before":   float64(1700003500),
				"after_entry_id":   float64(10),
				"before_entry_id":  float64(100),
				"search":           "weather test",
				"starred":          true,
				"order":            "published_at",
				"direction":        "desc",
				"globally_visible": true,
			},
		},
	}

	result, err := minifluxServer.GetEntries(context.Background(), request)
	if err != nil {
		t.Fatalf("GetEntries returned error: %v", err)
	}
	if result.IsError {
		t.Fatalf("GetEntries returned tool error: %#v", result.Content)
	}

	textContent, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("result content type = %T, want text", result.Content[0])
	}
	var entries client.EntryResultSet
	if err := json.Unmarshal([]byte(textContent.Text), &entries); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if entries.Total != 12 {
		t.Errorf("total = %d, want 12", entries.Total)
	}
}

func TestParseEntryFilterValidation(t *testing.T) {
	tests := []struct {
		name      string
		arguments map[string]interface{}
	}{
		{name: "zero limit", arguments: map[string]interface{}{"limit": float64(0)}},
		{name: "large limit", arguments: map[string]interface{}{"limit": float64(maximumEntryLimit + 1)}},
		{name: "negative offset", arguments: map[string]interface{}{"offset": float64(-1)}},
		{name: "zero feed id", arguments: map[string]interface{}{"feed_id": float64(0)}},
		{name: "fractional id", arguments: map[string]interface{}{"feed_id": 1.5}},
		{name: "unsafe float id", arguments: map[string]interface{}{"feed_id": float64(1 << 53)}},
		{name: "zero published timestamp", arguments: map[string]interface{}{"published_after": float64(0)}},
		{name: "invalid status", arguments: map[string]interface{}{"status": "pending"}},
		{name: "invalid order", arguments: map[string]interface{}{"order": "password"}},
		{name: "invalid direction", arguments: map[string]interface{}{"direction": "sideways"}},
		{name: "non-boolean starred", arguments: map[string]interface{}{"starred": "yes"}},
		{name: "non-string search", arguments: map[string]interface{}{"search": true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, result := parseEntryFilter(test.arguments); result == nil || !result.IsError {
				t.Fatalf("parseEntryFilter(%v) succeeded, want tool error", test.arguments)
			}
		})
	}
}

func TestParseEntryFilterValidationMessages(t *testing.T) {
	tests := []struct {
		name        string
		arguments   map[string]interface{}
		wantMessage string
	}{
		{name: "statuses outer type", arguments: map[string]interface{}{"statuses": "read"}, wantMessage: "statuses must be an array of strings"},
		{name: "statuses element type", arguments: map[string]interface{}{"statuses": []interface{}{"read", []interface{}{"unread"}}}, wantMessage: "statuses[1] must be a string"},
		{name: "statuses element value", arguments: map[string]interface{}{"statuses": []interface{}{"read", "pending"}}, wantMessage: "statuses[1] must be one of: read, unread, removed"},
		{name: "status type", arguments: map[string]interface{}{"status": true}, wantMessage: "status must be a string"},
		{name: "status value", arguments: map[string]interface{}{"status": "pending"}, wantMessage: "status must be one of: read, unread, removed"},
		{name: "order type", arguments: map[string]interface{}{"order": true}, wantMessage: "order must be a string"},
		{name: "order value", arguments: map[string]interface{}{"order": "password"}, wantMessage: "order must be one of: id, status, changed_at, published_at, created_at, category_title, category_id, title, author"},
		{name: "direction type", arguments: map[string]interface{}{"direction": true}, wantMessage: "direction must be a string"},
		{name: "direction value", arguments: map[string]interface{}{"direction": "sideways"}, wantMessage: "direction must be one of: asc, desc"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, result := parseEntryFilter(test.arguments)
			if result == nil || !result.IsError {
				t.Fatalf("parseEntryFilter(%v) succeeded, want tool error", test.arguments)
			}
			if message := resultText(t, result); message != test.wantMessage {
				t.Fatalf("validation message = %q, want %q", message, test.wantMessage)
			}
		})
	}
}

func TestArgumentsMustBeJSONObject(t *testing.T) {
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: []interface{}{}}}
	result, err := (&MinifluxServer{}).GetEntries(context.Background(), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("GetEntries = %#v, %v; want tool error", result, err)
	}
	if message := resultText(t, result); message != "arguments must be a JSON object" {
		t.Fatalf("validation message = %q", message)
	}
}

func TestContentOffsetValidationMessages(t *testing.T) {
	entry := &client.Entry{Content: "界"}
	tests := []struct {
		name          string
		contentOffset int64
		wantMessage   string
	}{
		{name: "past content", contentOffset: 4, wantMessage: "content_offset exceeds content length; use 0 or next_content_offset from the previous response"},
		{name: "inside UTF-8 sequence", contentOffset: 1, wantMessage: "content_offset must be 0 or a next_content_offset returned by the previous response"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := boundedEntryDetailResult(entry, test.contentOffset)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("boundedEntryDetailResult = %#v, %v; want tool error", result, err)
			}
			if message := resultText(t, result); message != test.wantMessage {
				t.Fatalf("validation message = %q, want %q", message, test.wantMessage)
			}
		})
	}
}

func TestIntegerArgumentAcceptsMaximumSafeFloat(t *testing.T) {
	value, result := integerArgument(
		map[string]interface{}{"entry_id": float64(maximumSafeJSONInteger)},
		"entry_id",
		true,
		1,
		0,
	)
	if result != nil {
		t.Fatalf("integerArgument returned tool error: %#v", result.Content)
	}
	if value != maximumSafeJSONInteger {
		t.Fatalf("integerArgument returned %d, want %d", value, maximumSafeJSONInteger)
	}
}

func TestParseEntryFilterDefaultsToBoundedLimit(t *testing.T) {
	filter, result := parseEntryFilter(map[string]interface{}{})
	if result != nil {
		t.Fatalf("parseEntryFilter returned tool error: %#v", result.Content)
	}
	if filter.Limit != defaultEntryLimit {
		t.Fatalf("limit = %d, want %d", filter.Limit, defaultEntryLimit)
	}
}

func TestSearchLengthBoundary(t *testing.T) {
	boundary := strings.Repeat("x", maximumFreeFormStringLength)
	filter, result := parseEntryFilter(map[string]interface{}{"search": boundary})
	if result != nil {
		t.Fatalf("boundary search returned tool error: %#v", result.Content)
	}
	if filter.Search != boundary {
		t.Fatal("boundary search was changed")
	}

	for _, search := range []string{
		strings.Repeat("x", maximumFreeFormStringLength+1),
		strings.Repeat("界", maximumFreeFormStringLength+1),
	} {
		if _, result := parseEntryFilter(map[string]interface{}{"search": search}); result == nil || !result.IsError {
			t.Fatal("over-boundary search succeeded")
		}
	}

	multibyteBoundary := strings.Repeat("界", maximumFreeFormStringLength)
	filter, result = parseEntryFilter(map[string]interface{}{"search": multibyteBoundary})
	if result != nil || filter.Search != multibyteBoundary {
		t.Fatalf("multibyte boundary failed: filter=%#v result=%#v", filter, result)
	}
}
