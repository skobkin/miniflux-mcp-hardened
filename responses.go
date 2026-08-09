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
	CheckedAt         time.Time    `json:"checked_at"`
	NextCheckAt       time.Time    `json:"next_check_at"`
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
	CommentsURL string    `json:"comments_url"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

type MCPEntryResultSet struct {
	Total   int               `json:"total"`
	Entries []MCPEntrySummary `json:"entries"`
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
		CheckedAt:         feed.CheckedAt,
		NextCheckAt:       feed.NextCheckAt,
		Disabled:          feed.Disabled,
		ParsingErrorCount: feed.ParsingErrorCount,
		Category:          toMCPCategory(feed.Category),
	}
}

func toMCPFeeds(feeds client.Feeds) []*MCPFeed {
	result := make([]*MCPFeed, 0, len(feeds))
	for _, feed := range feeds {
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

func toMCPEntryDetail(entry *client.Entry) *MCPEntryDetail {
	if entry == nil {
		return nil
	}
	return &MCPEntryDetail{
		MCPEntrySummary: toMCPEntrySummary(entry),
		CommentsURL:     entry.CommentsURL,
		Content:         entry.Content,
		CreatedAt:       entry.CreatedAt,
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
		result.Entries = append(result.Entries, toMCPEntrySummary(entry))
	}
	return result
}

func toMCPCategories(categories client.Categories) []*MCPCategory {
	result := make([]*MCPCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, toMCPCategory(category))
	}
	return result
}
