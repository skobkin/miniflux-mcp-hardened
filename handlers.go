package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

const (
	defaultEntryLimit           = 50
	maximumEntryLimit           = 100
	defaultDigestEntryLimit     = 10
	maximumDigestEntryLimit     = 20
	maximumDigestExcerptBytes   = 8 * 1024
	maximumContentResultBytes   = 96 * 1024
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
		return nil, mcp.NewToolResultError("arguments must be a JSON object")
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
		if math.Trunc(number) != number {
			return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be an integer", name))
		}
		if number > maximumSafeJSONInteger || number < -maximumSafeJSONInteger {
			return 0, mcp.NewToolResultError(fmt.Sprintf("%s must be a safely representable JSON integer (absolute value must not exceed %d)", name, maximumSafeJSONInteger))
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
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s must be an array of integers", name))
	}
	if len(items) > maximumItems {
		return nil, mcp.NewToolResultError(fmt.Sprintf("%s must contain at most %d items", name, maximumItems))
	}

	result := make([]int64, 0, len(items))
	seen := make(map[int64]int, len(items))
	for index, item := range items {
		path := fmt.Sprintf("%s[%d]", name, index)
		id, validationResult := integerValue(item, path, 1, 0)
		if validationResult != nil {
			return nil, validationResult
		}
		if firstIndex, duplicate := seen[id]; duplicate {
			return nil, mcp.NewToolResultError(fmt.Sprintf("%s duplicates %s[%d]; IDs must be unique", path, name, firstIndex))
		}
		seen[id] = index
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

func enumStringArgument(arguments map[string]interface{}, name string, required bool, allowed ...string) (string, *mcp.CallToolResult) {
	value, exists := arguments[name]
	if !exists {
		if required {
			return "", mcp.NewToolResultError(fmt.Sprintf("%s is required", name))
		}

		return "", nil
	}

	return enumStringValue(value, name, allowed...)
}

func enumStringValue(value interface{}, name string, allowed ...string) (string, *mcp.CallToolResult) {
	parsed, ok := value.(string)
	if !ok {
		return "", mcp.NewToolResultError(fmt.Sprintf("%s must be a string", name))
	}
	if !slices.Contains(allowed, parsed) {
		return "", mcp.NewToolResultError(fmt.Sprintf("%s must be one of: %s", name, strings.Join(allowed, ", ")))
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

func marshalCompactToolResult(value interface{}) (*mcp.CallToolResult, error) {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return toolErrorResult("failed to encode response")
	}
	encoded := bytes.TrimSuffix(data.Bytes(), []byte{'\n'})

	return mcp.NewToolResultText(string(encoded)), nil
}

func encodedToolResultSize(result *mcp.CallToolResult) (int, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return 0, err
	}

	return len(data), nil
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
			return nil, mcp.NewToolResultError("statuses must be an array of strings")
		}
		for index, rawStatus := range statuses {
			status, result := enumStringValue(rawStatus, fmt.Sprintf("statuses[%d]", index), "read", "unread", "removed")
			if result != nil {
				return nil, result
			}
			filter.Statuses = append(filter.Statuses, status)
		}
	}
	if rawStatus, exists := arguments["status"]; exists && len(filter.Statuses) == 0 {
		status, result := enumStringValue(rawStatus, "status", "read", "unread", "removed")
		if result != nil {
			return nil, result
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
		order, result := enumStringValue(value, "order", "id", "status", "changed_at", "published_at", "created_at", "category_title", "category_id", "title", "author")
		if result != nil {
			return nil, result
		}
		filter.Order = order
	}
	if value, exists := arguments["direction"]; exists {
		direction, result := enumStringValue(value, "direction", "asc", "desc")
		if result != nil {
			return nil, result
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
	options := unreadDigestOptions{limit: defaultDigestEntryLimit}

	limit, result := integerArgument(arguments, "limit", false, 1, maximumDigestEntryLimit)
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

	digestCandidates := make(client.Entries, 0, maximumEntryLimit)
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

	return boundedUnreadDigestResult(digestCandidates, scanTruncated)
}

func boundedUnreadDigestResult(entries client.Entries, scanTruncated bool) (*mcp.CallToolResult, error) {
	included := len(entries)
	responseSizeLimited := false
	var baseResult *mcp.CallToolResult
	for {
		digest := buildUnreadDigest(entries[:included], 0, scanTruncated, responseSizeLimited)
		result, err := marshalCompactToolResult(digest)
		if err != nil {
			return result, err
		}
		size, err := encodedToolResultSize(result)
		if err != nil {
			return toolErrorResult("failed to encode response")
		}
		if size <= maximumContentResultBytes {
			baseResult = result

			break
		}
		if included == 0 {
			return toolErrorResult("digest metadata exceeds response size limit")
		}
		included--
		responseSizeLimited = true
	}

	if included == 0 {
		if len(entries) > 0 {
			return toolErrorResult("digest metadata exceeds response size limit")
		}

		return baseResult, nil
	}

	bestResult := baseResult
	low, high := 1, maximumDigestExcerptBytes
	for low <= high {
		candidateLimit := low + (high-low)/2
		digest := buildUnreadDigest(entries[:included], candidateLimit, scanTruncated, responseSizeLimited)
		result, err := marshalCompactToolResult(digest)
		if err != nil {
			return result, err
		}
		size, err := encodedToolResultSize(result)
		if err != nil {
			return toolErrorResult("failed to encode response")
		}
		if size <= maximumContentResultBytes {
			bestResult = result
			low = candidateLimit + 1
		} else {
			high = candidateLimit - 1
		}
	}

	return bestResult, nil
}

func buildUnreadDigest(entries client.Entries, excerptLimit int, scanTruncated, responseSizeLimited bool) MCPUnreadDigest {
	digestEntries := make([]MCPDigestEntry, 0, len(entries))
	ackEntryIDs := make([]int64, 0, len(entries))
	for _, entry := range entries {
		entry = entryWithValidUTF8Content(entry)
		excerpt := utf8Prefix(entry.Content, excerptLimit)
		digestEntries = append(digestEntries, toMCPDigestEntry(entry, excerpt))
		ackEntryIDs = append(ackEntryIDs, entry.ID)
	}

	return MCPUnreadDigest{
		Entries:             digestEntries,
		AckEntryIDs:         ackEntryIDs,
		ScanTruncated:       scanTruncated,
		ResponseSizeLimited: responseSizeLimited,
	}
}

func utf8Prefix(value string, maximumBytes int) string {
	value = validUTF8(value)
	if maximumBytes >= len(value) {
		return value
	}
	if maximumBytes <= 0 {
		return ""
	}
	for maximumBytes > 0 && !utf8.RuneStart(value[maximumBytes]) {
		maximumBytes--
	}

	return value[:maximumBytes]
}

func validUTF8(value string) string {
	if utf8.ValidString(value) {
		return value
	}

	return strings.ToValidUTF8(value, string(utf8.RuneError))
}

func entryWithValidUTF8Content(entry *client.Entry) *client.Entry {
	if entry == nil || utf8.ValidString(entry.Content) {
		return entry
	}
	copy := *entry
	copy.Content = validUTF8(entry.Content)

	return &copy
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
	contentOffset, result := integerArgument(arguments, "content_offset", false, 0, maximumSafeJSONInteger)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.EntryContext(ctx, entryID)
	if err != nil || entry == nil {
		return toolErrorResult("failed to fetch entry")
	}

	return boundedEntryDetailResult(entry, contentOffset)
}

func boundedEntryDetailResult(entry *client.Entry, contentOffset int64) (*mcp.CallToolResult, error) {
	entry = entryWithValidUTF8Content(entry)
	if contentOffset > int64(len(entry.Content)) {
		return toolErrorResult("content_offset exceeds content length; use 0 or next_content_offset from the previous response")
	}
	offset := int(contentOffset)
	if offset < len(entry.Content) && !utf8.RuneStart(entry.Content[offset]) {
		return toolErrorResult("content_offset must be 0 or a next_content_offset returned by the previous response")
	}

	fullResult, err := marshalCompactToolResult(toMCPEntryDetail(entry, offset, len(entry.Content)))
	if err != nil {
		return fullResult, err
	}
	fullSize, err := encodedToolResultSize(fullResult)
	if err != nil {
		return toolErrorResult("failed to encode response")
	}
	if fullSize <= maximumContentResultBytes {
		return fullResult, nil
	}

	_, firstRuneBytes := utf8.DecodeRuneInString(entry.Content[offset:])
	firstEnd := offset + firstRuneBytes
	minimumResult, err := marshalCompactToolResult(toMCPEntryDetail(entry, offset, firstEnd))
	if err != nil {
		return minimumResult, err
	}
	minimumSize, err := encodedToolResultSize(minimumResult)
	if err != nil {
		return toolErrorResult("failed to encode response")
	}
	if minimumSize > maximumContentResultBytes {
		return toolErrorResult("entry metadata leaves no room for content")
	}

	bestResult := minimumResult
	bestEnd := firstEnd
	low, high := firstEnd+1, len(entry.Content)-1
	for low <= high {
		midpoint := low + (high-low)/2
		candidateEnd := midpoint
		for candidateEnd > offset && candidateEnd < len(entry.Content) && !utf8.RuneStart(entry.Content[candidateEnd]) {
			candidateEnd--
		}
		if candidateEnd <= bestEnd {
			low = midpoint + 1

			continue
		}
		result, err := marshalCompactToolResult(toMCPEntryDetail(entry, offset, candidateEnd))
		if err != nil {
			return result, err
		}
		size, err := encodedToolResultSize(result)
		if err != nil {
			return toolErrorResult("failed to encode response")
		}
		if size <= maximumContentResultBytes {
			bestResult = result
			bestEnd = candidateEnd
			low = midpoint + 1
		} else {
			high = candidateEnd - 1
		}
	}

	return bestResult, nil
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
	status, result := enumStringArgument(arguments, "status", true, "read", "unread")
	if result != nil {
		return result, nil
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
	status, result := enumStringArgument(arguments, "status", true, client.EntryStatusRead, client.EntryStatusUnread)
	if result != nil {
		return result, nil
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
		status, result := enumStringValue(rawStatus, "status", "read", "unread", "removed")
		if result != nil {
			return nil, result
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
	contentOffset, result := integerArgument(arguments, "content_offset", false, 0, maximumSafeJSONInteger)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.FeedEntryContext(ctx, feedID, entryID)
	if err != nil || entry == nil {
		return toolErrorResult("failed to fetch feed entry")
	}

	return boundedEntryDetailResult(entry, contentOffset)
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
	contentOffset, result := integerArgument(arguments, "content_offset", false, 0, maximumSafeJSONInteger)
	if result != nil {
		return result, nil
	}
	entry, err := s.client.CategoryEntryContext(ctx, categoryID, entryID)
	if err != nil || entry == nil {
		return toolErrorResult("failed to fetch category entry")
	}

	return boundedEntryDetailResult(entry, contentOffset)
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
