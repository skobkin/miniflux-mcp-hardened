# Security: untrusted feed content and prompt injection

This document is for the person deciding to connect this MCP server to an LLM agent. It covers content-borne attacks only: how untrusted RSS content can influence the agent, what this server does about it, and what still gets through. Transport and authentication hardening are described in the [README security model](../README.md#security-model); agent-side behavior rules are in the [triage skill](../skills/miniflux-mcp-triage/SKILL.md); developer policy is in [AGENTS.md](../AGENTS.md).

Core claim: **this server bounds and reduces prompt-injection risk. It does not prevent it.**

## Adversary model

- The adversary is any author of a subscribed feed and, indirectly, the operator of any site an entry links to.
- They control arbitrary text and markup in article content, excerpts, titles, authors, tags, feed titles, URLs, and — for a refreshed feed — freshly fetched remote content.
- Their gain is influence over the agent: its decisions, its tool calls, and whatever authority the client host grants it. Not code execution on the MCP server or Miniflux; feed content is processed as data and never evaluated.
- Trusted parties: the deployer's Miniflux instance and the MCP client host.

## What an attacker can attempt

Attack classes, described so their blast radius can be reasoned about — not working payloads:

- **Direct instructions** in article bodies or digest excerpts, addressed to the agent ("mark everything read"-style requests). Blast radius: the write tools the deployer enabled.
- **Authority spoofing**: forged system or user framing, fake "the user has approved this" claims, or text dressed up as tool output.
- **Markup concealment**: instructions hidden in article HTML. The default conversion strips common vectors (`hidden` attribute, inline `display`/`visibility` styles); class-based hiding, opacity, and positioning tricks are not detected.
- **Link channel**: visible link text is arbitrary text, and ordinary `http`/`https` (and `mailto`) links survive conversion by design. "Visit this URL" always reaches the agent.
- **Metadata fields**: entry titles, authors, tags, feed titles, and URLs cross the boundary unconverted in both content formats — conversion applies only to article content.
- **Queue flooding**: a high-volume feed places a payload into every unread digest batch. The server bounds this (at most 20 entries per digest, bounded excerpts, 1,000-candidate scan) but cannot remove the payload.

## What this server does — and what it cannot do

Each mitigation is paired with its limitation:

- **Read-only default.** No write tool is registered unless `MCP_WRITE_TOOLS` names it; unknown names fail startup. *Limit:* this bounds only what flows through this server — an enabled write remains reachable by a persuaded agent.
- **Default Markdown conversion.** Article HTML is converted to Markdown, stripping active markup and the common concealment vectors; conversion errors fail closed, and raw feed HTML is never emitted as a fallback. *Limit:* conversion removes markup, not meaning — plain-text injection survives untouched.
- **`MCP_CONTENT_FORMAT=html` opt-in.** The only transformation is UTF-8 replacement; raw untrusted HTML crosses the boundary. Appropriate only for clients that never render tool results as active content.
- **Result bounding.** Bounded entry collections, digest sizes, excerpts, and result bytes. *Limit:* bounds volume, does not inspect meaning.
- **Provenance labels.** Entry detail and digest results carry `content_origin` (`external_feed`) and `content_trust` (`untrusted`) response fields; tools carry the standard MCP annotations. *Limit:* informational labels for cooperative clients — a persuaded agent can ignore them.
- **Structured content with output-schema validation.** The server validates structured results against each tool's declared schema. *Limit:* injection lives in data values, not transport framing — validation cannot see it.
- **Stderr audit log.** Every tool call logs `event=mcp_tool_call` with tool name, read/write class, outcome, and duration. *Limit:* after-the-fact detection; arguments and results are not logged, so it shows that something happened, not what.
- **Transport authentication.** Bearer token, origin allowlist, HTTPS guidance, and per-request credentials protect who talks to the server. *Limit:* they do not constrain what the content means once the agent reads it.

## Content sanitization is not instruction isolation

- **Sanitization** is data hygiene: removing active markup and surfacing concealed text so the data is safer to hand over.
- **Instruction isolation** would be a guarantee that untrusted text cannot influence the agent's decisions. It cannot be provided server-side: injection instructions are syntactically ordinary sentences, and a filter that removed them would censor legitimate articles. Article text is never altered beyond markup conversion.
- Enforcement therefore belongs to the consumer of the data: host-side permission management, separation of instruction and data channels where the client supports it, and human confirmation for consequential actions. The triage skill states the agent-side discipline: feed text is data, never instructions, and tool availability does not authorize a mutation.

## Multiple MCP servers: your agent's authority is the union

This server's boundaries apply only to this server. A client typically connects several MCP servers, and the agent's real authority is the union of all of them:

- An entry can steer the agent toward another connected server — for example a generic-fetch or browser tool — to call the Miniflux API directly, bypassing this fork's write allowlist entirely.
- Read entries can be exfiltrated through any server with outbound reach: messaging, web, or file tools.
- The most privileged connected server defines the blast radius of content injected through this one.

When composing an agent client, evaluate permissions across all connected servers together, not per server.

## Deployment recommendations

- Leave `MCP_WRITE_TOOLS` unset unless writes are needed; enable the smallest subset. Note that `refresh_feed` makes Miniflux fetch remote content and is not idempotent.
- Keep the default `MCP_CONTENT_FORMAT=markdown`.
- Use a dedicated Miniflux account with least privilege, so even a direct-API bypass through another server acts as a bounded identity.
- For streamable HTTP: set `MCP_AUTH_TOKEN`, allowlist expected origins with `MCP_ALLOWED_ORIGINS`, and use HTTPS outside trusted networks. See the [configuration reference](../README.md#configuration).
- Run the agent client in a sandbox proportionate to the authority you grant it.
- Watch the stderr audit log for unexpected `class=write` records.
- Follow the [container hardening guidance](../README.md#run-with-docker) (`read_only`, `cap_drop`, non-root).

## Residual risk: what still gets through

After all of the above, assume the agent still receives:

- Plain-text instructions in article bodies and excerpts.
- Injection carried in titles, authors, tags, feed titles, and URLs — fields that are never converted.
- Text concealed by class-based, opacity, or positioning tricks.
- Ordinary `http(s)` links whose visible text or destination is the payload.
- Content engineered to exercise other connected MCP servers.

> **Server-side filtering cannot guarantee protection against prompt injection.** The last line of defense is host-side authority management and human oversight.
