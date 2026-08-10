package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func newToolCallLoggingMiddleware(logger *slog.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			startedAt := time.Now()
			result, err := next(ctx, request)
			outcome := "success"
			if err != nil || result == nil || result.IsError {
				outcome = "error"
			}
			logger.InfoContext(ctx, "MCP tool call",
				"event", "mcp_tool_call",
				"tool", request.Params.Name,
				"class", toolClass(request.Params.Name),
				"outcome", outcome,
				"duration_ms", time.Since(startedAt).Milliseconds(),
			)

			return result, err
		}
	}
}

func toolClass(name string) string {
	if approvedWriteTools().contains(name) {
		return "write"
	}

	return "read"
}
