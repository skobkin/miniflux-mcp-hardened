package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"miniflux.app/v2/client"
)

const sentinelSecret = "SENTINEL-SECRET-DO-NOT-EXPOSE"

type fakeMinifluxClient struct {
	feeds        client.Feeds
	entries      *client.EntryResultSet
	entry        *client.Entry
	feedsError   error
	entryError   error
	nilVersion   bool
	nilCounters  bool
	lastContext  context.Context
	entryFilters []*client.Filter
}

func (f *fakeMinifluxClient) HealthcheckContext(ctx context.Context) error {
	f.lastContext = ctx

	return nil
}

func (f *fakeMinifluxClient) VersionContext(ctx context.Context) (*client.VersionResponse, error) {
	f.lastContext = ctx
	if f.nilVersion {
		return nil, nil
	}

	return &client.VersionResponse{Version: "test"}, nil
}

func (f *fakeMinifluxClient) CategoriesContext(ctx context.Context) (client.Categories, error) {
	f.lastContext = ctx

	return nil, nil
}

func (f *fakeMinifluxClient) CategoryFeedsContext(ctx context.Context, _ int64) (client.Feeds, error) {
	f.lastContext = ctx

	return f.feeds, f.feedsError
}

func (f *fakeMinifluxClient) FeedsContext(ctx context.Context) (client.Feeds, error) {
	f.lastContext = ctx

	return f.feeds, f.feedsError
}

func (f *fakeMinifluxClient) FeedContext(ctx context.Context, _ int64) (*client.Feed, error) {
	f.lastContext = ctx
	if len(f.feeds) == 0 {
		return nil, f.feedsError
	}

	return f.feeds[0], f.feedsError
}

func (f *fakeMinifluxClient) RefreshFeedContext(ctx context.Context, _ int64) error {
	f.lastContext = ctx

	return nil
}

func (f *fakeMinifluxClient) FeedEntryContext(ctx context.Context, _, _ int64) (*client.Entry, error) {
	f.lastContext = ctx

	return f.entry, f.entryError
}

func (f *fakeMinifluxClient) CategoryEntryContext(ctx context.Context, _, _ int64) (*client.Entry, error) {
	f.lastContext = ctx

	return f.entry, f.entryError
}

func (f *fakeMinifluxClient) EntryContext(ctx context.Context, _ int64) (*client.Entry, error) {
	f.lastContext = ctx

	return f.entry, f.entryError
}

func (f *fakeMinifluxClient) EntriesContext(ctx context.Context, filter *client.Filter) (*client.EntryResultSet, error) {
	f.lastContext = ctx
	if filter != nil {
		filterCopy := *filter
		filterCopy.Statuses = append([]string(nil), filter.Statuses...)
		filterCopy.Tags = append([]string(nil), filter.Tags...)
		f.entryFilters = append(f.entryFilters, &filterCopy)
	}

	return f.entries, f.entryError
}

func (f *fakeMinifluxClient) FeedEntriesContext(ctx context.Context, _ int64, _ *client.Filter) (*client.EntryResultSet, error) {
	f.lastContext = ctx

	return f.entries, f.entryError
}

func (f *fakeMinifluxClient) CategoryEntriesContext(ctx context.Context, _ int64, _ *client.Filter) (*client.EntryResultSet, error) {
	f.lastContext = ctx

	return f.entries, f.entryError
}

func (f *fakeMinifluxClient) UpdateEntriesContext(ctx context.Context, _ []int64, _ string) error {
	f.lastContext = ctx

	return nil
}

func (f *fakeMinifluxClient) ToggleStarredContext(ctx context.Context, _ int64) error {
	f.lastContext = ctx

	return nil
}

func (f *fakeMinifluxClient) FetchCountersContext(ctx context.Context) (*client.FeedCounters, error) {
	f.lastContext = ctx
	if f.nilCounters {
		return nil, nil
	}

	return &client.FeedCounters{}, nil
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("nil tool result")
	}
	if len(result.Content) != 1 {
		t.Fatalf("result content length = %d, want 1", len(result.Content))
	}
	content, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		t.Fatalf("result content type = %T, want text", result.Content[0])
	}

	return content.Text
}

