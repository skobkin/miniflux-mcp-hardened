package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

const (
	defaultEntryLimit           = 50
	maximumEntryLimit           = 100
	maximumSafeJSONInteger      = 1<<53 - 1
	maximumDigestCandidates     = 1000
	maximumFreeFormStringLength = 4096
)

func toolErrorResult(message string) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(message), nil
}

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

	return integerValue(value, name, minimum, maximum)
}

func integerValue(value interface{}, name string, minimum, maximum int64) (int64, *mcp.CallToolResult) {
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

func integerArrayArgument(arguments map[string]interface{}, name string, maximumItems int) ([]int64, *mcp.CallToolResult) {
	value, exists := arguments[name]
	if !exists {
		return nil, nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s must be an array", name))
	}
	if len(items) > maximumItems {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s must contain at most %d IDs", name, maximumItems))
	}

	result := make([]int64, 0, len(items))
	seen := make(map[int64]struct{}, len(items))
	for _, item := range items {
		id, validationResult := integerValue(item, name, 1, 0)
		if validationResult != nil {
			return nil, validationResult
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, mcp.NewToolResultError(fmt.Sprintf("%s must not contain duplicate IDs", name))
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}

	return result, nil
}

func stringArgument(arguments map[string]interface{}, name string, maximumLength int) (string, *mcp.CallToolResult) {
	value, exists := arguments[name]
	if !exists {
		return "", nil
	}
	parsed, ok := value.(string)
	if !ok {
		return "", mcp.NewToolResultError(fmt.Sprintf("%s must be a string", name))
	}
	if utf8.RuneCountInString(parsed) > maximumLength {
		return "", mcp.NewToolResultError(fmt.Sprintf("%s must contain at most %d characters", name, maximumLength))
	}

	return parsed, nil
}

func marshalToolResult(value interface{}) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return toolErrorResult("failed to encode response")
	}

	return mcp.NewToolResultText(string(data)), nil
}

func (s *MinifluxServer) GetFeeds(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	feeds, err := s.client.FeedsContext(ctx)
	if err != nil {
		return toolErrorResult("failed to fetch feeds")
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
		return toolErrorResult("failed to fetch feed")
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

	search, result := stringArgument(arguments, "search", maximumFreeFormStringLength)
	if result != nil {
		return nil, result
	}
	filter.Search = search
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
		return toolErrorResult("failed to fetch entries")
	}

	return marshalToolResult(toMCPEntryResultSet(entries))
}

type unreadDigestOptions struct {
	limit              int
	since              int64
	feedID             int64
	categoryIDs        []int64
	excludeCategoryIDs []int64
}

func parseUnreadDigestOptions(arguments map[string]interface{}) (unreadDigestOptions, *mcp.CallToolResult) {
	options := unreadDigestOptions{limit: defaultEntryLimit}

	limit, result := integerArgument(arguments, "limit", false, 1, maximumEntryLimit)
	if result != nil {
		return options, result
	}
	if limit > 0 {
		options.limit = int(limit)
	}
	options.since, result = integerArgument(arguments, "since", false, 1, 0)
	if result != nil {
		return options, result
	}
	options.feedID, result = integerArgument(arguments, "feed_id", false, 1, 0)
	if result != nil {
		return options, result
	}
	options.categoryIDs, result = integerArrayArgument(arguments, "category_ids", maximumEntryLimit)
	if result != nil {
		return options, result
	}
	options.excludeCategoryIDs, result = integerArrayArgument(arguments, "exclude_category_ids", maximumEntryLimit)
	if result != nil {
		return options, result
	}

	return options, nil
}

