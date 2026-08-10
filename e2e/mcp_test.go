//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

type rpcClient struct {
	input  io.Writer
	output *bufio.Reader
	nextID int
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

type initializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type httpRPCClient struct {
	endpoint string
	token    string
	client   *http.Client
	nextID   int
}

const (
	e2eNonexistentEntryID = int64(1<<53 - 1)
	e2eOversizedMCPBody   = 1<<20 + 1
)

func (c *rpcClient) request(method string, params any, result any) error {
	c.nextID++
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  method,
		"params":  params,
	}
	if err := json.NewEncoder(c.input).Encode(request); err != nil {
		return fmt.Errorf("encode %s request: %w", method, err)
	}

	for {
		line, err := c.output.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read %s response: %w", method, err)
		}

		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		if response.ID != c.nextID {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf("%s failed (%d): %s", method, response.Error.Code, response.Error.Message)
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
		return nil
	}
}

func (c *rpcClient) notify(method string) error {
	return json.NewEncoder(c.input).Encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	})
}

func (c *rpcClient) callTool(name string, arguments map[string]any) (string, error) {
	var result toolCallResult
	if err := c.request("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, &result); err != nil {
		return "", err
	}
	if result.IsError {
		return "", fmt.Errorf("%s returned a tool error: %+v", name, result.Content)
	}
	for _, content := range result.Content {
		if content.Type == "text" {
			return content.Text, nil
		}
	}
	return "", fmt.Errorf("%s returned no text content", name)
}

func (c *httpRPCClient) request(method string, params any, result any) error {
	c.nextID++
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      c.nextID,
		"method":  method,
		"params":  params,
	}
	return c.send(request, http.StatusOK, result)
}

func (c *httpRPCClient) notify(method string) error {
	request := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	return c.send(request, http.StatusAccepted, nil)
}

func (c *httpRPCClient) send(request map[string]any, expectedStatus int, result any) error {
	payload, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode %s request: %w", request["method"], err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create %s request: %w", request["method"], err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("send %s request: %w", request["method"], err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read %s response: %w", request["method"], err)
	}
	if response.StatusCode != expectedStatus {
		return fmt.Errorf("%s returned HTTP %d: %s", request["method"], response.StatusCode, strings.TrimSpace(string(body)))
	}
	if result == nil {
		return nil
	}

	var rpcResult rpcResponse
	if err := json.Unmarshal(body, &rpcResult); err != nil {
		return fmt.Errorf("decode %s response: %w", request["method"], err)
	}
	if rpcResult.Error != nil {
		return fmt.Errorf("%s failed (%d): %s", request["method"], rpcResult.Error.Code, rpcResult.Error.Message)
	}
	if err := json.Unmarshal(rpcResult.Result, result); err != nil {
		return fmt.Errorf("decode %s result: %w", request["method"], err)
	}
	return nil
}

func (c *httpRPCClient) callTool(name string, arguments map[string]any) (string, error) {
	var result toolCallResult
	if err := c.request("tools/call", map[string]any{
		"name":      name,
		"arguments": arguments,
	}, &result); err != nil {
		return "", err
	}
	if result.IsError {
		return "", fmt.Errorf("%s returned a tool error: %+v", name, result.Content)
	}
	for _, content := range result.Content {
		if content.Type == "text" {
			return content.Text, nil
		}
	}
	return "", fmt.Errorf("%s returned no text content", name)
}

