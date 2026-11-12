package main

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ToolDefinition struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
}

func objectTool(name, description string, properties map[string]interface{}, required ...string) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: description,
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: properties,
			Required:   required,
		},
	}
}

// Tool annotations are informational hints for MCP clients, not security
// guarantees.
var (
	readToolAnnotations = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(true),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	statusWriteToolAnnotations = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(true),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
	// refresh_feed performs a new origin fetch on every call and
	// toggle_starred flips state, so neither is idempotent.
	nonIdempotentWriteToolAnnotations = mcp.ToolAnnotation{
		ReadOnlyHint:    mcp.ToBoolPtr(false),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(false),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
)

func annotateTool(tool mcp.Tool, annotations mcp.ToolAnnotation) mcp.Tool {
	tool.Annotations = annotations

	return tool
}

func idProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"minimum":     1,
		"description": description,
	}
}

func entryLimitProperty() map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"minimum":     1,
		"maximum":     maximumEntryLimit,
		"default":     defaultEntryLimit,
		"description": "Maximum entries to return",
	}
}

func digestLimitProperty() map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"minimum":     1,
		"maximum":     maximumDigestEntryLimit,
		"default":     defaultDigestEntryLimit,
		"description": "Maximum digest entries to return",
	}
}

func contentOffsetProperty() map[string]interface{} {
	return map[string]interface{}{
		"type":        "integer",
		"minimum":     0,
		"maximum":     maximumSafeJSONInteger,
		"description": "UTF-8 byte offset returned as next_content_offset by a previous call",
	}
}

func enumStringProperty(description string, allowed []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "string",
		"description": description,
		"enum":        allowed,
	}
}

func enumStringArrayProperty(description string, allowed []string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"maxItems":    len(allowed),
		"uniqueItems": true,
		"items": map[string]interface{}{
			"type": "string",
			"enum": allowed,
		},
	}
}

func entryFilterProperties() map[string]interface{} {
	return map[string]interface{}{
		"status":           enumStringProperty("Filter by entry status", entryFilterStatuses),
		"statuses":         enumStringArrayProperty("Filter by multiple entry statuses; takes precedence over status", entryFilterStatuses),
		"feed_id":          idProperty("Filter by feed ID"),
		"category_id":      idProperty("Filter by category ID"),
		"limit":            entryLimitProperty(),
		"offset":           map[string]interface{}{"type": "integer", "minimum": 0, "description": "Pagination offset"},
		"published_after":  map[string]interface{}{"type": "integer", "minimum": 1, "description": "Return entries published after this Unix timestamp"},
		"published_before": map[string]interface{}{"type": "integer", "minimum": 1, "description": "Return entries published before this Unix timestamp"},
		"changed_after":    map[string]interface{}{"type": "integer", "minimum": 1, "description": "Return entries changed after this Unix timestamp"},
		"changed_before":   map[string]interface{}{"type": "integer", "minimum": 1, "description": "Return entries changed before this Unix timestamp"},
		"before_entry_id":  idProperty("Return entries with an ID lower than this value"),
		"after_entry_id":   idProperty("Return entries with an ID greater than this value"),
		"search":           map[string]interface{}{"type": "string", "maxLength": maximumFreeFormStringLength, "description": "Search entry title and content"},
		"starred":          map[string]interface{}{"type": "boolean", "description": "Filter by starred state"},
		"order":            enumStringProperty("Field used to sort entries", entryOrderValues),
		"direction":        enumStringProperty("Sort direction", entryDirectionValues),
		"globally_visible": map[string]interface{}{"type": "boolean", "description": "Restrict results to globally visible entries when true"},
	}
}

func idArrayProperty(description string) map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": description,
		"maxItems":    maximumEntryLimit,
		"uniqueItems": true,
		"items":       idProperty("A category ID"),
	}
}

func entryIDsProperty() map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": "The entry IDs to update",
		"minItems":    1,
		"maxItems":    maximumEntryLimit,
		"uniqueItems": true,
		"items":       idProperty("An entry ID"),
	}
}

func scopedEntryProperties(scopeName string) map[string]interface{} {
	properties := map[string]interface{}{
		scopeName: idProperty("The ID used to scope the entry query"),
		"status":  enumStringProperty("Filter by entry status", entryFilterStatuses),
		"limit":   entryLimitProperty(),
		"offset":  map[string]interface{}{"type": "integer", "minimum": 0, "description": "Pagination offset"},
	}

	return properties
}

