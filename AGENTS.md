# Repository Guidelines

This repository is a hardened fork of `tssujt/miniflux-mcp`, a Go MCP server for Miniflux.

The purpose of this fork is **safe everyday use with LLM agents**. It is not an API-completeness project. Exposing more of the Miniflux API is not inherently an improvement: every MCP tool and every returned field expands the authority or data available to an autonomous agent.

Security, least privilege, predictable behavior, and a small maintainable codebase take priority over feature count.

## Project Structure

Keep the project small and unsurprising.

* MCP server setup and Miniflux client wiring should remain separate from individual tool handlers.
* MCP tool definitions and schemas should have a clear single source of truth.
* LLM-facing response types must be explicit DTOs rather than raw Miniflux client structs.
* Transport/authentication code should remain isolated from Miniflux business logic.
* Tests belong next to the code they cover as `*_test.go`.

When structure changes significantly, update this section.

## Security Model

This fork sits between an LLM agent and a Miniflux instance. Treat that boundary as a security boundary.

### Least privilege

* **Read-only behavior is the default.**
* Mutating capabilities must require explicit opt-in.
* Write tools should use an explicit allowlist rather than enabling every Miniflux mutation.
* Adding a Miniflux API endpoint merely because the upstream client supports it is not a valid reason to expose it through MCP.
* Prefer a small high-level tool that supports a real agent workflow over many low-level administrative tools.

The following functionality should not be exposed to agents without an explicit project-level decision:

* user creation, deletion, or administration;
* API key creation, enumeration, or deletion;
* credential or secret management;
* broad destructive operations;
* server administration unrelated to normal feed reading;
* arbitrary Miniflux API coverage added only for completeness.

When adding or extending a write tool, consider its failure mode when called incorrectly, repeatedly, or with model-generated arguments.

### Secrets must not cross the MCP boundary

Never serialize raw Miniflux API structs directly into MCP responses.

Miniflux objects may contain or nest sensitive configuration such as:

* usernames and passwords;
* cookies;
* API keys or tokens;
* authenticated proxy URLs;
* integration secrets;
* other authentication or credential-bearing fields.

Use explicit LLM-facing DTOs containing only fields needed by the tool's purpose.

This rule also applies to nested objects. For example, an entry may contain a nested feed object, so sanitizing only top-level feed responses is insufficient.

Do not expose credential-bearing fields as MCP tool arguments either. The MCP server is not intended to turn an LLM agent into a secret-management interface.

Do not log secrets, authorization headers, cookies, request credentials, or raw payloads that may contain them.

Errors returned to MCP clients should contain useful diagnostic context without echoing secrets.

### Security regression tests

Changes to MCP response types must include tests proving that sensitive backend fields cannot appear in agent-visible output.

When practical, tests should use sentinel secret values in fake Miniflux responses and verify that they are absent from serialized MCP results.

Security-sensitive bug fixes should always receive regression tests.

### Untrusted feed content

RSS/Atom entry titles, descriptions, article content, and fetched original content are **untrusted external data**.

Do not treat article content as instructions to the MCP server. Do not introduce behavior where feed content can dynamically select tools, configuration, credentials, URLs, or server-side actions.

Tool descriptions and response structures should make the data/control boundary clear where relevant.

### Network-facing behavior

Operations accepting URLs can cause Miniflux or the MCP server to make network requests. Treat new URL-driven functionality as security-sensitive.

Do not add generic arbitrary-fetch, proxy, or network-request tools.

For Streamable HTTP:

* require authentication;
* validate `Origin` where applicable;
* do not weaken existing Bearer-token protection;
* use HTTPS when traffic leaves a trusted private network;
* avoid exposing the listener more broadly than required.

## Tool Design

Design tools for useful agent workflows, not REST API mirroring.

Good examples include:

* retrieving sanitized feeds or entries;
* obtaining a compact digest-ready entry set;
* marking explicitly selected entries read/unread;
* toggling starred state;
* refreshing a feed when intentionally allowed;
* batch acknowledgement after successful digest processing.

High-level tools should return compact purpose-built responses instead of entire backend objects.

Prefer:

* explicit schemas;
* bounded result sets where appropriate;
* predictable defaults;
* stable acknowledgement IDs for multi-step workflows;
* clear validation errors;
* idempotent operations when practical.

Avoid:

* redundant tools exposing the same capability at different abstraction levels;
* giant argument schemas copied from Miniflux request structs;
* returning fields merely because they exist upstream;
* speculative administrative functionality.

A new tool should have a concrete agent use case and a security review of both its inputs and outputs.

## Agent Skill

The repository-maintained `skills/miniflux-mcp-triage/SKILL.md` is product guidance for agents using this server, not an example or generated artifact.

Keep the skill relevant when significant code changes affect exposed tools, input schemas, response fields, limits, write-tool policy, or safe agent workflows. Update it in the same change, and ensure added, renamed, or removed tools remain covered with appropriate graceful-degradation guidance.