func startStdioMCPServer(t *testing.T, writeTools, clientName string, extraEnv ...string) *rpcClient {
	t.Helper()
	serverPath := os.Getenv("MCP_SERVER_PATH")
	if serverPath == "" {
		t.Fatal("MCP_SERVER_PATH is required")
	}
	for _, name := range []string{"MINIFLUX_URL", "MINIFLUX_USERNAME", "MINIFLUX_PASSWORD"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required", name)
		}
	}

	command := exec.Command(serverPath)
	command.Env = append(os.Environ(), "MCP_WRITE_TOOLS="+writeTools)
	command.Env = append(command.Env, extraEnv...)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("create server stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("create server stdout: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start MCP server: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("MCP server exited with error: %v\n%s", err, stderr.String())
			}
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Errorf("MCP server did not stop after stdin was closed")
		}
	})

	client := &rpcClient{input: stdin, output: bufio.NewReader(stdout)}
	var initialized initializeResult
	if err := client.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    clientName,
			"version": "1.0.0",
		},
	}, &initialized); err != nil {
		t.Fatalf("initialize MCP session: %v\n%s", err, stderr.String())
	}
	if initialized.ProtocolVersion == "" {
		t.Fatal("initialize response did not include a protocol version")
	}
	if initialized.ServerInfo.Version != os.Getenv("EXPECTED_SERVER_VERSION") {
		t.Fatalf("initialize server version = %q, want %q", initialized.ServerInfo.Version, os.Getenv("EXPECTED_SERVER_VERSION"))
	}
	if err := client.notify("notifications/initialized"); err != nil {
		t.Fatalf("send initialized notification: %v", err)
	}

	return client
}

func TestMCPServerWithMiniflux(t *testing.T) {
	client := startStdioMCPServer(t, "", "miniflux-mcp-ci")

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := client.request("tools/list", map[string]any{}, &listed); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	toolNames := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	for _, name := range []string{"get_version", "get_categories", "get_entries", "get_entry", "get_unread_digest"} {
		if !slices.Contains(toolNames, name) {
			t.Fatalf("tools/list did not include %q", name)
		}
	}
	for _, name := range []string{"create_category", "delete_category", "create_user", "get_api_keys", "flush_history"} {
		if slices.Contains(toolNames, name) {
			t.Fatalf("tools/list unexpectedly included removed tool %q", name)
		}
	}
	for _, name := range []string{"update_entry_status", "update_entries_status", "toggle_starred", "refresh_feed"} {
		if slices.Contains(toolNames, name) {
			t.Fatalf("tools/list unexpectedly included disabled write tool %q", name)
		}
	}

	versionText, err := client.callTool("get_version", map[string]any{})
	if err != nil {
		t.Fatalf("get Miniflux version: %v", err)
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(versionText), &version); err != nil {
		t.Fatalf("decode Miniflux version: %v", err)
	}
	if version.Version == "" {
		t.Fatal("get_version returned an empty version")
	}

	categoriesText, err := client.callTool("get_categories", map[string]any{})
	if err != nil {
		t.Fatalf("get categories: %v", err)
	}
	var categories []struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(categoriesText), &categories); err != nil {
		t.Fatalf("decode categories: %v", err)
	}
	if len(categories) == 0 {
		t.Fatal("get_categories returned no categories")
	}

	digestText, err := client.callTool("get_unread_digest", map[string]any{})
	if err != nil {
		t.Fatalf("get unread digest: %v", err)
	}
	var digest struct {
		Entries       []json.RawMessage `json:"entries"`
		AckEntryIDs   []int64           `json:"ack_entry_ids"`
		ScanTruncated bool              `json:"scan_truncated"`
	}
	if err := json.Unmarshal([]byte(digestText), &digest); err != nil {
		t.Fatalf("decode unread digest: %v", err)
	}
	if digest.Entries == nil || digest.AckEntryIDs == nil {
		t.Fatalf("get_unread_digest returned null collections: %s", digestText)
	}

	healthCommand := exec.Command(os.Getenv("MCP_SERVER_PATH"), "healthcheck")
	healthCommand.Env = append(os.Environ(), "MCP_TRANSPORT=stdio")
	if output, err := healthCommand.CombinedOutput(); err != nil {
		t.Fatalf("stdio healthcheck command failed: %v\n%s", err, output)
	}
}

