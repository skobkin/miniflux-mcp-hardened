package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

const (
	defaultEntryLimit      = 50
	maximumEntryLimit      = 100
	maximumSafeJSONInteger = 1<<53 - 1
)

func argumentsMap(request mcp.CallToolRequest) (map[string]interface{}, *mcp.CallToolResult) {
	if request.Params.Arguments == nil {
		return map[string]interface{}{}, nil
	}
	arguments, ok := request.Params.Arguments.(map[string]interface{})
	if !ok {
		return nil, mcp.NewToolResultError("invalid arguments format")
	}
	return arguments, nil
}

func integerArgument(arguments map[string]interface{}, name string, required bool, minimum, maximum int64) (int64, *mcp.CallToolResult) {
	value, exists := arguments[name]
	if !exists {
		if required {
			return 0, mcp.NewToolResultError(fmt.Sprintf("%s is required", name))
		}
		return 0, nil
	}

	var parsed int64
	switch number := value.(type) {
	case float64:
		if math.Trunc(number) != number || number > maximumSafeJSONInteger || number < -maximumSafeJSONInteger {
			return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be a safely representable integer", name))
		}
		parsed = int64(number)
	case int:
		parsed = int64(number)
	case int64:
		parsed = number
	default:
		return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be an integer", name))
	}

	if parsed < minimum || maximum > 0 && parsed > maximum {
		if maximum > 0 {
			return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
		}
		return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be at least %d", name, minimum))
	}
	return parsed, nil
}

func marshalToolResult(value interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return mcp.NewToolResultError("failed to encode response"), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *MinifluxServer) GetFeeds(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	feeds, err := s.client.FeedsContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch feeds"), nil
	}
	return marshalToolResult(toMCPFeeds(feeds))
}

func (s *MinifluxServer) GetFeed(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	feedID, result := integerArgument(arguments, "feed_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	feed, err := s.client.FeedContext(ctx, feedID)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch feed"), nil
	}
	return marshalToolResult(toMCPFeed(feed))
}

func parseEntryFilter(arguments map[string]interface{}) (*client.Filter, *mcp.CallToolResult) {
	filter := &client.Filter{Limit: defaultEntryLimit}

	if rawStatuses, exists := arguments["statuses"]; exists {
		statuses, ok := rawStatuses.([]interface{})
		if !ok {
			return nil, mcp.NewToolResultError("statuses must be an array")
		}
		for _, rawStatus := range statuses {
			status, ok := rawStatus.(string)
			if !ok || !validFilterStatus(status) {
				return nil, mcp.NewToolResultError("statuses must contain only read, unread, or removed")
			}
			filter.Statuses = append(filter.Statuses, status)
		}
	}
	if rawStatus, exists := arguments["status"]; exists && len(filter.Statuses) == 0 {
		status, ok := rawStatus.(string)
		if !ok || !validFilterStatus(status) {
			return nil, mcp.NewToolResultError("status must be read, unread, or removed")
		}
		filter.Status = status
	}

	idFields := map[string]*int64{
		"feed_id":         &filter.FeedID,
		"category_id":     &filter.CategoryID,
		"before_entry_id": &filter.BeforeEntryID,
		"after_entry_id":  &filter.AfterEntryID,
	}
	for name, target := range idFields {
		value, result := integerArgument(arguments, name, false, 1, 0)
		if result != nil {
			return nil, result
		}
		*target = value
	}

	timestampFields := map[string]*int64{
		"published_after":  &filter.PublishedAfter,
		"published_before": &filter.PublishedBefore,
		"changed_after":    &filter.ChangedAfter,
		"changed_before":   &filter.ChangedBefore,
	}
	for name, target := range timestampFields {
		value, result := integerArgument(arguments, name, false, 1, 0)
		if result != nil {
			return nil, result
		}
		*target = value
	}

	limit, result := integerArgument(arguments, "limit", false, 1, maximumEntryLimit)
	if result != nil {
		return nil, result
	}
	if limit > 0 {
		filter.Limit = int(limit)
	}
	offset, result := integerArgument(arguments, "offset", false, 0, 0)
	if result != nil {
		return nil, result
	}
	filter.Offset = int(offset)

	if value, exists := arguments["search"]; exists {
		search, ok := value.(string)
		if !ok {
			return nil, mcp.NewToolResultError("search must be a string")
		}
		filter.Search = search
	}
	if value, exists := arguments["starred"]; exists {
		starred, ok := value.(bool)
		if !ok {
			return nil, mcp.NewToolResultError("starred must be a boolean")
		}
		if starred {
			filter.Starred = client.FilterOnlyStarred
		} else {
			filter.Starred = client.FilterNotStarred
		}
	}
	if value, exists := arguments["order"]; exists {
		order, ok := value.(string)
		if !ok || !validEntryOrder(order) {
			return nil, mcp.NewToolResultError("order is not supported")
		}
		filter.Order = order
	}
	if value, exists := arguments["direction"]; exists {
		direction, ok := value.(string)
		if !ok || direction != "asc" && direction != "desc" {
			return nil, mcp.NewToolResultError("direction must be asc or desc")
		}
		filter.Direction = direction
	}
	if value, exists := arguments["globally_visible"]; exists {
		globallyVisible, ok := value.(bool)
		if !ok {
			return nil, mcp.NewToolResultError("globally_visible must be a boolean")
		}
		filter.GloballyVisible = globallyVisible
	}
	return filter, nil
}

func validEntryOrder(order string) bool {
	switch order {
	case "id", "status", "changed_at", "published_at", "created_at", "category_title", "category_id", "title", "author":
		return true
	default:
		return false
	}
}