func TestGetFeedsSanitizesSensitiveFields(t *testing.T) {
	checkedAt := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	nextCheckAt := checkedAt.Add(time.Hour)
	feed := &client.Feed{
		ID:                 42,
		Title:              "Example",
		SiteURL:            "https://example.com/",
		FeedURL:            "https://token:" + sentinelSecret + "@example.com/feed",
		Username:           sentinelSecret,
		Password:           sentinelSecret,
		Cookie:             sentinelSecret,
		ProxyURL:           "https://" + sentinelSecret + "@proxy.example/",
		AppriseServiceURLs: sentinelSecret,
		WebhookURL:         sentinelSecret,
		EtagHeader:         sentinelSecret,
		ParsingErrorMsg:    sentinelSecret,
		CheckedAt:          checkedAt,
		NextCheckAt:        nextCheckAt,
		Category: &client.Category{
			ID:     7,
			Title:  "News",
			UserID: 99,
		},
	}
	fake := &fakeMinifluxClient{feeds: client.Feeds{feed}}
	server := &MinifluxServer{client: fake}

	result, err := server.GetFeeds(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("GetFeeds returned error: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, sentinelSecret) {
		t.Fatalf("serialized feed leaked sentinel secret: %s", text)
	}
	assertJSONKeys(t, text, []string{
		"category", "checked_at", "description", "disabled", "id", "language",
		"next_check_at", "parsing_error_count", "site_url", "title",
	})
	if fake.lastContext == nil {
		t.Fatal("request context was not passed to Miniflux client")
	}
}

func TestFeedOmitsUnknownPollingTimes(t *testing.T) {
	server := &MinifluxServer{client: &fakeMinifluxClient{feeds: client.Feeds{{ID: 42, Title: "Unchecked"}}}}

	result, err := server.GetFeeds(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("GetFeeds returned error: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "0001-01-01") {
		t.Fatalf("serialized feed contained a zero timestamp: %s", text)
	}

	var feeds []map[string]interface{}
	if err := json.Unmarshal([]byte(text), &feeds); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(feeds) != 1 {
		t.Fatalf("decoded object count = %d, want 1", len(feeds))
	}
	for _, field := range []string{"checked_at", "next_check_at"} {
		if _, exists := feeds[0][field]; exists {
			t.Errorf("serialized unchecked feed contained %q: %s", field, text)
		}
	}
}

func TestEntryListIsCompactAndNestedFeedIsSanitized(t *testing.T) {
	entry := secretEntry()
	fake := &fakeMinifluxClient{entries: &client.EntryResultSet{Total: 1, Entries: client.Entries{entry}}}
	server := &MinifluxServer{client: fake}

	result, err := server.GetEntries(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("GetEntries returned error: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, sentinelSecret) {
		t.Fatalf("serialized entry list leaked sentinel secret: %s", text)
	}
	for _, forbidden := range []string{"content", "comments_url", "created_at", "feed_url", "share_code", "user_id"} {
		if strings.Contains(text, `"`+forbidden+`"`) {
			t.Errorf("serialized entry list contained forbidden field %q: %s", forbidden, text)
		}
	}
}

func TestEntryDetailIncludesContentWithoutSensitiveBackendFields(t *testing.T) {
	entry := secretEntry()
	entry.Content = "trusted-for-test article body"
	fake := &fakeMinifluxClient{entry: entry}
	server := &MinifluxServer{client: fake}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{"entry_id": float64(1)}}}

	result, err := server.GetEntry(context.Background(), request)
	if err != nil {
		t.Fatalf("GetEntry returned error: %v", err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "trusted-for-test article body") {
		t.Fatalf("entry detail omitted article content: %s", text)
	}
	if strings.Contains(text, sentinelSecret) {
		t.Fatalf("serialized entry detail leaked sentinel secret: %s", text)
	}
	for _, forbidden := range []string{"feed_url", "share_code", "user_id", "hash", "enclosures"} {
		if strings.Contains(text, `"`+forbidden+`"`) {
			t.Errorf("serialized entry detail contained forbidden field %q: %s", forbidden, text)
		}
	}
	var detail MCPEntryDetail
	if err := json.Unmarshal([]byte(text), &detail); err != nil {
		t.Fatalf("decode entry detail: %v", err)
	}
	if detail.ContentOffset != 0 || detail.NextContentOffset != nil || detail.ContentTotalBytes != len(entry.Content) || !detail.ContentComplete {
		t.Fatalf("content paging metadata = %#v", detail)
	}
}

func TestEntryDetailPagesLargeMultibyteContent(t *testing.T) {
	content := strings.Repeat("<p>界 & quoted \"content\"</p>\n", 10000)
	entry := secretEntry()
	entry.Content = content
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}

	var reconstructed strings.Builder
	var offset int
	for call := 0; ; call++ {
		if call > 20 {
			t.Fatal("content paging did not complete")
		}
		request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
			"entry_id":       float64(entry.ID),
			"content_offset": float64(offset),
		}}}
		result, err := server.GetEntry(context.Background(), request)
		if err != nil || result.IsError {
			t.Fatalf("GetEntry(offset=%d) = %#v, %v", offset, result, err)
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatalf("encode tool result: %v", err)
		}
		if len(encoded) > maximumContentResultBytes {
			t.Fatalf("encoded result size = %d, want <= %d", len(encoded), maximumContentResultBytes)
		}

		var detail MCPEntryDetail
		if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
			t.Fatalf("decode entry detail: %v", err)
		}
		if detail.ContentOffset != offset || detail.ContentTotalBytes != len(content) {
			t.Fatalf("paging metadata at offset %d = %#v", offset, detail)
		}
		if !strings.Contains(resultText(t, result), "<p>") || strings.Contains(resultText(t, result), `\u003c`) {
			t.Fatal("entry content was unnecessarily HTML-escaped")
		}
		reconstructed.WriteString(detail.Content)
		if detail.ContentComplete {
			if detail.NextContentOffset != nil {
				t.Fatalf("complete response has next offset %d", *detail.NextContentOffset)
			}

			break
		}
		if detail.NextContentOffset == nil || *detail.NextContentOffset <= offset {
			t.Fatalf("next content offset = %v after %d", detail.NextContentOffset, offset)
		}
		offset = *detail.NextContentOffset
	}
	if reconstructed.String() != content {
		t.Fatal("paged content did not reconstruct the original article")
	}
}

