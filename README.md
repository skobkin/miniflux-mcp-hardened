# Miniflux MCP Hardened

A deliberately small, security-focused Model Context Protocol server for reading a Miniflux account with an LLM agent.

This fork is read-only by default. It exposes sanitized, purpose-built responses rather than raw Miniflux API objects, and it does not aim for Miniflux API completeness.

## Security model

- The default MCP surface contains only read tools.
- Three ordinary reading-workflow mutations can be enabled individually with an explicit allowlist.
- User administration, API-key management, broad mutations, feed/category management, and credential-management schemas are not exposed.
- Feed responses omit subscription URLs and fetch configuration, including usernames, passwords, cookies, proxy URLs, and integration endpoints.
- Entry responses contain only a sanitized nested feed identity. Entry lists omit article bodies; single-entry tools include article content.
- Titles, descriptions, article content, links, and tags originate outside this server and must be treated as untrusted data, never as MCP instructions.
- Streamable HTTP requires a Bearer token. Browser origins are denied unless explicitly allowlisted.

One server process uses one configured Miniflux identity. Miniflux permissions still apply behind the narrower MCP policy, so use a dedicated non-administrator account where practical.

## Configuration

### Miniflux connection

| Variable | Description | Required |
| --- | --- | --- |
| `MINIFLUX_URL` | Miniflux base URL | Yes |
| `MINIFLUX_API_KEY` | API key used by this server | Yes, unless username/password are used |
| `MINIFLUX_USERNAME` | Miniflux username | With `MINIFLUX_PASSWORD` when no API key is set |
| `MINIFLUX_PASSWORD` | Miniflux password | With `MINIFLUX_USERNAME` when no API key is set |
| `MCP_WRITE_TOOLS` | Comma-separated allowlist of supported write tools | No; empty means read-only |

If both authentication forms are present, the API key is used. Credentials configure the server itself and are never accepted as MCP tool arguments.

### Optional write tools

Only these values are accepted in `MCP_WRITE_TOOLS`:

- `update_entry_status` marks one explicitly selected entry `read` or `unread`.
- `toggle_starred` toggles one explicitly selected entry's starred state.
- `refresh_feed` asks Miniflux to refresh one explicitly selected feed.

For example:

```text
MCP_WRITE_TOOLS=update_entry_status,toggle_starred,refresh_feed
```

Names are case-sensitive. Unknown, removed, or empty list elements cause startup to fail instead of being ignored. Disabled tools are omitted from MCP registration entirely.

## Tool surface

The following 13 tools are always available:

### Feeds and categories

- `get_feeds`
- `get_feed`
- `get_feed_entries`
- `get_feed_entry`
- `get_categories`
- `get_category_feeds`
- `get_category_entries`
- `get_category_entry`

### Entries and diagnostics

- `get_entries`
- `get_entry`
- `get_version`
- `healthcheck`
- `fetch_counters`

Entry collection tools default to 50 results and reject limits above 100. IDs must be positive integers and offsets must be non-negative.

### Sanitized responses

- Categories expose identity, visibility, and available feed/unread counts.
- Feeds expose identity, public site metadata, language, check timestamps, disabled state, parsing-error count, and a sanitized category.
- Entry lists expose article metadata, status, tags, and sanitized feed identity without article content.
- Single-entry tools additionally expose article content, comments URL, and creation timestamp.
- Version and counter tools return compact purpose-specific objects.

Subscription `feed_url`, Miniflux user IDs, credentials, cookies, fetch/proxy settings, integration URLs, share codes, internal hashes, icons, and enclosures are intentionally absent.

## Local stdio server

`stdio` is the default transport and is intended for an MCP client that starts the server locally.

Build and run with Docker or Podman:

```bash
docker build -t miniflux-mcp-hardened .
docker run -i --rm \
  --read-only \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --env-file .env \
  miniflux-mcp-hardened
```

Example `.mcp.json`:

