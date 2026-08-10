package main

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

func TestParseUnreadDigestOptions(t *testing.T) {
	options, result := parseUnreadDigestOptions(map[string]interface{}{
		"limit":                float64(12),
		"since":                float64(1700000000),
		"feed_id":              float64(42),
		"category_ids":         []interface{}{float64(1), float64(2)},
		"exclude_category_ids": []interface{}{float64(2)},
	})
	if result != nil {
		t.Fatalf("parseUnreadDigestOptions returned tool error: %#v", result.Content)
	}
	if options.limit != 12 || options.since != 1700000000 || options.feedID != 42 {
		t.Fatalf("options = %#v", options)
	}
	if !digestCategoryAllowed(&client.Entry{Feed: &client.Feed{Category: &client.Category{ID: 1}}}, options.categoryIDs, options.excludeCategoryIDs) {
		t.Fatal("included category was rejected")
	}
	if digestCategoryAllowed(&client.Entry{Feed: &client.Feed{Category: &client.Category{ID: 2}}}, options.categoryIDs, options.excludeCategoryIDs) {
		t.Fatal("excluded category was accepted")
	}
}

func TestParseUnreadDigestOptionsDefaults(t *testing.T) {
	options, result := parseUnreadDigestOptions(map[string]interface{}{})
	if result != nil {
		t.Fatalf("parseUnreadDigestOptions returned tool error: %#v", result.Content)
	}
	if options.limit != defaultEntryLimit || options.since != 0 || options.feedID != 0 {
		t.Fatalf("default options = %#v", options)
	}
}

func TestParseUnreadDigestOptionsValidation(t *testing.T) {
	tests := []map[string]interface{}{
		{"limit": float64(0)},
		{"limit": float64(maximumEntryLimit + 1)},
		{"since": float64(1 << 53)},
		{"feed_id": float64(1.5)},
		{"category_ids": "1"},
		{"category_ids": []interface{}{float64(1), float64(1)}},
		{"category_ids": []interface{}{float64(0)}},
	}
	tooMany := make([]interface{}, maximumEntryLimit+1)
	for i := range tooMany {
		tooMany[i] = float64(i + 1)
	}
	tests = append(tests, map[string]interface{}{"exclude_category_ids": tooMany})

	for _, arguments := range tests {
		if _, result := parseUnreadDigestOptions(arguments); result == nil || !result.IsError {
			t.Errorf("parseUnreadDigestOptions(%v) succeeded", arguments)
		}
	}
}

func TestGetUnreadDigestBuildsBoundedOrderedResponse(t *testing.T) {
	base := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	entries := &client.EntryResultSet{Total: 3, Entries: client.Entries{
		{ID: 3, Date: base.Add(time.Minute), Status: client.EntryStatusUnread, Content: "third", Feed: &client.Feed{ID: 30, Title: "Thirty", Category: &client.Category{ID: 3, Title: "Three"}}},
		{ID: 2, Date: base, Status: client.EntryStatusUnread, Content: "second", Feed: &client.Feed{ID: 20, Title: "Twenty", Category: &client.Category{ID: 2, Title: "Two"}}},
		{ID: 1, Date: base, Status: client.EntryStatusUnread, Content: "first", Feed: &client.Feed{ID: 10, Title: "Ten", Category: &client.Category{ID: 1, Title: "One"}}},
	}}
	fake := &fakeMinifluxClient{entries: entries}
	server := &MinifluxServer{client: fake}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"limit":                float64(2),
		"since":                float64(1700000000),
		"feed_id":              float64(42),
		"exclude_category_ids": []interface{}{float64(2)},
	}}}

	result, err := server.GetUnreadDigest(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	var digest MCPUnreadDigest
	if err := json.Unmarshal([]byte(resultText(t, result)), &digest); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if len(digest.Entries) != 2 || digest.Entries[0].ID != 1 || digest.Entries[1].ID != 3 {
		t.Fatalf("digest entries = %#v", digest.Entries)
	}
	if len(digest.AckEntryIDs) != 2 || digest.AckEntryIDs[0] != 1 || digest.AckEntryIDs[1] != 3 {
		t.Fatalf("ack_entry_ids = %v", digest.AckEntryIDs)
	}
	if len(fake.entryFilters) != 1 {
		t.Fatalf("Miniflux calls = %d, want 1", len(fake.entryFilters))
	}
	filter := fake.entryFilters[0]
	if filter.Status != client.EntryStatusUnread || filter.Limit != 2 || filter.Offset != 0 || filter.Order != "published_at" || filter.Direction != "asc" || filter.PublishedAfter != 1700000000 || filter.FeedID != 42 {
		t.Fatalf("Miniflux filter = %#v", filter)
	}
}

type pagedDigestClient struct {
	*fakeMinifluxClient
	pages []*client.EntryResultSet
	next  int
}

func (f *pagedDigestClient) EntriesContext(ctx context.Context, filter *client.Filter) (*client.EntryResultSet, error) {
	f.lastContext = ctx
	filterCopy := *filter
	f.entryFilters = append(f.entryFilters, &filterCopy)
	if f.next >= len(f.pages) {
		return &client.EntryResultSet{}, nil
	}
	page := f.pages[f.next]
	f.next++

	return page, nil
}