func TestEntryDetailReturnsLargestUTF8ChunkWithinBudget(t *testing.T) {
	entry := secretEntry()
	entry.Content = strings.Repeat("界", maximumContentResultBytes)
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_id": float64(entry.ID),
	}}}

	result, err := server.GetEntry(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetEntry = %#v, %v", result, err)
	}
	var detail MCPEntryDetail
	if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
		t.Fatalf("decode entry detail: %v", err)
	}
	if detail.ContentComplete || detail.NextContentOffset == nil {
		t.Fatalf("paging metadata = %#v, want incomplete chunk", detail)
	}
	_, runeBytes := utf8.DecodeRuneInString(entry.Content[*detail.NextContentOffset:])
	tooLarge, err := marshalCompactToolResult(toMCPEntryDetail(entry, 0, *detail.NextContentOffset+runeBytes))
	if err != nil {
		t.Fatalf("encode next larger chunk: %v", err)
	}
	size, err := encodedToolResultSize(tooLarge)
	if err != nil {
		t.Fatalf("measure next larger chunk: %v", err)
	}
	if size <= maximumContentResultBytes {
		t.Fatalf("next UTF-8 chunk size = %d, want > %d", size, maximumContentResultBytes)
	}
}

func TestEntryDetailNormalizesInvalidUTF8BeforePaging(t *testing.T) {
	entry := secretEntry()
	entry.Content = "before\xffafter"
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_id": float64(entry.ID),
	}}}

	result, err := server.GetEntry(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetEntry = %#v, %v", result, err)
	}
	var detail MCPEntryDetail
	if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
		t.Fatalf("decode entry detail: %v", err)
	}
	want := "before\ufffdafter"
	if detail.Content != want || detail.ContentTotalBytes != len(want) || !detail.ContentComplete {
		t.Fatalf("detail = %#v, want normalized complete content", detail)
	}
}

