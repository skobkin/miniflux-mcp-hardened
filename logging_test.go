package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestToolCallLoggingSuccess(t *testing.T) {
	const secret = "SENTINEL-TOOL-SECRET"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	middleware := newToolCallLoggingMiddleware(logger)
	handler := middleware(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText(secret), nil
	})
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "get_entries",
		Arguments: map[string]interface{}{"search": secret},
	}}

	if _, err := handler(context.Background(), request); err != nil {
		t.Fatalf("handler: %v", err)
	}
	record := decodeLogRecord(t, output.Bytes())
	if record["event"] != "mcp_tool_call" || record["tool"] != "get_entries" || record["class"] != "read" || record["outcome"] != "success" {
		t.Fatalf("record = %#v", record)
	}
	if _, ok := record["duration_ms"].(float64); !ok {
		t.Fatalf("duration_ms = %#v", record["duration_ms"])
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("log leaked arguments or result: %s", output.String())
	}
}

func TestToolCallLoggingFailureAndWriteClass(t *testing.T) {
	const secret = "SENTINEL-BACKEND-SECRET"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	middleware := newToolCallLoggingMiddleware(logger)
	tests := []struct {
		name    string
		handler server.ToolHandlerFunc
	}{
		{name: "update_entries_status", handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError(secret), nil
		}},
		{name: "update_entry_status", handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New(secret)
		}},
	}
	for _, test := range tests {
		request := mcp.CallToolRequest{Params: mcp.CallToolParams{Name: test.name}}
		_, _ = middleware(test.handler)(context.Background(), request)
	}

	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, want 2", len(lines))
	}
	for _, line := range lines {
		record := decodeLogRecord(t, line)
		if record["class"] != "write" || record["outcome"] != "error" {
			t.Fatalf("record = %#v", record)
		}
	}
	if strings.Contains(output.String(), secret) {
		t.Fatalf("log leaked result or error: %s", output.String())
	}
}

func decodeLogRecord(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var record map[string]interface{}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode log record: %v\n%s", err, data)
	}

	return record
}
