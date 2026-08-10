package main

import (
	"time"

	"miniflux.app/v2/client"
)

type MCPCategory struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	HideGlobally bool   `json:"hide_globally"`
	FeedCount    *int   `json:"feed_count,omitempty"`
	TotalUnread  *int   `json:"total_unread,omitempty"`
}

type MCPCategoryIdentity struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type MCPFeed struct {
	ID                int64        `json:"id"`
	Title             string       `json:"title"`
	SiteURL           string       `json:"site_url"`
	Description       string       `json:"description"`
	Language          string       `json:"language"`
	CheckedAt         *time.Time   `json:"checked_at,omitempty"`
	NextCheckAt       *time.Time   `json:"next_check_at,omitempty"`
	Disabled          bool         `json:"disabled"`
	ParsingErrorCount int          `json:"parsing_error_count"`
	Category          *MCPCategory `json:"category,omitempty"`
}

type MCPFeedIdentity struct {
	ID       int64                `json:"id"`
	Title    string               `json:"title"`
	Category *MCPCategoryIdentity `json:"category,omitempty"`
}

type MCPEntrySummary struct {
	ID          int64            `json:"id"`
	Title       string           `json:"title"`
	URL         string           `json:"url"`
	Author      string           `json:"author"`
	Language    string           `json:"language"`
	PublishedAt time.Time        `json:"published_at"`
	ChangedAt   time.Time        `json:"changed_at"`
	Status      string           `json:"status"`
	Starred     bool             `json:"starred"`
	ReadingTime int              `json:"reading_time"`
	Tags        []string         `json:"tags"`
	Feed        *MCPFeedIdentity `json:"feed,omitempty"`
}

type MCPEntryDetail struct {
	MCPEntrySummary
	CommentsURL       string    `json:"comments_url"`
	Content           string    `json:"content"`
	CreatedAt         time.Time `json:"created_at"`
	ContentOffset     int       `json:"content_offset"`
	NextContentOffset *int      `json:"next_content_offset,omitempty"`
	ContentTotalBytes int       `json:"content_total_bytes"`
	ContentComplete   bool      `json:"content_complete"`
}

type MCPEntryResultSet struct {
	Total   int               `json:"total"`
	Entries []MCPEntrySummary `json:"entries"`
}

type MCPDigestEntry struct {
	MCPEntrySummary
	ContentExcerpt   string `json:"content_excerpt"`
	ContentTruncated bool   `json:"content_truncated"`
}

type MCPUnreadDigest struct {
	Entries             []MCPDigestEntry `json:"entries"`
	AckEntryIDs         []int64          `json:"ack_entry_ids"`
	ScanTruncated       bool             `json:"scan_truncated"`
	ResponseSizeLimited bool             `json:"response_size_limited"`
}

type MCPEntriesStatusUpdate struct {
	Updated int    `json:"updated"`
	Status  string `json:"status"`
}

type MCPVersion struct {
	Version string `json:"version"`
}

type MCPFeedCounters struct {
	ReadCounters   map[int64]int `json:"reads"`
	UnreadCounters map[int64]int `json:"unreads"`
}

func toMCPCategory(category *client.Category) *MCPCategory {
	if category == nil {
		return nil
	}

	return &MCPCategory{
		ID:           category.ID,
		Title:        category.Title,
		HideGlobally: category.HideGlobally,
		FeedCount:    category.FeedCount,
		TotalUnread:  category.TotalUnread,
	}
}

func toMCPCategoryIdentity(category *client.Category) *MCPCategoryIdentity {
	if category == nil {
		return nil
	}

	return &MCPCategoryIdentity{ID: category.ID, Title: category.Title}
}

func toMCPFeed(feed *client.Feed) *MCPFeed {
	if feed == nil {
		return nil
	}

	return &MCPFeed{
		ID:                feed.ID,
		Title:             feed.Title,
		SiteURL:           feed.SiteURL,
		Description:       feed.Description,
		Language:          feed.Language,
		CheckedAt:         nonZeroTime(feed.CheckedAt),
		NextCheckAt:       nonZeroTime(feed.NextCheckAt),
		Disabled:          feed.Disabled,
		ParsingErrorCount: feed.ParsingErrorCount,
		Category:          toMCPCategory(feed.Category),
	}
}

func nonZeroTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}

	return &value
}

func toMCPFeeds(feeds client.Feeds) []*MCPFeed {
	result := make([]*MCPFeed, 0, len(feeds))
	for _, feed := range feeds {
		if feed == nil {
			continue
		}
		result = append(result, toMCPFeed(feed))
	}

	return result
}

func toMCPFeedIdentity(feed *client.Feed) *MCPFeedIdentity {
	if feed == nil {
		return nil
	}

	return &MCPFeedIdentity{
		ID:       feed.ID,
		Title:    feed.Title,
		Category: toMCPCategoryIdentity(feed.Category),
	}
}

func toMCPEntrySummary(entry *client.Entry) MCPEntrySummary {
	if entry == nil {
		return MCPEntrySummary{}
	}

	return MCPEntrySummary{
		ID:          entry.ID,
		Title:       entry.Title,
		URL:         entry.URL,
		Author:      entry.Author,
		Language:    entry.Language,
		PublishedAt: entry.Date,
		ChangedAt:   entry.ChangedAt,
		Status:      entry.Status,
		Starred:     entry.Starred,
		ReadingTime: entry.ReadingTime,
		Tags:        entry.Tags,
		Feed:        toMCPFeedIdentity(entry.Feed),
	}
}

func toMCPEntryDetail(entry *client.Entry, contentOffset, contentEnd int) *MCPEntryDetail {
	if entry == nil {
		return nil
	}
	var nextContentOffset *int
	if contentEnd < len(entry.Content) {
		next := contentEnd
		nextContentOffset = &next
	}

	return &MCPEntryDetail{
		MCPEntrySummary:   toMCPEntrySummary(entry),
		CommentsURL:       entry.CommentsURL,
		Content:           entry.Content[contentOffset:contentEnd],
		CreatedAt:         entry.CreatedAt,
		ContentOffset:     contentOffset,
		NextContentOffset: nextContentOffset,
		ContentTotalBytes: len(entry.Content),
		ContentComplete:   contentEnd == len(entry.Content),
	}
}

func toMCPDigestEntry(entry *client.Entry, excerpt string) MCPDigestEntry {
	if entry == nil {
		return MCPDigestEntry{}
	}

	return MCPDigestEntry{
		MCPEntrySummary:  toMCPEntrySummary(entry),
		ContentExcerpt:   excerpt,
		ContentTruncated: len(excerpt) < len(entry.Content),
	}
}

func toMCPEntryResultSet(entries *client.EntryResultSet) *MCPEntryResultSet {
	if entries == nil {
		return &MCPEntryResultSet{Entries: []MCPEntrySummary{}}
	}
	result := &MCPEntryResultSet{
		Total:   entries.Total,
		Entries: make([]MCPEntrySummary, 0, len(entries.Entries)),
	}
	for _, entry := range entries.Entries {
		if entry == nil {
			continue
		}
		result.Entries = append(result.Entries, toMCPEntrySummary(entry))
	}

	return result
}

func toMCPCategories(categories client.Categories) []*MCPCategory {
	result := make([]*MCPCategory, 0, len(categories))
	for _, category := range categories {
		if category == nil {
			continue
		}
		result = append(result, toMCPCategory(category))
	}

	return result
}

func toMCPFeedCounters(counters *client.FeedCounters) *MCPFeedCounters {
	if counters == nil {
		return nil
	}
	reads := counters.ReadCounters
	if reads == nil {
		reads = map[int64]int{}
	}
	unreads := counters.UnreadCounters
	if unreads == nil {
		unreads = map[int64]int{}
	}

	return &MCPFeedCounters{
		ReadCounters:   reads,
		UnreadCounters: unreads,
	}
}
