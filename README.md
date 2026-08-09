# Miniflux MCP Hardened [![ci](https://ci.skobk.in/api/badges/19/status.svg)](https://ci.skobk.in/repos/19)

![Miniflux MCP Hardened banner](docs/miniflux-mcp-banner.svg)

A deliberately small, security-focused Model Context Protocol server for reading a Miniflux account with an LLM agent. It is read-only by default, returns purpose-built sanitized data, and does not aim for Miniflux API completeness.

## Differences from upstream

This project is a hardened fork of [`tssujt/miniflux-mcp`](https://github.com/tssujt/miniflux-mcp). The upstream project exposes broad Miniflux API coverage; this fork narrows that surface to ordinary feed-reading workflows.

| Area | Upstream | Hardened fork |
| --- | --- | --- |
| Tool surface | 40+ API-oriented tools | 13 default read tools and 3 individually opt-in writes |
| Feeds | Read, create, update, delete, refresh, bulk operations, and icon access | Sanitized reads; only single-feed refresh can be enabled |
| Entries | Read, save, fetch original content, bulk marking, starring, and status changes including `removed` | Bounded sanitized reads; only single-entry starring and `read`/`unread` changes can be enabled |
| Categories | Read, create, update, delete, refresh, and bulk marking | Sanitized reads only |
| Administration and utilities | User and API-key administration, discovery, export, history flushing, and raw media access | Not exposed; only compact version, health, and counter diagnostics remain |
| Responses | Miniflux client objects can cross the MCP boundary | Explicit LLM-facing DTOs omit credentials and unnecessary internal fields, including in nested objects |
| Inputs and result size | Broad API-shaped schemas | Strict integer validation, bounded entry collections, no credential-bearing tool arguments |
| HTTP and runtime | STDIO and Bearer-protected Streamable HTTP | Adds strict token/origin validation, bounded requests, graceful shutdown, and a minimal non-root container |

The removed capabilities are intentional security boundaries, not missing API-completeness work.

### Security model

- Only read tools are registered by default. The three supported writes require an explicit per-tool allowlist.
- User administration, API-key management, broad mutations, feed/category management, and credential-management schemas are absent.
- Feed responses omit subscription URLs and fetch configuration such as usernames, passwords, cookies, proxy URLs, and integration endpoints. Nested feed objects are sanitized too.
- Titles, descriptions, article content, links, and tags are untrusted external data and must never be treated as MCP instructions.
- Streamable HTTP requires a Bearer token and rejects browser origins unless they are explicitly allowlisted. Use HTTPS outside a trusted private network.
- One server process uses one configured Miniflux identity. Miniflux permissions still apply, so use a dedicated non-administrator account where practical.

## Tools

`Default` indicates whether the tool is registered without `MCP_WRITE_TOOLS`; it is not a claim that private account data or external feed content is inherently trusted. `Upstream` records whether the tool existed in the original project.

| Tool | Default | Upstream | Description |
| --- | :---: | :---: | --- |
| `get_feeds` | ✅ | ✅ | List sanitized feed metadata. |
| `get_feed` | ✅ | ✅ | Get sanitized metadata for one feed. |
| `get_feed_entries` | ✅ | ✅ | List entries from one feed. |
| `get_feed_entry` | ✅ | ✅ | Get one entry from a feed, including article content. |
| `get_entries` | ✅ | ✅ | List entries with optional status, scope, time, search, starred, and ordering filters. |
| `get_entry` | ✅ | ✅ | Get one entry, including article content. |
| `get_categories` | ✅ | ✅ | List sanitized categories. |
| `get_category_feeds` | ✅ | ✅ | List sanitized feeds in one category. |
| `get_category_entries` | ✅ | ✅ | List entries in one category. |
| `get_category_entry` | ✅ | ✅ | Get one entry from a category, including article content. |
| `get_version` | ✅ | ✅ | Get the Miniflux version. |
| `healthcheck` | ✅ | ✅ | Check Miniflux availability. |
| `fetch_counters` | ✅ | ✅ | Get per-feed read and unread counters. |
| `update_entry_status` | ❌ | ✅ | Mark one explicitly selected entry `read` or `unread`. |
| `toggle_starred` | ❌ | ✅ | Toggle one explicitly selected entry's starred state. |
| `refresh_feed` | ❌ | ✅ | Request a refresh of one explicitly selected feed. |

Entry collections default to 50 results and reject limits above 100. IDs and Unix timestamp filters must be positive integers; offsets must be non-negative. Categories expose identity, visibility, and feed/unread counts; feeds expose identity, public site metadata, language, known check timestamps, disabled/parsing state, and a sanitized category. Unknown check times are omitted. Entry lists expose article metadata, status, tags, and sanitized feed identity without article bodies; single-entry responses add content, comments URL, and creation time.

Subscription `feed_url`, Miniflux user IDs, credentials, cookies, fetch/proxy settings, integration URLs, share codes, internal hashes, icons, and enclosures are intentionally absent. Version and counter tools return compact purpose-specific objects.

## Configuration

| Variable | Description | Default / requirement |
| --- | --- | --- |
| `MINIFLUX_URL` | Miniflux base URL | Required |
| `MINIFLUX_API_KEY` | API key used by this server | Required unless username/password are used |
| `MINIFLUX_USERNAME` | Miniflux username | Use with `MINIFLUX_PASSWORD` when no API key is set |
| `MINIFLUX_PASSWORD` | Miniflux password | Use with `MINIFLUX_USERNAME` when no API key is set |
| `MCP_WRITE_TOOLS` | Comma-separated write-tool allowlist | Empty; read-only |
| `MCP_TRANSPORT` | `stdio` or `streamable-http` | `stdio` |
| `MCP_HTTP_ADDR` | HTTP listen address | `:8080` |
| `MCP_HTTP_PATH` | MCP endpoint path | `/mcp` |
| `MCP_AUTH_TOKEN` | Bearer token protecting the MCP endpoint | Required for HTTP |
| `MCP_ALLOWED_ORIGINS` | Comma-separated browser origins | Empty; reject requests carrying `Origin` |

If both Miniflux authentication forms are configured, the API key is used. Credentials configure the server and are never accepted as MCP tool arguments. Health and authentication probes share a 15-second startup deadline and stop early on process termination.

Enable any combination of the three non-default tools listed in the catalog:

```text
MCP_WRITE_TOOLS=update_entry_status,toggle_starred,refresh_feed
```

Names are case-sensitive. Unknown, removed, or empty list elements fail startup, and disabled write tools are omitted from MCP registration entirely.

Configured browser origins must be exact `http://host[:port]` or `https://host[:port]` values without paths, credentials, queries, fragments, or wildcards. Scheme/host case and default ports are normalized. Requests without `Origin`, including ordinary non-browser MCP clients, remain usable. Authentication tokens with outer whitespace or newlines are rejected at startup.

## Usage

### Add to an agent

The STDIO examples assume `MINIFLUX_URL` and `MINIFLUX_API_KEY` are exported and the binary is available at `/path/to/miniflux-mcp`. The HTTP examples assume a server is listening on loopback and `MCP_AUTH_TOKEN` is exported.

Claude Code, STDIO:

```bash
claude mcp add --scope user --transport stdio miniflux --env MINIFLUX_URL="$MINIFLUX_URL" --env MINIFLUX_API_KEY="$MINIFLUX_API_KEY" -- /path/to/miniflux-mcp
```

Claude Code, HTTP:

```bash
claude mcp add --scope user --transport http miniflux http://127.0.0.1:8080/mcp --header "Authorization: Bearer $MCP_AUTH_TOKEN"
```

Codex, STDIO:

```bash
codex mcp add miniflux --env MINIFLUX_URL="$MINIFLUX_URL" --env MINIFLUX_API_KEY="$MINIFLUX_API_KEY" -- /path/to/miniflux-mcp
```

Codex, HTTP:

```bash
codex mcp add miniflux --url http://127.0.0.1:8080/mcp --bearer-token-env-var MCP_AUTH_TOKEN
```

For project-scoped clients that support `.mcp.json`, choose either transport. This STDIO example launches the published container:

```json
{
  "mcpServers": {
    "miniflux": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "--read-only", "--cap-drop=ALL",
        "--security-opt=no-new-privileges", "-e", "MINIFLUX_URL", "-e", "MINIFLUX_API_KEY",
        "skobkin/miniflux-mcp-hardened:latest"],
      "env": {
        "MINIFLUX_URL": "${MINIFLUX_URL}",
        "MINIFLUX_API_KEY": "${MINIFLUX_API_KEY}"
      }
    }
  }
}
```

For an already-running HTTP server:

```json
{
  "mcpServers": {
    "miniflux": {
      "type": "http",
      "url": "https://mcp.example.com/mcp",
      "headers": {"Authorization": "Bearer ${MCP_AUTH_TOKEN}"}
    }
  }
}
```

### Run with Docker or Podman

Run the default STDIO transport:

```bash
docker run -i --rm \
  --read-only \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --env-file .env \
  skobkin/miniflux-mcp-hardened:latest
```

Run Streamable HTTP behind an HTTPS reverse proxy:

```bash
docker run --rm \
  --read-only \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  -p 127.0.0.1:8080:8080 \
  --env-file .env \
  -e MCP_TRANSPORT=streamable-http \
  -e MCP_AUTH_TOKEN \
  skobkin/miniflux-mcp-hardened:latest
```

Example Compose service:

```yaml
services:
  miniflux-mcp:
    image: skobkin/miniflux-mcp-hardened:latest
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

Clients send `Authorization: Bearer <token>`. Request reads and header sizes are bounded; response writes remain unbounded because Streamable HTTP may use long-lived SSE streams. SIGTERM and SIGINT trigger a bounded graceful shutdown. `/healthz` is intentionally unauthenticated and returns only `ok`.

## Continuous integration and releases

Woodpecker CI runs formatting, lint, vet, unit, race, build, and end-to-end checks for pushes and pull requests targeting `main`. The end-to-end suite covers both STDIO and authenticated Streamable HTTP.

Tags matching `v*` run the same checks before publishing a static Linux AMD64 archive and checksum to [Forgejo](https://git.skobk.in/skobkin/miniflux-mcp-hardened/releases) and versioned [`skobkin/miniflux-mcp-hardened`](https://hub.docker.com/r/skobkin/miniflux-mcp-hardened) images to Docker Hub.

## Development

The local completion checks are:

```bash
go fmt ./...
go vet ./...
go test ./...
golangci-lint run ./...
go build ./...
```

Unit tests use local fakes and `httptest`; a running Miniflux/PostgreSQL stack is not required. Run the containerized end-to-end suite with `make e2e`, or target an existing Miniflux instance:

```bash
make e2e-test \
  E2E_MINIFLUX_URL=http://miniflux:8080 \
  E2E_MINIFLUX_USERNAME=admin \
  E2E_MINIFLUX_PASSWORD=test123
```

## License

This project is licensed under the MIT License. See `LICENSE` for details.