func TestDetailHandlersHonorContentOffset(t *testing.T) {
	entry := secretEntry()
	entry.Content = "abc界def"
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	tests := []struct {
		name      string
		arguments map[string]interface{}
		call      func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	}{
		{
			name:      "entry",
			arguments: map[string]interface{}{"entry_id": float64(entry.ID), "content_offset": float64(3)},
			call:      server.GetEntry,
		},
		{
			name:      "feed entry",
			arguments: map[string]interface{}{"feed_id": float64(42), "entry_id": float64(entry.ID), "content_offset": float64(3)},
			call:      server.GetFeedEntry,
		},
		{
			name:      "category entry",
			arguments: map[string]interface{}{"category_id": float64(7), "entry_id": float64(entry.ID), "content_offset": float64(3)},
			call:      server.GetCategoryEntry,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: test.arguments}}
			result, err := test.call(context.Background(), request)
			if err != nil || result.IsError {
				t.Fatalf("detail call = %#v, %v", result, err)
			}
			var detail MCPEntryDetail
			if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
				t.Fatalf("decode entry detail: %v", err)
			}
			if detail.ContentOffset != 3 || detail.Content != "界def" || !detail.ContentComplete {
				t.Fatalf("detail = %#v", detail)
			}
		})
	}
}

func TestGetEntryValidatesContentOffset(t *testing.T) {
	entry := secretEntry()
	entry.Content = "a界"
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	tests := []struct {
		name   string
		offset interface{}
	}{
		{name: "negative", offset: float64(-1)},
		{name: "fractional", offset: 1.5},
		{name: "unsafe", offset: float64(1 << 53)},
		{name: "past end", offset: float64(len(entry.Content) + 1)},
		{name: "mid codepoint", offset: float64(2)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
				"entry_id":       float64(entry.ID),
				"content_offset": test.offset,
			}}}
			result, err := server.GetEntry(context.Background(), request)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("GetEntry = %#v, %v; want tool error", result, err)
			}
		})
	}
}

func TestGetEntryAcceptsEndContentOffset(t *testing.T) {
	entry := secretEntry()
	entry.Content = "finished"
	server := &MinifluxServer{client: &fakeMinifluxClient{entry: entry}}
	request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
		"entry_id":       float64(entry.ID),
		"content_offset": float64(len(entry.Content)),
	}}}

	result, err := server.GetEntry(context.Background(), request)
	if err != nil || result.IsError {
		t.Fatalf("GetEntry = %#v, %v", result, err)
	}
	var detail MCPEntryDetail
	if err := json.Unmarshal([]byte(resultText(t, result)), &detail); err != nil {
		t.Fatalf("decode entry detail: %v", err)
	}
	if detail.Content != "" || detail.ContentOffset != len(entry.Content) || detail.NextContentOffset != nil || !detail.ContentComplete {
		t.Fatalf("detail = %#v", detail)
	}
}

func TestBackendErrorsDoNotCrossMCPBoundary(t *testing.T) {
	fake := &fakeMinifluxClient{feedsError: errors.New("backend said " + sentinelSecret)}
	result, err := (&MinifluxServer{client: fake}).GetFeeds(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("GetFeeds returned error: %v", err)
	}
	if !result.IsError {
		t.Fatal("GetFeeds succeeded despite backend error")
	}
	if text := resultText(t, result); strings.Contains(text, sentinelSecret) {
		t.Fatalf("tool error leaked backend detail: %s", text)
	}
}

