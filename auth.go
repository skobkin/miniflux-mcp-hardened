package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"miniflux.app/v2/client"
)

const (
	minifluxTokenHeader       = "X-Miniflux-Token" // #nosec G101 -- public HTTP header name, not a credential value
	minifluxNativeTokenHeader = "X-Auth-Token"     // #nosec G101 -- public HTTP header name, not a credential value
)

type minifluxTokenContextKey struct{}
type minifluxClientContextKey struct{}

func validCredentialValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < ' ' || value[index] > '~' {
			return false
		}
	}

	return true
}

func enforceMinifluxCredentialHeaders(credentialSource string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values(minifluxNativeTokenHeader)) != 0 {
			http.Error(w, minifluxNativeTokenHeader+" is not accepted", http.StatusBadRequest)

			return
		}

		ctx := r.Context()
		switch credentialSource {
		case minifluxCredentialSourceConfig:
			if len(r.Header.Values(minifluxTokenHeader)) != 0 {
				http.Error(w, minifluxTokenHeader+" is not accepted when MINIFLUX_CREDENTIAL_SOURCE=config", http.StatusBadRequest)

				return
			}
		case minifluxCredentialSourceHeader:
			values := r.Header.Values(minifluxTokenHeader)
			if len(values) != 1 || !validCredentialValue(values[0]) {
				http.Error(w, "exactly one non-empty "+minifluxTokenHeader+" header is required", http.StatusBadRequest)

				return
			}
			ctx = context.WithValue(ctx, minifluxTokenContextKey{}, values[0])
		default:
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

			return
		}

		request := r.Clone(ctx)
		// Keep all credential headers out of MCP processing, including the
		// native header currently rejected above, if that policy ever changes.
		for _, name := range []string{"Authorization", minifluxTokenHeader, minifluxNativeTokenHeader} {
			request.Header.Del(name)
		}
		next.ServeHTTP(w, request)
	})
}

func newRequestMinifluxClientMiddleware(baseURL string, httpClient *http.Client) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			token, ok := ctx.Value(minifluxTokenContextKey{}).(string)
			if !ok || token == "" {
				return toolErrorResult("Miniflux authentication failed")
			}

			miniflux := newMinifluxAPIKeyClient(baseURL, token, httpClient)
			ctx = context.WithValue(ctx, minifluxClientContextKey{}, minifluxClient(miniflux))

			return next(ctx, request)
		}
	}
}

type requestScopedMinifluxClient struct{}

func minifluxClientFromContext(ctx context.Context) (minifluxClient, error) {
	miniflux, ok := ctx.Value(minifluxClientContextKey{}).(minifluxClient)
	if !ok || miniflux == nil {
		return nil, client.ErrNotAuthorized
	}

	return miniflux, nil
}

func (requestScopedMinifluxClient) HealthcheckContext(ctx context.Context) error {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return err
	}

	return miniflux.HealthcheckContext(ctx)
}

func (requestScopedMinifluxClient) VersionContext(ctx context.Context) (*client.VersionResponse, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.VersionContext(ctx)
}

func (requestScopedMinifluxClient) CategoriesContext(ctx context.Context) (client.Categories, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.CategoriesContext(ctx)
}

func (requestScopedMinifluxClient) CategoryFeedsContext(ctx context.Context, categoryID int64) (client.Feeds, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.CategoryFeedsContext(ctx, categoryID)
}

func (requestScopedMinifluxClient) FeedsContext(ctx context.Context) (client.Feeds, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.FeedsContext(ctx)
}

func (requestScopedMinifluxClient) FeedContext(ctx context.Context, feedID int64) (*client.Feed, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.FeedContext(ctx, feedID)
}

func (requestScopedMinifluxClient) RefreshFeedContext(ctx context.Context, feedID int64) error {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return err
	}

	return miniflux.RefreshFeedContext(ctx, feedID)
}

func (requestScopedMinifluxClient) FeedEntryContext(ctx context.Context, feedID, entryID int64) (*client.Entry, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.FeedEntryContext(ctx, feedID, entryID)
}

func (requestScopedMinifluxClient) CategoryEntryContext(ctx context.Context, categoryID, entryID int64) (*client.Entry, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.CategoryEntryContext(ctx, categoryID, entryID)
}

func (requestScopedMinifluxClient) EntryContext(ctx context.Context, entryID int64) (*client.Entry, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.EntryContext(ctx, entryID)
}

func (requestScopedMinifluxClient) EntriesContext(ctx context.Context, filter *client.Filter) (*client.EntryResultSet, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.EntriesContext(ctx, filter)
}

func (requestScopedMinifluxClient) FeedEntriesContext(ctx context.Context, feedID int64, filter *client.Filter) (*client.EntryResultSet, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.FeedEntriesContext(ctx, feedID, filter)
}

func (requestScopedMinifluxClient) CategoryEntriesContext(ctx context.Context, categoryID int64, filter *client.Filter) (*client.EntryResultSet, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.CategoryEntriesContext(ctx, categoryID, filter)
}

func (requestScopedMinifluxClient) UpdateEntriesContext(ctx context.Context, entryIDs []int64, status string) error {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return err
	}

	return miniflux.UpdateEntriesContext(ctx, entryIDs, status)
}

func (requestScopedMinifluxClient) ToggleStarredContext(ctx context.Context, entryID int64) error {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return err
	}

	return miniflux.ToggleStarredContext(ctx, entryID)
}

func (requestScopedMinifluxClient) FetchCountersContext(ctx context.Context) (*client.FeedCounters, error) {
	miniflux, err := minifluxClientFromContext(ctx)
	if err != nil {
		return nil, err
	}

	return miniflux.FetchCountersContext(ctx)
}