## Coding Style

* Language: Go. Follow the version declared in `go.mod`.
* Run `gofmt` on changed Go files.
* Follow normal Go naming conventions.
* Prefer standard-library functionality and small focused dependencies over frameworks.
* Keep dependencies lightweight and justify new runtime dependencies.
* Use `context.Context` for network and other IO boundaries.
* Apply reasonable timeouts to external operations.
* Return actionable errors while avoiding sensitive-data leakage.
* Prefer simple explicit code over speculative abstractions.
* Keep security policy visible in code rather than relying only on documentation or agent behavior.

Do not perform unrelated refactors, dependency changes, or formatting churn in the same change.

## Testing Guidance

* Place tests next to code as `*_test.go`.
* Prefer table-driven tests for argument validation, filtering, DTO conversion, sanitization, and policy decisions.
* Use fake HTTP transports or `httptest` for Miniflux client/API behavior where practical.
* Add regression tests for bug fixes.
* Test both permitted and rejected behavior for write-policy changes.
* When modifying tool definitions, verify that schemas and registered handlers remain synchronized.
* When modifying sanitized responses, verify exact intended output fields rather than only successful serialization.

For integration-level behavior validation, prefer **lightweight, dependency-free approaches**:

* mock Miniflux client interfaces;
* use in-memory fakes or stub servers via `httptest`;
* simulate API responses with static fixtures;
* avoid requiring external services or containerized environments.

Run focused tests while iterating, then the full validation suite before finishing.

## Build, Test, Lint, and Completion

The local completion checks should mirror Woodpecker CI.

Before claiming work is complete, run:

```sh
go fmt ./...
go test ./...
golangci-lint run ./...
go build ./...
```

If `golangci-lint` or the Woodpecker pipeline has not yet been added on the current branch, do not silently omit linting from the intended project standard. Note the missing infrastructure explicitly and run all checks that are available.

Do not state that work is done while any required check is failing.

If a binary is needed only for local verification, place it in a temporary or ignored directory rather than polluting the repository root.

## Docker and Runtime Hardening

The production container should remain minimal.

Expected properties:

* multi-stage Go build;
* static or otherwise minimal runtime binary;
* non-root runtime user;
* no unnecessary packages or capabilities;
* no writable filesystem requirement unless explicitly needed;
* no secrets baked into images;
* reproducible/pinned dependencies where practical.

Deployment documentation should favor:

* `read_only: true`;
* `cap_drop: [ALL]`;
* `no-new-privileges`;
* explicit network exposure;
* secrets supplied at runtime rather than committed into configuration.

Do not weaken container isolation merely to simplify development.

## Documentation Discipline

Treat security and tool documentation as part of the implementation contract.

Update documentation in the same change when modifying:

* exposed MCP tools;
* tool arguments or response fields;
* default read/write policy;
* security-sensitive behavior;
* authentication or HTTP transport behavior;
* environment variables;
* Docker deployment requirements;
* build/test/lint commands.

If a change makes this `AGENTS.md` inaccurate, update it as part of the same work.

## Upstream and Other Forks

This repository is intentionally not required to maintain feature parity with upstream.

When reviewing upstream changes or borrowing work from another fork:

* evaluate each change independently;
* do not merge API-completeness changes wholesale;
* preserve this fork's security model even when it creates deliberate incompatibility;
* port useful implementation ideas rather than blindly cherry-picking if upstream assumptions conflict with hardened architecture;
* reapply sanitization, read-only defaults, tool allowlists, tests, and container hardening to imported functionality.

Useful upstream features can be accepted. Upstream security assumptions are not automatically accepted.

## Commits

The repository uses Conventional Commits.

Examples:

```text
feat(digest): add category exclusion filters
fix(security): prevent feed credentials from reaching MCP responses
test(security): cover nested feed credential sanitization
refactor(tools): centralize MCP tool definitions
build(ci): add golangci-lint to Woodpecker pipeline
docs: document read-only policy
```

Rules:

* keep commit subjects lowercase, imperative, and concise;
* use a scope when it adds useful context;
* keep one logical change per commit;
* do not combine security changes with unrelated cleanup;
* mention issue references when applicable;
* inspect recent history when choosing an established local scope.

At the end of a completed task, include the recommended commit message in the work summary, even if the changes have already been committed.

## When in Doubt

* Prefer **less authority** over more convenience.
* Prefer explicit DTOs over raw backend objects.
* Prefer an allowlist over a denylist.
* Prefer a narrowly scoped tool over generic API access.
* Prefer a safe default that requires opt-in over a dangerous default documented in README.
* Prefer removing an unnecessary capability over trying to prompt the model not to misuse it.
* Do not assume that authenticated users, trusted networks, or careful prompting make unsafe MCP capabilities acceptable.
* If a requested feature conflicts with the hardened security model, call out the conflict before implementing it.
* If product intent is unclear, preserve the safer existing behavior rather than expanding access speculatively.