func TestWriteEnabledMCPServerWithMiniflux(t *testing.T) {
	updateRequests := make(chan []byte, 1)
	proxyTransport := &http.Transport{Proxy: nil}
	t.Cleanup(proxyTransport.CloseIdleConnections)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/v1/entries" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "failed to record request", http.StatusBadGateway)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			updateRequests <- body
		}

		outbound := r.Clone(r.Context())
		outbound.RequestURI = ""
		response, err := proxyTransport.RoundTrip(outbound)
		if err != nil {
			http.Error(w, "failed to proxy request", http.StatusBadGateway)
			return
		}
		defer func() {
			_ = response.Body.Close()
		}()
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()

	client := startStdioMCPServer(t, "update_entries_status", "miniflux-mcp-write-ci", "MINIFLUX_PROXY_URL="+proxy.URL)

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := client.request("tools/list", map[string]any{}, &listed); err != nil {
		t.Fatalf("list write-enabled tools: %v", err)
	}
	toolNames := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	if !slices.Contains(toolNames, "update_entries_status") {
		t.Fatalf("tools/list did not include enabled update_entries_status tool: %v", toolNames)
	}
	for _, name := range []string{"update_entry_status", "toggle_starred", "refresh_feed"} {
		if slices.Contains(toolNames, name) {
			t.Fatalf("tools/list unexpectedly included disabled write tool %q", name)
		}
	}

	statusText, err := client.callTool("update_entries_status", map[string]any{
		"entry_ids": []int64{e2eNonexistentEntryID},
		"status":    "read",
	})
	if err != nil {
		t.Fatalf("acknowledge entries through bulk status tool: %v", err)
	}
	var status struct {
		Updated int    `json:"updated"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal([]byte(statusText), &status); err != nil {
		t.Fatalf("decode bulk status result: %v", err)
	}
	if status.Updated != 1 || status.Status != "read" {
		t.Fatalf("bulk status result = %#v", status)
	}

	var updatePayload struct {
		EntryIDs []int64 `json:"entry_ids"`
		Status   string  `json:"status"`
	}
	select {
	case body := <-updateRequests:
		if err := json.Unmarshal(body, &updatePayload); err != nil {
			t.Fatalf("decode proxied bulk status request: %v", err)
		}
	default:
		t.Fatal("bulk status tool did not send a request to Miniflux")
	}
	if !slices.Equal(updatePayload.EntryIDs, []int64{e2eNonexistentEntryID}) || updatePayload.Status != "read" {
		t.Fatalf("proxied bulk status request = %#v", updatePayload)
	}
}

func TestRemoteMCPServerWithMiniflux(t *testing.T) {
	serverPath := os.Getenv("MCP_SERVER_PATH")
	if serverPath == "" {
		t.Fatal("MCP_SERVER_PATH is required")
	}
	for _, name := range []string{"MINIFLUX_URL", "MINIFLUX_USERNAME", "MINIFLUX_PASSWORD"} {
		if os.Getenv(name) == "" {
			t.Fatalf("%s is required", name)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve HTTP port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release HTTP port: %v", err)
	}

	const token = "e2e-secret"
	command := exec.Command(serverPath)
	command.Env = append(os.Environ(),
		"MCP_TRANSPORT=streamable-http",
		"MCP_HTTP_ADDR="+address,
		"MCP_HTTP_PATH=/mcp",
		"MCP_AUTH_TOKEN="+token,
		"MCP_ALLOWED_ORIGINS=",
		"MCP_WRITE_TOOLS=update_entries_status",
	)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start remote MCP server: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = command.Process.Signal(os.Interrupt)
		}
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Errorf("remote MCP server did not stop after interrupt")
		}
	})

	baseURL := "http://" + address
	httpClient := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, requestErr := httpClient.Get(baseURL + "/healthz")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote MCP server did not become healthy: %v\n%s", requestErr, stderr.String())
		}
		time.Sleep(100 * time.Millisecond)
	}

	unauthorizedRequest, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("create unauthorized request: %v", err)
	}
	unauthorizedRequest.Header.Set("Content-Type", "application/json")
	unauthorizedResponse, err := httpClient.Do(unauthorizedRequest)
	if err != nil {
		t.Fatalf("send unauthorized request: %v", err)
	}
	_ = unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized request returned HTTP %d, want 401", unauthorizedResponse.StatusCode)
	}

	badOriginRequest, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	if err != nil {
		t.Fatalf("create bad-origin request: %v", err)
	}
	badOriginRequest.Header.Set("Content-Type", "application/json")
	badOriginRequest.Header.Set("Authorization", "Bearer "+token)
	badOriginRequest.Header.Set("Origin", "https://evil.example")
	badOriginResponse, err := httpClient.Do(badOriginRequest)
	if err != nil {
		t.Fatalf("send bad-origin request: %v", err)
	}
	_ = badOriginResponse.Body.Close()
	if badOriginResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("bad-origin request returned HTTP %d, want 403", badOriginResponse.StatusCode)
	}

	oversizedRequest, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(bytes.Repeat([]byte("x"), e2eOversizedMCPBody)))
	if err != nil {
		t.Fatalf("create oversized request: %v", err)
	}
	oversizedRequest.Header.Set("Authorization", "Bearer "+token)
	oversizedRequest.Header.Set("Content-Type", "application/json")
	oversizedResponse, err := httpClient.Do(oversizedRequest)
	if err != nil {
		t.Fatalf("send oversized request: %v", err)
	}
	_ = oversizedResponse.Body.Close()
	if oversizedResponse.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request returned HTTP %d, want 413", oversizedResponse.StatusCode)
	}

	client := &httpRPCClient{
		endpoint: baseURL + "/mcp",
		token:    token,
		client:   httpClient,
	}
	var initialized initializeResult
	if err := client.request("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "miniflux-mcp-http-ci",
			"version": "1.0.0",
		},
	}, &initialized); err != nil {
		t.Fatalf("initialize remote MCP session: %v", err)
	}
	if initialized.ProtocolVersion == "" {
		t.Fatal("initialize response did not include a protocol version")
	}
	if initialized.ServerInfo.Version != os.Getenv("EXPECTED_SERVER_VERSION") {
		t.Fatalf("initialize server version = %q, want %q", initialized.ServerInfo.Version, os.Getenv("EXPECTED_SERVER_VERSION"))
	}
	if err := client.notify("notifications/initialized"); err != nil {
		t.Fatalf("send initialized notification: %v", err)
	}

	var listed struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := client.request("tools/list", map[string]any{}, &listed); err != nil {
		t.Fatalf("list remote tools: %v", err)
	}
	toolNames := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	for _, name := range []string{"get_version", "get_categories", "get_entries", "get_unread_digest"} {
		if !slices.Contains(toolNames, name) {
			t.Fatalf("tools/list did not include %q", name)
		}
	}
	for _, name := range []string{"create_category", "delete_category", "create_user", "get_api_keys", "flush_history"} {
		if slices.Contains(toolNames, name) {
			t.Fatalf("remote tools/list unexpectedly included removed tool %q", name)
		}
	}
	if !slices.Contains(toolNames, "update_entries_status") {
		t.Fatalf("remote tools/list did not include enabled update_entries_status tool: %v", toolNames)
	}
	for _, name := range []string{"update_entry_status", "toggle_starred", "refresh_feed"} {
		if slices.Contains(toolNames, name) {
			t.Fatalf("remote tools/list unexpectedly included disabled write tool %q", name)
		}
	}

	healthCommand := exec.Command(serverPath, "healthcheck")
	healthCommand.Env = append(os.Environ(),
		"MCP_TRANSPORT=streamable-http",
		"MCP_HTTP_ADDR="+address,
		"MCP_HTTP_PATH=/mcp",
		"MCP_AUTH_TOKEN="+token,
		"MCP_ALLOWED_ORIGINS=",
	)
	if output, err := healthCommand.CombinedOutput(); err != nil {
		t.Fatalf("streamable HTTP healthcheck command failed: %v\n%s", err, output)
	}
}
