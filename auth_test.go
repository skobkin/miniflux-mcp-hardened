package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestMinifluxCredentialHeaderPolicy(t *testing.T) {
	const token = "SENTINEL-MINIFLUX-TOKEN"
	tests := []struct {
		name             string
		credentialSource string
		headers          http.Header
		wantStatus       int
		wantToken        string
	}{
		{name: "configured credentials", credentialSource: minifluxCredentialSourceConfig, headers: http.Header{"Authorization": {"Bearer mcp-secret"}}, wantStatus: http.StatusNoContent},
		{name: "configured rejects dynamic", credentialSource: minifluxCredentialSourceConfig, headers: http.Header{minifluxTokenHeader: {token}}, wantStatus: http.StatusBadRequest},
		{name: "configured rejects native", credentialSource: minifluxCredentialSourceConfig, headers: http.Header{minifluxNativeTokenHeader: {token}}, wantStatus: http.StatusBadRequest},
		{name: "header accepts one", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{"Authorization": {"Bearer mcp-secret"}, minifluxTokenHeader: {token}}, wantStatus: http.StatusNoContent, wantToken: token},
		{name: "header missing", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{}, wantStatus: http.StatusBadRequest},
		{name: "header empty", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {""}}, wantStatus: http.StatusBadRequest},
		{name: "header outer whitespace", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {" token "}}, wantStatus: http.StatusBadRequest},
		{name: "header NUL", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {"token\x00value"}}, wantStatus: http.StatusBadRequest},
		{name: "header tab", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {"token\tvalue"}}, wantStatus: http.StatusBadRequest},
		{name: "header DEL", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {"token\x7fvalue"}}, wantStatus: http.StatusBadRequest},
		{name: "header non-ASCII", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {"tøken"}}, wantStatus: http.StatusBadRequest},
		{name: "header duplicate", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {token, "second"}}, wantStatus: http.StatusBadRequest},
		{name: "header rejects native", credentialSource: minifluxCredentialSourceHeader, headers: http.Header{minifluxTokenHeader: {token}, minifluxNativeTokenHeader: {token}}, wantStatus: http.StatusBadRequest},
		{name: "unknown source", credentialSource: "unknown", headers: http.Header{}, wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var downstreamCalled bool
			handler := enforceMinifluxCredentialHeaders(test.credentialSource, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				downstreamCalled = true
				if got, _ := r.Context().Value(minifluxTokenContextKey{}).(string); got != test.wantToken {
					t.Errorf("context token = %q, want %q", got, test.wantToken)
				}
				for _, name := range []string{"Authorization", minifluxTokenHeader, minifluxNativeTokenHeader} {
					if values := r.Header.Values(name); len(values) != 0 {
						t.Errorf("downstream request retained %s: %q", name, values)
					}
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodPost, "http://server.example/mcp", nil)
			request.Header = test.headers.Clone()
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if downstreamCalled != (test.wantStatus == http.StatusNoContent) {
				t.Fatalf("downstream called = %t", downstreamCalled)
			}
			if strings.Contains(response.Body.String(), token) {
				t.Fatal("HTTP error echoed the Miniflux token")
			}
		})
	}
}