func TestGetUnreadDigestScalesCategoryPageSize(t *testing.T) {
	pages := []*client.EntryResultSet{
		{Total: 3, Entries: client.Entries{
			{ID: 1, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
		}},
		{Total: 3, Entries: client.Entries{
			{ID: 2, Feed: &client.Feed{Category: &client.Category{ID: 1}}},
			{ID: 3, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
		}},
	}
	fake := &pagedDigestClient{fakeMinifluxClient: &fakeMinifluxClient{}, pages: pages}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"limit":        float64(1),
		"category_ids": []interface{}{float64(1)},
	}}}

	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	if len(fake.entryFilters) != 2 {
		t.Fatalf("Miniflux calls = %d, want 2", len(fake.entryFilters))
	}
	if fake.entryFilters[0].Limit != 1 || fake.entryFilters[1].Limit != 2 {
		t.Fatalf("Miniflux page limits = [%d %d], want [1 2]", fake.entryFilters[0].Limit, fake.entryFilters[1].Limit)
	}
	if fake.entryFilters[0].Offset != 0 || fake.entryFilters[1].Offset != 1 {
		t.Fatalf("Miniflux page offsets = [%d %d], want [0 1]", fake.entryFilters[0].Offset, fake.entryFilters[1].Offset)
	}
}

func TestGetUnreadDigestSortsAcrossCategoryPages(t *testing.T) {
	publishedAt := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	pages := []*client.EntryResultSet{
		{Total: 6, Entries: client.Entries{
			{ID: 100, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
			{ID: 20, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 1}}},
		}},
		{Total: 6, Entries: client.Entries{
			{ID: 101, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
			{ID: 102, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
			{ID: 103, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 2}}},
			{ID: 10, Date: publishedAt, Feed: &client.Feed{Category: &client.Category{ID: 1}}},
		}},
	}
	fake := &pagedDigestClient{fakeMinifluxClient: &fakeMinifluxClient{}, pages: pages}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"limit":        float64(2),
		"category_ids": []interface{}{float64(1)},
	}}}

	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	var digest MCPUnreadDigest
	if err := json.Unmarshal([]byte(resultText(t, result)), &digest); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if !reflect.DeepEqual(digest.AckEntryIDs, []int64{10, 20}) {
		t.Fatalf("ack_entry_ids = %v, want [10 20]", digest.AckEntryIDs)
	}
	if len(digest.Entries) != 2 || digest.Entries[0].ID != 10 || digest.Entries[1].ID != 20 {
		t.Fatalf("digest entries = %#v", digest.Entries)
	}
}

func TestGetUnreadDigestSanitizesNestedFeed(t *testing.T) {
	entry := secretEntry()
	entry.Content = "article content"
	fake := &fakeMinifluxClient{entries: &client.EntryResultSet{Total: 1, Entries: client.Entries{entry}}}
	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), mcp.CallToolRequest{})
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "article content") {
		t.Fatalf("digest omitted content: %s", text)
	}
	if strings.Contains(text, sentinelSecret) {
		t.Fatalf("digest leaked backend secret: %s", text)
	}
}

func TestGetUnreadDigestReportsScanCap(t *testing.T) {
	entries := make(client.Entries, maximumEntryLimit)
	for i := range entries {
		entries[i] = &client.Entry{ID: int64(i + 1), Feed: &client.Feed{Category: &client.Category{ID: 2}}}
	}
	fake := &fakeMinifluxClient{entries: &client.EntryResultSet{Total: maximumDigestCandidates + 1, Entries: entries}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"category_ids": []interface{}{float64(1)},
	}}}
	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	var digest MCPUnreadDigest
	if err := json.Unmarshal([]byte(resultText(t, result)), &digest); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if !digest.ScanTruncated {
		t.Fatal("scan_truncated = false, want true")
	}
}

func TestGetUnreadDigestDoesNotReportExhaustedScanAsTruncated(t *testing.T) {
	entries := make(client.Entries, maximumEntryLimit)
	for i := range entries {
		entries[i] = &client.Entry{ID: int64(i + 1), Feed: &client.Feed{Category: &client.Category{ID: 2}}}
	}
	fake := &fakeMinifluxClient{entries: &client.EntryResultSet{Total: maximumDigestCandidates, Entries: entries}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"category_ids": []interface{}{float64(1)},
	}}}
	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v", result, err)
	}
	var digest MCPUnreadDigest
	if err := json.Unmarshal([]byte(resultText(t, result)), &digest); err != nil {
		t.Fatalf("decode digest: %v", err)
	}
	if digest.ScanTruncated {
		t.Fatal("scan_truncated = true for exhausted candidate set")
	}
}

func TestGetUnreadDigestHidesBackendErrors(t *testing.T) {
	fake := &fakeMinifluxClient{entryError: context.Canceled}
	result, err := (&MinifluxServer{client: fake}).GetUnreadDigest(context.Background(), mcp.CallToolRequest{})
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("GetUnreadDigest = %#v, %v; want tool error", result, err)
	}
	if strings.Contains(resultText(t, result), context.Canceled.Error()) {
		t.Fatalf("digest error leaked backend detail: %s", resultText(t, result))
	}
}