func validFilterStatus(status string) bool {
	return status == "read" || status == "unread" || status == "removed"
}

func (s *MinifluxServer) GetEntries(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	filter, result := parseEntryFilter(arguments)
	if result != nil {
		return result, nil
	}
	entries, err := s.client.EntriesContext(ctx, filter)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch entries"), nil
	}
	return marshalToolResult(toMCPEntryResultSet(entries))
}

func (s *MinifluxServer) GetEntry(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	entryID, result := integerArgument(arguments, "entry_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.EntryContext(ctx, entryID)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch entry"), nil
	}
	return marshalToolResult(toMCPEntryDetail(entry))
}

func (s *MinifluxServer) UpdateEntryStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	entryID, result := integerArgument(arguments, "entry_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	status, ok := arguments["status"].(string)
	if !ok || status != "read" && status != "unread" {
		return mcp.NewToolResultError("status must be read or unread"), nil
	}
	if err := s.client.UpdateEntriesContext(ctx, []int64{entryID}, status); err != nil {
		return mcp.NewToolResultError("failed to update entry status"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Entry %d status updated to: %s", entryID, status)), nil
}

func (s *MinifluxServer) RefreshFeed(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	feedID, result := integerArgument(arguments, "feed_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	if err := s.client.RefreshFeedContext(ctx, feedID); err != nil {
		return mcp.NewToolResultError("failed to refresh feed"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Feed %d refreshed successfully", feedID)), nil
}

func (s *MinifluxServer) GetCategories(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	categories, err := s.client.CategoriesContext(ctx)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch categories"), nil
	}
	return marshalToolResult(toMCPCategories(categories))
}

func scopedFilter(arguments map[string]interface{}) (*client.Filter, *mcp.CallToolResult) {
	filter := &client.Filter{Limit: defaultEntryLimit}
	if rawStatus, exists := arguments["status"]; exists {
		status, ok := rawStatus.(string)
		if !ok || !validFilterStatus(status) {
			return nil, mcp.NewToolResultError("status must be read, unread, or removed")
		}
		filter.Status = status
	}
	limit, result := integerArgument(arguments, "limit", false, 1, maximumEntryLimit)
	if result != nil {
		return nil, result
	}
	if limit > 0 {
		filter.Limit = int(limit)
	}
	offset, result := integerArgument(arguments, "offset", false, 0, 0)
	if result != nil {
		return nil, result
	}
	filter.Offset = int(offset)
	return filter, nil
}

func (s *MinifluxServer) GetFeedEntries(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	feedID, result := integerArgument(arguments, "feed_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	filter, result := scopedFilter(arguments)
	if result != nil {
		return result, nil
	}
	entries, err := s.client.FeedEntriesContext(ctx, feedID, filter)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch feed entries"), nil
	}
	return marshalToolResult(toMCPEntryResultSet(entries))
}

func (s *MinifluxServer) GetFeedEntry(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	feedID, result := integerArgument(arguments, "feed_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	entryID, result := integerArgument(arguments, "entry_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.FeedEntryContext(ctx, feedID, entryID)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch feed entry"), nil
	}
	return marshalToolResult(toMCPEntryDetail(entry))
}

func (s *MinifluxServer) GetCategoryFeeds(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	categoryID, result := integerArgument(arguments, "category_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	feeds, err := s.client.CategoryFeedsContext(ctx, categoryID)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch category feeds"), nil
	}
	return marshalToolResult(toMCPFeeds(feeds))
}

func (s *MinifluxServer) GetCategoryEntries(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	categoryID, result := integerArgument(arguments, "category_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	filter, result := scopedFilter(arguments)
	if result != nil {
		return result, nil
	}
	entries, err := s.client.CategoryEntriesContext(ctx, categoryID, filter)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch category entries"), nil
	}
	return marshalToolResult(toMCPEntryResultSet(entries))
}

func (s *MinifluxServer) GetCategoryEntry(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	categoryID, result := integerArgument(arguments, "category_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	entryID, result := integerArgument(arguments, "entry_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.CategoryEntryContext(ctx, categoryID, entryID)
	if err != nil {
		return mcp.NewToolResultError("failed to fetch category entry"), nil
	}
	return marshalToolResult(toMCPEntryDetail(entry))
}

func (s *MinifluxServer) ToggleStarred(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	entryID, result := integerArgument(arguments, "entry_id", true, 1, 0)
	if result != nil {
		return result, nil
	}
	if err := s.client.ToggleStarredContext(ctx, entryID); err != nil {
		return mcp.NewToolResultError("failed to toggle starred status"), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("Starred status toggled for entry %d", entryID)), nil
}

func (s *MinifluxServer) GetVersion(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	version, err := s.client.VersionContext(ctx)
	if err != nil || version == nil {
		return mcp.NewToolResultError("failed to fetch version"), nil
	}
	return marshalToolResult(MCPVersion{Version: version.Version})
}

func (s *MinifluxServer) Healthcheck(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.client.HealthcheckContext(ctx); err != nil {
		return mcp.NewToolResultError("healthcheck failed"), nil
	}
	return mcp.NewToolResultText("Healthcheck passed"), nil
}

func (s *MinifluxServer) FetchCounters(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	counters, err := s.client.FetchCountersContext(ctx)
	if err != nil || counters == nil {
		return mcp.NewToolResultError("failed to fetch counters"), nil
	}
	return marshalToolResult(toMCPFeedCounters(counters))
}