func TestDynamicMinifluxClientsAreIsolatedAcrossConcurrentCalls(t *testing.T) {
	const (
		tokenA = "SENTINEL-TOKEN-A"
		tokenB = "SENTINEL-TOKEN-B"
	)
	identityForToken := map[string]string{tokenA: "user-a", tokenB: "user-b"}

	var mu sync.Mutex
	writeTokens := make(map[int64]string)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get(minifluxTokenHeader) != "" {
			t.Error("backend received an inbound MCP credential header")
		}
		token := r.Header.Get(minifluxNativeTokenHeader)
		identity, ok := identityForToken[token]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/feeds":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"id":1,"title":%q,"site_url":"https://example.invalid"}]`, identity)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/entries":
			var update struct {
				EntryIDs []int64 `json:"entry_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil || len(update.EntryIDs) != 1 {
				http.Error(w, "bad update", http.StatusBadRequest)

				return
			}
			mu.Lock()
			writeTokens[update.EntryIDs[0]] = token
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	httpClient, err := newMinifluxHTTPClient("")
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	minifluxServer := &MinifluxServer{client: requestScopedMinifluxClient{}}
	middleware := newRequestMinifluxClientMiddleware(backend.URL, httpClient)
	readTool := middleware(minifluxServer.GetFeeds)
	writeTool := middleware(minifluxServer.UpdateEntryStatus)

	const calls = 100
	var wait sync.WaitGroup
	errors := make(chan error, calls)
	for index := 1; index <= calls; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			token, identity := tokenA, "user-a"
			if index%2 == 0 {
				token, identity = tokenB, "user-b"
			}
			ctx := context.WithValue(context.Background(), minifluxTokenContextKey{}, token)
			readResult, err := readTool(ctx, mcp.CallToolRequest{})
			if err != nil {
				errors <- fmt.Errorf("read %d failed: %w", index, err)

				return
			}
			if readResult == nil || readResult.IsError {
				errors <- fmt.Errorf("read %d returned an error result: %#v", index, readResult)

				return
			}
			text := toolResultText(readResult)
			if !strings.Contains(text, identity) || strings.Contains(text, tokenA) || strings.Contains(text, tokenB) {
				errors <- fmt.Errorf("read %d returned wrong or secret-bearing result: %s", index, text)

				return
			}
			request := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]interface{}{
				"entry_id": float64(index),
				"status":   "read",
			}}}
			writeResult, err := writeTool(ctx, request)
			if err != nil {
				errors <- fmt.Errorf("write %d failed: %w", index, err)

				return
			}
			if writeResult == nil || writeResult.IsError {
				errors <- fmt.Errorf("write %d returned an error result: %#v", index, writeResult)
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(writeTokens) != calls {
		t.Fatalf("recorded writes = %d, want %d", len(writeTokens), calls)
	}
	for entryID, token := range writeTokens {
		want := tokenA
		if entryID%2 == 0 {
			want = tokenB
		}
		if token != want {
			t.Errorf("entry %d used token %q, want the matching request token", entryID, token)
		}
	}
}

func TestDynamicMinifluxAuthenticationFailureIsOpaque(t *testing.T) {
	const token = "SENTINEL-INVALID-TOKEN"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer backend.Close()

	httpClient, err := newMinifluxHTTPClient("")
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	tool := newToolCallLoggingMiddleware(logger)(
		newRequestMinifluxClientMiddleware(backend.URL, httpClient)((&MinifluxServer{client: requestScopedMinifluxClient{}}).GetFeeds),
	)
	ctx := context.WithValue(context.Background(), minifluxTokenContextKey{}, token)
	result, err := tool(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("GetFeeds returned Go error: %v", err)
	}
	if result == nil || !result.IsError || toolResultText(result) != "Miniflux authentication failed" {
		t.Fatalf("result = %#v, want fixed authentication error", result)
	}
	if strings.Contains(toolResultText(result), token) {
		t.Fatal("authentication result leaked the token")
	}
	if strings.Contains(logs.String(), token) {
		t.Fatal("tool-call log leaked the token")
	}
}

func toolResultText(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	text, ok := mcp.AsTextContent(result.Content[0])
	if !ok {
		return ""
	}

	return text.Text
}

func TestMinifluxRedirectsAreRefusedWithoutForwardingCredentials(t *testing.T) {
	const token = "SENTINEL-REDIRECT-TOKEN"
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if r.Header.Get(minifluxNativeTokenHeader) != "" {
			t.Error("redirect target received Miniflux credentials")
		}
		_, _ = io.WriteString(w, "OK")
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+r.URL.Path)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirector.Close()

	httpClient, err := newMinifluxHTTPClient("")
	if err != nil {
		t.Fatalf("newMinifluxHTTPClient: %v", err)
	}
	miniflux := newMinifluxAPIKeyClient(redirector.URL, token, httpClient)
	if err := miniflux.HealthcheckContext(context.Background()); err == nil {
		t.Fatal("redirected healthcheck succeeded")
	}
	if targetCalls.Load() != 0 {
		t.Fatalf("redirect target calls = %d, want 0", targetCalls.Load())
	}
}

func TestRequestScopedMiddlewareRequiresTokenContext(t *testing.T) {
	tool := newRequestMinifluxClientMiddleware("http://miniflux.invalid", &http.Client{})(func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		t.Fatal("tool handler called without a Miniflux token")

		return nil, nil
	})
	result, err := tool(context.Background(), mcp.CallToolRequest{})
	if err != nil || result == nil || !result.IsError || toolResultText(result) != "Miniflux authentication failed" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestCredentialMiddlewareDoesNotReadRequestBody(t *testing.T) {
	body := []byte("request body")
	handler := enforceMinifluxCredentialHeaders(minifluxCredentialSourceConfig, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("body = %q, want %q", got, body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://server.example/mcp", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", response.Code)
	}
}