func (s *MinifluxServer) GetUnreadDigest(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	options, result := parseUnreadDigestOptions(arguments)
	if result != nil {
		return result, nil
	}

	pageSize := options.limit
	filterCategories := len(options.categoryIDs) > 0 || len(options.excludeCategoryIDs) > 0

	digestCandidates := make(client.Entries, 0, options.limit)
	scanned := 0
	moreCandidates := false
	for len(digestCandidates) < options.limit && scanned < maximumDigestCandidates {
		remainingScan := maximumDigestCandidates - scanned
		requestLimit := min(pageSize, remainingScan)
		filter := &client.Filter{
			Status:         client.EntryStatusUnread,
			Limit:          requestLimit,
			Offset:         scanned,
			Order:          "published_at",
			Direction:      "asc",
			PublishedAfter: options.since,
			FeedID:         options.feedID,
		}
		entries, err := s.client.EntriesContext(ctx, filter)
		if err != nil || entries == nil {
			return toolErrorResult("failed to fetch unread digest")
		}
		pageCount := len(entries.Entries)
		if pageCount == 0 {
			break
		}
		scanned += pageCount
		moreCandidates = scanned < entries.Total

		for _, entry := range entries.Entries {
			if entry == nil || !digestCategoryAllowed(entry, options.categoryIDs, options.excludeCategoryIDs) {
				continue
			}
			digestCandidates = append(digestCandidates, entry)
		}
		if scanned >= entries.Total || pageCount < requestLimit {
			break
		}
		if !filterCategories {
			break
		}
		pageSize = min(pageSize*2, maximumEntryLimit)
	}
	scanTruncated := len(digestCandidates) < options.limit && scanned >= maximumDigestCandidates && moreCandidates
	sort.Slice(digestCandidates, func(i, j int) bool {
		if digestCandidates[i].Date.Equal(digestCandidates[j].Date) {
			return digestCandidates[i].ID < digestCandidates[j].ID
		}

		return digestCandidates[i].Date.Before(digestCandidates[j].Date)
	})
	if len(digestCandidates) > options.limit {
		digestCandidates = digestCandidates[:options.limit]
	}
	digestEntries := make([]MCPDigestEntry, 0, len(digestCandidates))
	ackEntryIDs := make([]int64, 0, len(digestCandidates))
	for _, entry := range digestCandidates {
		digestEntries = append(digestEntries, toMCPDigestEntry(entry))
		ackEntryIDs = append(ackEntryIDs, entry.ID)
	}

	return marshalToolResult(MCPUnreadDigest{
		Entries:       digestEntries,
		AckEntryIDs:   ackEntryIDs,
		ScanTruncated: scanTruncated,
	})
}

func digestCategoryAllowed(entry *client.Entry, included, excluded []int64) bool {
	var categoryID int64
	if entry.Feed != nil && entry.Feed.Category != nil {
		categoryID = entry.Feed.Category.ID
	}
	if len(included) > 0 && !slices.Contains(included, categoryID) {
		return false
	}

	return !slices.Contains(excluded, categoryID)
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
		return toolErrorResult("failed to fetch entry")
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
		return toolErrorResult("status must be read or unread")
	}
	if err := s.client.UpdateEntriesContext(ctx, []int64{entryID}, status); err != nil {
		return toolErrorResult("failed to update entry status")
	}

	return mcp.NewToolResultText(fmt.Sprintf("Entry %d status updated to: %s", entryID, status)), nil
}

func (s *MinifluxServer) UpdateEntriesStatus(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	arguments, result := argumentsMap(request)
	if result != nil {
		return result, nil
	}
	entryIDs, result := integerArrayArgument(arguments, "entry_ids", maximumEntryLimit)
	if result != nil {
		return result, nil
	}
	if len(entryIDs) == 0 {
		return toolErrorResult("entry_ids must contain at least one ID")
	}
	status, ok := arguments["status"].(string)
	if !ok || status != client.EntryStatusRead && status != client.EntryStatusUnread {
		return toolErrorResult("status must be read or unread")
	}
	if err := s.client.UpdateEntriesContext(ctx, entryIDs, status); err != nil {
		return toolErrorResult("failed to update entries status")
	}

	return marshalToolResult(MCPEntriesStatusUpdate{Updated: len(entryIDs), Status: status})
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
		return toolErrorResult("failed to refresh feed")
	}

	return mcp.NewToolResultText(fmt.Sprintf("Feed %d refreshed successfully", feedID)), nil
}

func (s *MinifluxServer) GetCategories(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	categories, err := s.client.CategoriesContext(ctx)
	if err != nil {
		return toolErrorResult("failed to fetch categories")
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
		return toolErrorResult("failed to fetch feed entries")
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
		return toolErrorResult("failed to fetch feed entry")
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
		return toolErrorResult("failed to fetch category feeds")
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
		return toolErrorResult("failed to fetch category entries")
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
		return toolErrorResult("failed to fetch category entry")
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
		return toolErrorResult("failed to toggle starred status")
	}

	return mcp.NewToolResultText(fmt.Sprintf("Starred status toggled for entry %d", entryID)), nil
}

func (s *MinifluxServer) GetVersion(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	version, err := s.client.VersionContext(ctx)
	if err != nil || version == nil {
		return toolErrorResult("failed to fetch version")
	}

	return marshalToolResult(MCPVersion{Version: version.Version})
}

func (s *MinifluxServer) Healthcheck(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.client.HealthcheckContext(ctx); err != nil {
		return toolErrorResult("healthcheck failed")
	}

	return mcp.NewToolResultText("Healthcheck passed"), nil
}

func (s *MinifluxServer) FetchCounters(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	counters, err := s.client.FetchCountersContext(ctx)
	if err != nil || counters == nil {
		return toolErrorResult("failed to fetch counters")
	}

	return marshalToolResult(toMCPFeedCounters(counters))
}