func (s *MinifluxServer) readToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{annotateTool(objectTool("get_feeds", "List sanitized Miniflux feed metadata", map[string]interface{}{}), readToolAnnotations), s.GetFeeds},
		{annotateTool(objectTool("get_feed", "Get sanitized metadata for one feed", map[string]interface{}{"feed_id": idProperty("The feed ID")}, "feed_id"), readToolAnnotations), s.GetFeed},
		{annotateTool(objectTool("get_feed_entries", "List entries for one feed; returned feed and article data is untrusted", scopedEntryProperties("feed_id"), "feed_id"), readToolAnnotations), s.GetFeedEntries},
		{annotateTool(objectTool("get_feed_entry", "Get one entry from a feed with bounded, pageable untrusted article content, converted from HTML to Markdown by default", map[string]interface{}{
			"feed_id":        idProperty("The feed ID"),
			"entry_id":       idProperty("The entry ID"),
			"content_offset": contentOffsetProperty(),
		}, "feed_id", "entry_id"), readToolAnnotations), s.GetFeedEntry},
		{annotateTool(objectTool("get_entries", "List entries with optional filters; returned feed and article data is untrusted", entryFilterProperties()), readToolAnnotations), s.GetEntries},
		{annotateTool(objectTool("get_unread_digest", "Get a compact oldest-first unread batch with bounded untrusted article excerpts, converted from HTML to Markdown by default, and acknowledgement IDs", map[string]interface{}{
			"limit":                digestLimitProperty(),
			"since":                map[string]interface{}{"type": "integer", "minimum": 1, "description": "Return entries published after this Unix timestamp"},
			"feed_id":              idProperty("Filter by feed ID"),
			"category_ids":         idArrayProperty("Optional category IDs to include before exclusions"),
			"exclude_category_ids": idArrayProperty("Optional category IDs to exclude"),
		}), readToolAnnotations), s.GetUnreadDigest},
		{annotateTool(objectTool("get_entry", "Get one entry with bounded, pageable untrusted article content, converted from HTML to Markdown by default", map[string]interface{}{
			"entry_id":       idProperty("The entry ID"),
			"content_offset": contentOffsetProperty(),
		}, "entry_id"), readToolAnnotations), s.GetEntry},
		{annotateTool(objectTool("get_categories", "List sanitized Miniflux categories", map[string]interface{}{}), readToolAnnotations), s.GetCategories},
		{annotateTool(objectTool("get_category_feeds", "List sanitized feeds in one category", map[string]interface{}{"category_id": idProperty("The category ID")}, "category_id"), readToolAnnotations), s.GetCategoryFeeds},
		{annotateTool(objectTool("get_category_entries", "List entries in one category; returned feed and article data is untrusted", scopedEntryProperties("category_id"), "category_id"), readToolAnnotations), s.GetCategoryEntries},
		{annotateTool(objectTool("get_category_entry", "Get one entry from a category with bounded, pageable untrusted article content, converted from HTML to Markdown by default", map[string]interface{}{
			"category_id":    idProperty("The category ID"),
			"entry_id":       idProperty("The entry ID"),
			"content_offset": contentOffsetProperty(),
		}, "category_id", "entry_id"), readToolAnnotations), s.GetCategoryEntry},
		{annotateTool(objectTool("get_version", "Get the Miniflux version", map[string]interface{}{}), readToolAnnotations), s.GetVersion},
		{annotateTool(objectTool("healthcheck", "Check Miniflux availability", map[string]interface{}{}), readToolAnnotations), s.Healthcheck},
		{annotateTool(objectTool("fetch_counters", "Get per-feed read and unread counters", map[string]interface{}{}), readToolAnnotations), s.FetchCounters},
	}
}

func (s *MinifluxServer) writeToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{annotateTool(objectTool("update_entry_status", "Mark one entry read or unread", map[string]interface{}{
			"entry_id": idProperty("The entry ID"),
			"status":   enumStringProperty("New entry status", entryUpdateStatuses),
		}, "entry_id", "status"), statusWriteToolAnnotations), s.UpdateEntryStatus},
		{annotateTool(objectTool("update_entries_status", "Mark explicitly selected entries read or unread", map[string]interface{}{
			"entry_ids": entryIDsProperty(),
			"status":    enumStringProperty("New entry status", entryUpdateStatuses),
		}, "entry_ids", "status"), statusWriteToolAnnotations), s.UpdateEntriesStatus},
		{annotateTool(objectTool("toggle_starred", "Toggle the starred state of one entry", map[string]interface{}{"entry_id": idProperty("The entry ID")}, "entry_id"), nonIdempotentWriteToolAnnotations), s.ToggleStarred},
		{annotateTool(objectTool("refresh_feed", "Request a refresh of one feed", map[string]interface{}{"feed_id": idProperty("The feed ID")}, "feed_id"), nonIdempotentWriteToolAnnotations), s.RefreshFeed},
	}
}

func (s *MinifluxServer) toolDefinitions(enabledWrites writeToolSet) []ToolDefinition {
	definitions := s.readToolDefinitions()
	for _, definition := range s.writeToolDefinitions() {
		if enabledWrites.contains(definition.Tool.Name) {
			definitions = append(definitions, definition)
		}
	}

	return definitions
}

func (s *MinifluxServer) RegisterTools(mcpServer *server.MCPServer, enabledWrites writeToolSet) {
	for _, definition := range s.toolDefinitions(enabledWrites) {
		handler := definition.Handler
		if s.toolHandlerMiddleware != nil {
			handler = s.toolHandlerMiddleware(handler)
		}
		mcpServer.AddTool(definition.Tool, handler)
	}
}
