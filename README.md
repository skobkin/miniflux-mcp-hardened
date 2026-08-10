# Miniflux MCP Hardened [![ci](https://ci.skobk.in/api/badges/19/status.svg)](https://ci.skobk.in/repos/19)

![Miniflux MCP Hardened banner](docs/miniflux-mcp-banner.svg)

A deliberately small, security-focused Model Context Protocol server for reading a Miniflux account with an LLM agent. It is read-only by default, returns purpose-built sanitized data, and does not aim for Miniflux API completeness.

## Differences from upstream

This project is a hardened fork of [`tssujt/miniflux-mcp`](https://github.com/tssujt/miniflux-mcp). The upstream project exposes broad Miniflux API coverage; this fork narrows that surface to ordinary feed-reading workflows.

| Area | Upstream | Hardened fork |
| --- | --- | --- |
| Tool surface | 40+ API-oriented tools | 14 default read tools and 4 individually opt-in writes |
| Feeds | Read, create, update, delete, refresh, bulk operations, and icon access | Sanitized reads; only single-feed refresh can be enabled |
| Entries | Read, save, fetch original content, bulk marking, starring, and status changes including `removed` | Bounded sanitized reads; only explicitly selected entries can be starred or changed to `read`/`unread`, including status updates in batches of up to 100 |
| Categories | Read, create, update, delete, refresh, and bulk marking | Sanitized reads only |
| Administration and utilities | User and API-key administration, discovery, export, history flushing, and raw media access | Not exposed; only compact version, health, and counter diagnostics remain |
| Responses | Miniflux client objects can cross the MCP boundary | Explicit LLM-facing DTOs omit credentials and unnecessary internal fields, including in nested objects |
| Inputs and result size | Broad API-shaped schemas | Strict integer validation, bounded entry collections, no credential-bearing tool arguments |
| HTTP and runtime | STDIO and Bearer-protected Streamable HTTP | Adds strict token/origin validation, bounded requests, graceful shutdown, and a minimal non-root container |

The removed capabilities are intentional security boundaries, not missing API-completeness work.

### Security model

- Only read tools are registered by default. The four supported writes require an explicit per-tool allowlist.
- User administration, API-key management, broad mutations, feed/category management, and credential-management schemas are absent.
- Feed responses omit subscription URLs and fetch configuration such as usernames, passwords, cookies, proxy URLs, and integration endpoints. Nested feed objects are sanitized too.
- Titles, descriptions, article content, links, and tags are untrusted external data and must never be treated as MCP instructions.
- Streamable HTTP requires a Bearer token and rejects browser origins unless they are explicitly allowlisted. Use HTTPS outside a trusted private network.
- One server process uses one configured Miniflux identity. Miniflux permissions still apply, so use a dedicated non-administrator account where practical.
- Every MCP tool invocation emits a structured JSON record to stderr with only the tool name, read/write class, outcome, and duration. Arguments, results, article data, credentials, tokens, headers, and backend error details are not logged.

## Tools

`Default` indicates whether the tool is registered without `MCP_WRITE_TOOLS`; it is not a claim that private account data or external feed content is inherently trusted. `Upstream` records whether the tool existed in the original project.

| Tool | Default | Upstream | Description |
| --- | :---: | :---: | --- |
| `get_feeds` | ✅ | ✅ | List sanitized feed metadata. |
| `get_feed` | ✅ | ✅ | Get sanitized metadata for one feed. |
| `get_feed_entries` | ✅ | ✅ | List entries from one feed. |
| `get_feed_entry` | ✅ | ✅ | Get one entry from a feed, including bounded pageable article content. |
| `get_entries` | ✅ | ✅ | List entries with optional status, scope, time, search, starred, and ordering filters. |
| `get_unread_digest` | ✅ | ❌ | Get a compact oldest-first unread batch with bounded excerpts, acknowledgement IDs, and truncation metadata. |
| `get_entry` | ✅ | ✅ | Get one entry, including bounded pageable article content. |
| `get_categories` | ✅ | ✅ | List sanitized categories. |
| `get_category_feeds` | ✅ | ✅ | List sanitized feeds in one category. |
| `get_category_entries` | ✅ | ✅ | List entries in one category. |
| `get_category_entry` | ✅ | ✅ | Get one entry from a category, including bounded pageable article content. |
| `get_version` | ✅ | ✅ | Get the Miniflux version. |
| `healthcheck` | ✅ | ✅ | Check Miniflux availability. |
| `fetch_counters` | ✅ | ✅ | Get per-feed read and unread counters. |
| `update_entry_status` | ❌ | ✅ | Mark one explicitly selected entry `read` or `unread`. |
| `update_entries_status` | ❌ | ❌ | Mark up to 100 explicitly selected entries `read` or `unread`. |
| `toggle_starred` | ❌ | ✅ | Toggle one explicitly selected entry's starred state. |
| `refresh_feed` | ❌ | ✅ | Request a refresh of one explicitly selected feed. |

Metadata-only entry collections default to 50 results and reject limits above 100. The content-bearing unread digest defaults to 10 entries and rejects limits above 20. IDs and Unix timestamp filters must be positive integers; collection and content offsets must be non-negative safely representable JSON integers. Entry search strings are limited to 4,096 Unicode characters in both the schema and runtime validation. Categories expose identity, visibility, and feed/unread counts; feeds expose identity, public site metadata, language, known check timestamps, disabled/parsing state, and a sanitized category. Unknown check times are omitted. Entry lists expose article metadata, status, tags, and sanitized feed identity without article bodies.

`get_unread_digest` returns Miniflux-provided article excerpts with an oldest-first unread queue. Invalid UTF-8 from a backend is replaced before excerpts are measured. Excerpts are shared fairly across the batch, are capped at 8 KiB per entry, and carry `content_truncated` when they do not contain the complete normalized article. The fully encoded result is capped at 96 KiB; `response_size_limited` is present and true only in the exceptional case where untrusted metadata forces the server to return fewer entries than requested. After successful downstream processing, pass only the entries the caller actually processed from `ack_entry_ids` to the separately allowlisted `update_entries_status` tool. An acknowledgement ID identifies the stable returned batch; it does not assert that a truncated excerpt represents the full article. The bulk tool accepts 1–100 unique safe IDs and only the statuses `read` and `unread`; it never exposes Miniflux's `removed` mutation. `since` is an optional caller-owned Unix timestamp applied to `published_at`; no timezone or day-boundary policy is invented. Feed and category filters intersect, exclusions win, and category filtering scans at most 1,000 unread candidates to fill the bounded batch. `scan_truncated` reports when that defensive scan cap prevents a full batch.

Single-entry tools replace invalid backend UTF-8 and return a content chunk within the same 96 KiB encoded-result limit. When `content_complete` is false, call the same tool again with its `next_content_offset`; the offset is an opaque byte position in the normalized UTF-8 content and should be passed back unchanged. `content_total_bytes` reports that normalized content size. Detail responses also include comments URL and creation time.

Subscription `feed_url`, Miniflux user IDs, credentials, cookies, fetch/proxy settings, integration URLs, share codes, internal hashes, icons, and enclosures are intentionally absent. Version and counter tools return compact purpose-specific objects.

## Configuration

| Variable | Description | Default / requirement |
| --- | --- | --- |
| `MINIFLUX_URL` | Miniflux base URL | Required |
| `MINIFLUX_API_KEY` | API key used by this server | Required unless username/password are used |
| `MINIFLUX_USERNAME` | Miniflux username | Use with `MINIFLUX_PASSWORD` when no API key is set |
| `MINIFLUX_PASSWORD` | Miniflux password | Use with `MINIFLUX_USERNAME` when no API key is set |
| `MINIFLUX_PROXY_URL` | Proxy for this process's connection to Miniflux; supports `http`, `https`, `socks5`, and `socks5h` | Unset; standard Go transport behavior |
| `MCP_WRITE_TOOLS` | Comma-separated write-tool allowlist | Empty; read-only |
| `MCP_TRANSPORT` | `stdio` or `streamable-http` | `stdio`; container image: `streamable-http` |
| `MCP_HTTP_ADDR` | HTTP listen address | `:8080` |
| `MCP_HTTP_PATH` | MCP endpoint path | `/mcp` |
| `MCP_AUTH_TOKEN` | Bearer token protecting the MCP endpoint | Required for HTTP |
| `MCP_ALLOWED_ORIGINS` | Comma-separated browser origins | Empty; reject requests carrying `Origin` |

If both Miniflux authentication forms are configured, the API key is used. Credentials configure the server and are never accepted as MCP tool arguments. Health and authentication probes share a 15-second startup deadline and stop early on process termination. Miniflux API requests use a 30-second client timeout.

`MINIFLUX_PROXY_URL` controls only MCP-to-Miniflux traffic; it does not change how Miniflux fetches feeds or articles. When unset, Go's default transport continues to honor `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY`. When set, the explicit proxy is used for both HTTP and HTTPS Miniflux URLs while `NO_PROXY`/`no_proxy` exclusions remain effective. Proxy credentials may be supplied as URL userinfo but are never included in MCP results, logs, or startup errors.

Enable any combination of the four non-default tools listed in the catalog:

```text
MCP_WRITE_TOOLS=update_entry_status,update_entries_status,toggle_starred,refresh_feed
```

Names are case-sensitive. Unknown, disallowed, or empty list elements fail startup, and disabled write tools are omitted from MCP registration entirely.

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

For project-scoped clients that support `.mcp.json`, choose either transport. This STDIO example launches the local binary:

```json
{
  "mcpServers": {
    "miniflux": {
      "type": "stdio",
      "command": "/path/to/miniflux-mcp",
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

### Run with Docker

Example Compose service for Streamable HTTP behind an HTTPS reverse proxy:

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
      MCP_AUTH_TOKEN: ${MCP_AUTH_TOKEN}
      MCP_WRITE_TOOLS: ${MCP_WRITE_TOOLS:-}
      MCP_ALLOWED_ORIGINS: ${MCP_ALLOWED_ORIGINS:-}
```

The container image defaults to Streamable HTTP; set `MCP_AUTH_TOKEN` or startup fails. Clients send `Authorization: Bearer <token>`. Authenticated MCP request bodies are limited to 1 MiB and oversized requests return HTTP 413; `/healthz` is unaffected. Request reads and header sizes are bounded, while response writes remain unbounded because Streamable HTTP may use long-lived SSE streams. SIGTERM and SIGINT trigger a bounded graceful shutdown. `/healthz` is intentionally unauthenticated and returns plain text `ok` followed by a newline.

The scratch image uses an exec-form `HEALTHCHECK` that runs `/miniflux-mcp healthcheck` without a shell or extra runtime packages. In Streamable HTTP mode it probes the local `/healthz` endpoint. In STDIO mode it validates configuration and performs a bounded Miniflux health probe, so the absence of an HTTP listener does not make STDIO containers permanently unhealthy. The command returns conventional exit status and does not print secrets or require a writable filesystem.

## Continuous integration and releases

Woodpecker CI runs formatting, lint, vet, unit, race, build, end-to-end, and dry-run container image build checks for pushes and pull requests targeting `main`. The end-to-end suite covers both STDIO and authenticated Streamable HTTP.

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

## Acknowledgements

Adapted fork work is credited in [ACKNOWLEDGEMENTS.md](ACKNOWLEDGEMENTS.md).

## License

This project is licensed under the MIT License. See `LICENSE` for details.