```json
{
  "mcpServers": {
    "miniflux": {
      "type": "stdio",
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "--read-only",
        "--cap-drop=ALL",
        "--security-opt=no-new-privileges",
        "-e",
        "MINIFLUX_URL",
        "-e",
        "MINIFLUX_API_KEY",
        "miniflux-mcp-hardened"
      ],
      "env": {
        "MINIFLUX_URL": "${MINIFLUX_URL}",
        "MINIFLUX_API_KEY": "${MINIFLUX_API_KEY}"
      }
    }
  }
}
```

## Remote Streamable HTTP server

| Variable | Description | Default |
| --- | --- | --- |
| `MCP_TRANSPORT` | Set to `streamable-http` | `stdio` |
| `MCP_HTTP_ADDR` | Listen address | `:8080` |
| `MCP_HTTP_PATH` | MCP endpoint path | `/mcp` |
| `MCP_AUTH_TOKEN` | Bearer token protecting the MCP endpoint | Required in HTTP mode |
| `MCP_ALLOWED_ORIGINS` | Comma-separated browser origins such as `https://client.example`; scheme/host case and default ports are normalized | Empty; reject all requests carrying `Origin` |

Requests without `Origin`, including ordinary non-browser MCP clients, remain usable. Configured origins must be exact `http://host[:port]` or `https://host[:port]` values without paths, credentials, queries, or fragments. There is no wildcard mode.

Start an HTTP server behind an HTTPS reverse proxy:

```bash
docker run --rm \
  --read-only \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  -p 127.0.0.1:8080:8080 \
  --env-file .env \
  -e MCP_TRANSPORT=streamable-http \
  -e MCP_AUTH_TOKEN \
  miniflux-mcp-hardened
```

Clients send the configured secret as `Authorization: Bearer <token>`. Request reads and header sizes are bounded; response writes remain unbounded because Streamable HTTP may use long-lived SSE streams. SIGTERM and SIGINT trigger a bounded graceful shutdown. `/healthz` is intentionally unauthenticated and returns only `ok`.

Example Compose service:

```yaml
services:
  miniflux-mcp:
    build: .
    restart: unless-stopped
    read_only: true
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      MINIFLUX_URL: ${MINIFLUX_URL}
      MINIFLUX_API_KEY: ${MINIFLUX_API_KEY}
      MCP_TRANSPORT: streamable-http
      MCP_AUTH_TOKEN: ${MCP_AUTH_TOKEN}
      MCP_WRITE_TOOLS: ${MCP_WRITE_TOOLS:-}
      MCP_ALLOWED_ORIGINS: ${MCP_ALLOWED_ORIGINS:-}
```

Terminate TLS at a reverse proxy whenever traffic leaves a trusted private network; otherwise the Bearer token and returned feed data are not encrypted in transit.

## Deliberate differences from upstream

The upstream-oriented implementation exposed broad Miniflux API coverage. This fork removes the following MCP tools rather than merely disabling them by documentation:

- Feed management and bulk operations: `create_feed`, `update_feed`, `delete_feed`, `refresh_all_feeds`, `get_feed_icon`, `mark_feed_as_read`.
- Entry operations outside the selected workflow: `save_entry`, `fetch_original_content`, `mark_all_as_read`.
- Category mutations: `create_category`, `update_category`, `delete_category`, `mark_category_as_read`, `refresh_category`.
- User administration: `get_users`, `get_me`, `get_user_by_id`, `get_user_by_username`, `create_user`, `delete_user`.
- Network/export/administrative utilities: `discover`, `export`, `flush_history`.
- API-key management: `get_api_keys`, `create_api_key`, `delete_api_key`.
- Raw media lookups: `get_icon`, `get_enclosure`.

The retained `update_entry_status` tool no longer accepts `removed`; only `read` and `unread` are supported. Feed create/update schemas and all credential-bearing arguments are absent. These are intentional security boundaries, not missing API-completeness work.

## Development

The local completion checks are:

```bash
go fmt ./...
go vet ./...
go test ./...
golangci-lint run ./...
go build ./...
```

Tests use local fakes and `httptest`; a running Miniflux/PostgreSQL stack is not required for unit validation.

## License

This project is licensed under the MIT License. See `LICENSE` for details.