func TestNilBackendResponsesReturnToolErrors(t *testing.T) {
	tests := []struct {
		name   string
		server *MinifluxServer
		call   func(*MinifluxServer) (*mcp.CallToolResult, error)
	}{
		{
			name:   "version",
			server: &MinifluxServer{client: &fakeMinifluxClient{nilVersion: true}},
			call: func(server *MinifluxServer) (*mcp.CallToolResult, error) {
				return server.GetVersion(context.Background(), mcp.CallToolRequest{})
			},
		},
		{
			name:   "counters",
			server: &MinifluxServer{client: &fakeMinifluxClient{nilCounters: true}},
			call: func(server *MinifluxServer) (*mcp.CallToolResult, error) {
				return server.FetchCounters(context.Background(), mcp.CallToolRequest{})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.call(test.server)
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if result == nil || !result.IsError {
				t.Fatalf("result = %#v, want tool error", result)
			}
		})
	}
}

func TestFetchCountersUsesEmptyObjects(t *testing.T) {
	result, err := (&MinifluxServer{client: &fakeMinifluxClient{}}).FetchCounters(context.Background(), mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("FetchCounters returned error: %v", err)
	}
	text := resultText(t, result)
	if strings.Contains(text, "null") {
		t.Fatalf("serialized counters contained null: %s", text)
	}

	var counters map[string]map[string]int
	if err := json.Unmarshal([]byte(text), &counters); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, field := range []string{"reads", "unreads"} {
		if counters[field] == nil {
			t.Errorf("serialized counters omitted empty object %q: %s", field, text)
		}
	}
}

func TestCollectionConvertersSkipNilItems(t *testing.T) {
	feeds := toMCPFeeds(client.Feeds{nil, {ID: 42, Title: "Feed"}})
	if len(feeds) != 1 || feeds[0].ID != 42 {
		t.Fatalf("converted feeds = %#v, want one non-nil feed", feeds)
	}

	categories := toMCPCategories(client.Categories{nil, {ID: 7, Title: "Category"}})
	if len(categories) != 1 || categories[0].ID != 7 {
		t.Fatalf("converted categories = %#v, want one non-nil category", categories)
	}

	entries := toMCPEntryResultSet(&client.EntryResultSet{
		Total:   2,
		Entries: client.Entries{nil, {ID: 1, Title: "Entry"}},
	})
	if entries.Total != 2 {
		t.Errorf("converted total = %d, want backend total 2", entries.Total)
	}
	if len(entries.Entries) != 1 || entries.Entries[0].ID != 1 {
		t.Fatalf("converted entries = %#v, want one non-nil entry", entries.Entries)
	}
}

func secretEntry() *client.Entry {
	return &client.Entry{
		ID:        1,
		Title:     "Article",
		URL:       "https://example.com/article",
		Content:   sentinelSecret,
		ShareCode: sentinelSecret,
		Hash:      sentinelSecret,
		UserID:    99,
		Feed: &client.Feed{
			ID:       42,
			Title:    "Example",
			FeedURL:  "https://example.com/private?token=" + sentinelSecret,
			Username: sentinelSecret,
			Password: sentinelSecret,
			Cookie:   sentinelSecret,
			ProxyURL: sentinelSecret,
			Category: &client.Category{ID: 7, Title: "News", UserID: 99},
		},
		Enclosures: client.Enclosures{{URL: "https://example.com/media?token=" + sentinelSecret}},
	}
}

func assertJSONKeys(t *testing.T, value string, expected []string) {
	t.Helper()
	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(value), &objects); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(objects) != 1 {
		t.Fatalf("decoded object count = %d, want 1", len(objects))
	}
	actual := make([]string, 0, len(objects[0]))
	for key := range objects[0] {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON keys = %v, want %v", actual, expected)
	}
}
