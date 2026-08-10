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

func TestUpdateEntriesStatusRequest(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/v1/entries" {
			t.Errorf("path = %s, want /v1/entries", r.URL.Path)
		}
		var payload struct {
			EntryIDs []int64 `json:"entry_ids"`
			Status   string  `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !reflect.DeepEqual(payload.EntryIDs, []int64{42, 43}) || payload.Status != client.EntryStatusRead {
			t.Errorf("payload = %#v", payload)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer apiServer.Close()

	server := &MinifluxServer{client: client.NewClient(apiServer.URL, "test-api-key")}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_ids": []interface{}{float64(42), float64(43)},
		"status":    client.EntryStatusRead,
	}}}
	result, err := server.UpdateEntriesStatus(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("UpdateEntriesStatus = %#v, %v", result, err)
	}
	var response MCPEntriesStatusUpdate
	if err := json.Unmarshal([]byte(resultText(t, result)), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Updated != 2 || response.Status != client.EntryStatusRead {
		t.Fatalf("response = %#v", response)
	}
}

func TestUpdateEntriesStatusAcceptsOneID(t *testing.T) {
	server := &MinifluxServer{client: &fakeMinifluxClient{}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_ids": []interface{}{float64(42)},
		"status":    client.EntryStatusUnread,
	}}}
	result, err := server.UpdateEntriesStatus(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("UpdateEntriesStatus = %#v, %v", result, err)
	}
	var response MCPEntriesStatusUpdate
	if err := json.Unmarshal([]byte(resultText(t, result)), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Updated != 1 || response.Status != client.EntryStatusUnread {
		t.Fatalf("response = %#v", response)
	}
}

func TestUpdateEntriesStatusHidesBackendErrors(t *testing.T) {
	const backendDetail = "SENTINEL-BULK-STATUS-BACKEND-DETAIL"
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		if err := json.NewEncoder(w).Encode(map[string]string{"error_message": backendDetail}); err != nil {
			t.Errorf("encode backend error: %v", err)
		}
	}))
	defer apiServer.Close()

	server := &MinifluxServer{client: client.NewClient(apiServer.URL, "test-api-key")}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_ids": []interface{}{float64(42)},
		"status":    client.EntryStatusRead,
	}}}
	result, err := server.UpdateEntriesStatus(context.Background(), request)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("UpdateEntriesStatus = %#v, %v; want tool error", result, err)
	}
	if strings.Contains(resultText(t, result), backendDetail) {
		t.Fatalf("bulk status error leaked backend detail: %s", resultText(t, result))
	}
}

func TestUpdateEntriesStatusValidation(t *testing.T) {
	tooMany := make([]interface{}, maximumEntryLimit+1)
	for i := range tooMany {
		tooMany[i] = float64(i + 1)
	}
	tests := []struct {
		name        string
		arguments   map[string]interface{}
		wantMessage string
	}{
		{name: "missing entry IDs", arguments: map[string]interface{}{}, wantMessage: "entry_ids must contain at least one ID"},
		{name: "wrong array type", arguments: map[string]interface{}{"entry_ids": "1", "status": "read"}, wantMessage: "entry_ids must be an array of integers"},
		{name: "empty", arguments: map[string]interface{}{"entry_ids": []interface{}{}, "status": "read"}, wantMessage: "entry_ids must contain at least one ID"},
		{name: "too many", arguments: map[string]interface{}{"entry_ids": tooMany, "status": "read"}, wantMessage: "entry_ids must contain at most 100 items"},
		{name: "duplicate", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1), float64(1)}, "status": "read"}, wantMessage: "entry_ids[1] duplicates entry_ids[0]; IDs must be unique"},
		{name: "zero", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(0)}, "status": "read"}, wantMessage: "entry_ids[0] must be at least 1"},
		{name: "fractional", arguments: map[string]interface{}{"entry_ids": []interface{}{1.5}, "status": "read"}, wantMessage: "entry_ids[0] must be an integer"},
		{name: "unsafe", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1 << 53)}, "status": "read"}, wantMessage: "entry_ids[0] must be a safely representable JSON integer (absolute value must not exceed 9007199254740991)"},
		{
			name: "nested array",
			arguments: map[string]interface{}{
				"entry_ids": []interface{}{[]interface{}{float64(2477), float64(2896), float64(3287)}},
				"status":    "read",
			},
			wantMessage: "entry_ids[0] must be an integer",
		},
		{name: "missing status", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}}, wantMessage: "status is required"},
		{name: "wrong status type", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}, "status": true}, wantMessage: "status must be a string"},
		{name: "removed", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}, "status": "removed"}, wantMessage: "status must be one of: read, unread"},
		{name: "unknown status", arguments: map[string]interface{}{"entry_ids": []interface{}{float64(1)}, "status": "pending"}, wantMessage: "status must be one of: read, unread"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (&MinifluxServer{}).UpdateEntriesStatus(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: test.arguments}})
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("UpdateEntriesStatus = %#v, %v; want tool error", result, err)
			}
			if message := resultText(t, result); message != test.wantMessage {
				t.Fatalf("validation message = %q, want %q", message, test.wantMessage)
			}
		})
	}
}

func TestBulkStatusSchemaMatchesRuntimePolicy(t *testing.T) {
	for _, definition := range (&MinifluxServer{}).writeToolDefinitions() {
		if definition.Tool.Name != "update_entries_status" {
			continue
		}
		entryIDs := definition.Tool.InputSchema.Properties["entry_ids"].(map[string]interface{})
		if entryIDs["minItems"] != 1 || entryIDs["maxItems"] != maximumEntryLimit || entryIDs["uniqueItems"] != true {
			t.Fatalf("entry_ids schema = %#v", entryIDs)
		}

		return
	}
	t.Fatal("update_entries_status definition not found")
}
