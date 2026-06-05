# Step 1.2 — Provider/API implementations (`pkg/ai`)

Source plan: `PLAN.md` Phase 1, Step 1.2.

Goal: add the `openai-codex` provider adapter for `pkg/ai`, using the Step 1.1 stream protocol. MVP is SSE-only, non-interactive streaming with OAuth/Codex bearer tokens resolved either from `StreamOptions.APIKey` or dynamically from the auth store (`~/.pi/agent/auth.json`). WebSocket support remains part of Step 1.2, but as a later, clearly separate milestone once the SSE path is stable.

This breakdown is intentionally commit-oriented: each step should land as one reviewable, functional milestone with a clean concern boundary. Avoid Step 1.1-style micro-splitting.

## Original Pi references

Use these upstream files from `https://github.com/earendil-works/pi` as references:

- `packages/ai/src/providers/openai-codex-responses.ts`
- `packages/ai/src/providers/openai-responses-shared.ts`
- `packages/ai/src/providers/transform-messages.ts`
- `packages/ai/src/providers/simple-options.ts`
- `packages/ai/src/utils/oauth/openai-codex.ts`
- `packages/coding-agent/src/core/auth-storage.ts`
- `packages/coding-agent/src/config.ts`

## Hard constraints

- Support exactly one provider ID: `openai-codex`.
- Support exactly one API dialect: `openai-codex-responses`.
- Credentials are OAuth/Codex subscription credentials only.
- Keep the field name `StreamOptions.APIKey` as the override bearer token. If not provided, dynamically resolve and refresh the token from auth storage.
- Do not add `CODEX_ACCESS_TOKEN`, `OPENAI_API_KEY`, direct OpenAI API-key support, runtime API-key override, env fallback, or models.json fallback.
- Do not register unsupported providers or extension-provided provider backends.
- Preserve reasoning signatures, text signatures, and tool-call IDs needed for future-turn replay.
- Provider failures must resolve through `AssistantStream` error events; no panics, no leaked goroutines.


## Planned file layout

- `pkg/ai/model_registry.go`
- `pkg/ai/codex.go`
- `pkg/ai/codex_sse.go`
- `pkg/ai/codex_auth.go`
- `pkg/ai/codex_ws.go` later

---

## Step 1.2.1: Static Codex model registry

**Commit shape:** add the reviewed Codex-only model registry entries required by the adapter and tests, but no provider transport yet.

Scope:

- Add `pkg/ai/model_registry.go`.
  - Define the static Codex-only model registry entries with these exact values:
    * **gpt-5.3-codex-spark**: Name = `"GPT-5.3 Codex Spark"`, Input = `["text"]`, Cost = `{Input: 1.75, Output: 14.0, CacheRead: 0.175, CacheWrite: 0.0}`, ContextWindow = `128000`, MaxTokens = `128000`.
    * **gpt-5.4**: Name = `"GPT-5.4"`, Input = `["text", "image"]`, Cost = `{Input: 2.5, Output: 15.0, CacheRead: 0.25, CacheWrite: 0.0}`, ContextWindow = `272000`, MaxTokens = `128000`.
    * **gpt-5.4-mini**: Name = `"GPT-5.4 mini"`, Input = `["text", "image"]`, Cost = `{Input: 0.75, Output: 4.5, CacheRead: 0.075, CacheWrite: 0.0}`, ContextWindow = `272000`, MaxTokens = `128000`.
    * **gpt-5.5**: Name = `"GPT-5.5"`, Input = `["text", "image"]`, Cost = `{Input: 5.0, Output: 30.0, CacheRead: 0.5, CacheWrite: 0.0}`, ContextWindow = `272000`, MaxTokens = `128000`.
  - All entries must set `Provider` to `ProviderIDOpenAICodex`, `API` to `APIIDOpenAICodexResponses`, `BaseURL` to `"https://chatgpt.com/backend-api"`, `Reasoning` to `true`, `Headers` to `nil`/empty, and `ThinkingLevelMap` mapping `ModelThinkingLevelXHigh` to `"xhigh"` and `ModelThinkingLevelMinimal` to `"low"` (using string pointer helpers).
- Keep the registry Codex-only. No models.json loading, aliases, custom providers, or extension-provided models.

Keep out of this step:

- provider registration
- auth storage
- request-body conversion
- SSE/WebSocket transport

Acceptance:

- The static model registry exposes only the reviewed Codex models listed above.
- Every entry uses `ProviderIDOpenAICodex` and `APIIDOpenAICodexResponses`.
- Tests prove no unsupported model/provider source is introduced.

Why this is one commit:

- It is a prerequisite for request shaping, pricing, and provider tests.
- It keeps model data review separate from provider behavior.

---

## Step 1.2.2: Codex provider surface and registration

**Commit shape:** introduce the Codex adapter entrypoints and option types, but no real network transport yet.

Scope:

- Add the Codex-specific provider-facing surface in `pkg/ai`.
- Define `CodexResponsesOptions` with Codex-only controls:
  - `ReasoningEffort`
  - `ReasoningSummary`
  - `ServiceTier`
  - `TextVerbosity`
- Keep the defaults aligned with upstream request-shaping behavior:
  - `TextVerbosity` defaults later in request building to `"low"`
  - `ReasoningSummary` defaults later in request building to `"auto"`
- Add `StreamOpenAICodexResponses(...)` and `StreamSimpleOpenAICodexResponses(...)`.
- Before transport is implemented, make the new stream entrypoints return a pre-completed error stream instead of panicking or silently succeeding.
- Add `RegisterBuiltinProviders()` that registers only `APIIDOpenAICodexResponses`.
- Make the adapter reject wrong `model.Provider` / `model.API` with an error stream.
- Make `StreamSimple...` map Step 1.1 `SimpleStreamOptions` into Codex options using `BuildBaseOptions` and `ClampThinkingLevel`.
- Keep `TextVerbosity` out of `SimpleStreamOptions`; it remains a Codex-specific option whose default is applied in request shaping.

Keep out of this step:

- auth storage
- request-body conversion
- SSE parser
- WebSocket support

Acceptance:

- Builtin registration exposes only `openai-codex-responses`.
- `StreamSimple...` correctly maps simple reasoning options into `CodexResponsesOptions`.
- The new entrypoints fail safely with an error stream until transport exists.
- No unsupported provider registration path is introduced.

Why this is one commit:

- It creates the public adapter seam the rest of Step 1.2 builds on.
- It is meaningful on its own, but small enough to review without transport noise.

---

## Step 1.2.3: Codex auth store and token resolution

**Commit shape:** add the credential source for the provider, with refresh support, but still no streaming.

Scope:

- Implement Codex-only auth storage in `pkg/ai/codex_auth.go` for `~/.pi/agent/auth.json`, honoring `PI_CODING_AGENT_DIR`.
- Load only the `openai-codex` entry.
- Accept only OAuth credentials (`type: "oauth"`).
  - If token is expired, refresh it against `https://auth.openai.com/oauth/token`, persist updated credentials atomically, then return the new access token.
  - Use cross-process locking around refresh/write by locking a stable companion file (`auth.json.lock`) using Unix file locking (`flock`) suitable for the macOS/Linux target environment. Re-read `auth.json` after acquiring the lock to check if another process already completed the refresh.
- Expose a thread-safe token resolver/refresher function that can be called internally by `pkg/ai` streaming functions and the later CLI login flow.
- Port the fixed upstream OAuth constants needed for refresh/login compatibility.

Keep out of this step:

- browser/device login CLI wiring
- provider HTTP requests
- JWT/header handling

Acceptance:

- Temporary-file tests cover stored-token read, expired-token refresh, unsupported credential-type rejection, and cross-process refresh recovery behavior.
- Tests verify there is no env/API-key fallback even if common OpenAI env vars are set.
- File permissions/path behavior match the plan (`0700` dir, `0600` file when writing).

Why this is one commit:

- Auth resolution is a complete subsystem with separate failure modes and tests.
- Keeping it separate avoids mixing filesystem/OAuth behavior into transport work.

---

## Step 1.2.4: Codex request shaping and replay conversion

**Commit shape:** build the outbound Codex request correctly from Step 1.1 messages/tools, but do not send it yet.

Scope:

- Implement JWT account ID extraction from the OAuth access token.
- Implement Codex URL/header helpers:
  - default base URL `https://chatgpt.com/backend-api`
  - SSE URL normalization to `/codex/responses`
  - required headers: `Authorization`, `chatgpt-account-id`, `originator` (set to `pi`), `User-Agent` (custom string identifying the client), `OpenAI-Beta` (set to `"responses=experimental"` for SSE, and deleted or adjusted for WS connection handshake if necessary).
- Implement `buildCodexRequestBody(model, context, opts)`.
- Apply the upstream request-body defaults in the builder:
  - `text.verbosity` defaults to `"low"`
  - `reasoning.summary` defaults to `"auto"`
- Implement message conversion for the Codex Responses API:
  - user text/image blocks
  - assistant text/thinking/tool-call replay
  - tool-result conversion
  - same-model reasoning signature preservation
  - text signature preservation
  - synthetic tool results for orphaned tool calls
  - image placeholders for non-vision models
- Implement tool schema conversion for `[]ToolDefinition`.
- Add a prompt-cache key normalizer for `SessionID` compatible with upstream intent.

Keep out of this step:

- actual HTTP request/streaming
- SSE frame parsing
- final stream event emission

Acceptance:

- Tests validate request JSON shape, defaults, and header construction.
- Tests cover JWT account ID extraction including robust handling of malformed tokens (invalid segment count, invalid base64/JSON, missing auth claim, non-string account ID, with errors kept secret-safe).
- Tests cover replay conversion for user messages, assistant messages, tool results, tool schemas, and reasoning/text signatures.
- Tests verify no unsupported credential/provider fields are serialized into the request body.

Why this is one commit:

- Request shaping is the main protocol-compatibility boundary.
- It is large enough to matter, but still isolated from network/stream complexity.

---

## Step 1.2.5: SSE transport and Codex event normalization

**Commit shape:** make the adapter talk to Codex over SSE and produce a normalized provider event stream, but not yet the full final `AssistantMessage` assembly logic.

Scope:

- Implement the SSE POST path with `net/http`.
  - Require a non-empty OAuth token (either passed in `opts.APIKey` or resolved dynamically from the auth store); fail fast if missing or if token resolution/refresh fails. If a non-empty `opts.APIKey` is passed, it must be used authoritatively and the client must never fall back to `auth.json` during connection retries or token refreshes.
  - Apply the response-header timeout (`10_000ms` default) specifically to the initial response headers via `http.Transport.ResponseHeaderTimeout` in a custom `http.Client`. Do not apply this timeout as a request/context deadline, which would prematurely terminate the long-lived SSE streams.
- Implement retry behavior for retryable transport/status failures:
  - 429/500/502/503/504
  - `retry-after-ms` / `retry-after`
  - max retry delay cap
  - no retry for terminal usage/quota errors
- Respect cancellation during request, response read, and retry sleep.
- Wire the test hooks with upstream-compatible timing:
  - `OnPayload` runs before JSON serialization and may replace the request body
  - `OnRequest` receives the outbound `*http.Request` plus raw body bytes
  - `OnResponse` receives the HTTP response
- Implement a streaming SSE parser.
- Implement Codex event normalization (`error`, `response.failed`, `response.done` / `response.completed` / `response.incomplete` normalization), ensuring that normalized terminal events preserve all raw payload fields (usage, cached-token counts, response ID, stop reason, and error details) needed for later stream assembly.

Keep out of this step:

- final `AssistantMessage` block assembly
- usage/cost mapping
- service-tier pricing

Acceptance:

- `httptest.Server` fixtures cover success, timeout, retryable errors, terminal 429, malformed SSE JSON, and cancellation.
- Tests verify that normalized terminal events preserve the full raw payload details needed for usage, cached tokens, response ID, stop reasons, and error mapping.
- Normalized event iteration stops correctly on Codex terminal events.
- No real OpenAI/ChatGPT network calls.

Why this is one commit:

- It delivers a working transport layer with its own tests.
- It avoids mixing socket-level behavior with higher-level message assembly logic.

---

## Step 1.2.6: Responses stream assembly, stop reasons, and diagnostics

**Commit shape:** convert normalized Codex events into Step 1.1 `AssistantStream` events and the final `AssistantMessage`.

Scope:

- Port the `processResponsesStream` behavior into Go.
- Map Responses events into Step 1.1 events:
  - thinking start/delta/end
  - text start/delta/end
  - toolcall start/delta/end
- Implement the best-effort streaming JSON parser for tool-call arguments.
- Finalize message content blocks, including cleanup of scratch `partialJson` state.
- Map usage, cached-token subtraction, `ResponseID`, and stop reasons.
- Apply `CalculateCost(model, usage)` and Codex service-tier multipliers:
  - `flex` = 0.5x
  - `priority` = 2x, except `gpt-5.5` = 2.5x
  - default = 1x
- Convert transport/protocol failures into final partial assistant messages with `ErrorMessage`, `StopReasonError` / `StopReasonAborted`, and safe diagnostics.
- Implement the friendly usage-limit error mapping needed for ChatGPT plan/quota failures without leaking sensitive response data.
- Ensure sensitive values never appear in errors/diagnostics.

Acceptance:

- Synthetic event-sequence tests assert exact emitted Step 1.1 event order and final message shape.
- Tests cover stop-reason mapping, `toolUse` override, usage accounting, pricing multipliers, final argument parsing, partial cleanup, and friendly usage-limit errors.
- `Complete(...)` and streaming consumption agree on the same canonical final message.

Why this is one commit:

- This is the behavioral heart of the adapter.
- It is substantial, but focused on one concern: turning provider events into Pi/Go stream semantics.

---

## Step 1.2.7: End-to-end SSE provider milestone

**Commit shape:** wire the pieces together into one complete Codex SSE provider and prove it with integration-style tests.

Scope:

- Connect auth resolution (prioritize `opts.APIKey`, fall back to reading/refreshing from `auth.json` if empty), request shaping, SSE transport, and stream assembly into the registered provider implementation.
- Add end-to-end fixture tests that exercise the full request -> SSE -> `AssistantMessage` path.
- Add provider-registration tests at the `pkg/ai` level:
  - only `openai-codex-responses` is registered
  - `Stream` dispatch works
  - `StreamSimple` dispatch works
  - missing registration still returns Step 1.1 error stream
  - wrong API/provider model returns an error stream
- Add one complete SSE fixture including:
  - `response.created`
  - reasoning deltas
  - text deltas
  - function call arguments
  - `response.completed` with usage/cached tokens

Acceptance:

- One end-to-end local fixture proves request headers/body, stream event order, final message, usage/cost, and `StopReasonToolUse`.
- Tests prove no unsupported credential path is used.
- At this point, Step 1.2 has a complete MVP: Codex over SSE with OAuth token input.

Why this is one commit:

- It is the first user-visible functional milestone for Step 1.2.
- The commit is reviewable because the lower-level pieces have already been separated.

---

## Step 1.2.8: WebSocket and cached-WebSocket follow-up

**Commit shape:** add the non-MVP transport path only after the SSE provider is stable.

Scope:

- Add `github.com/coder/websocket` to `go.mod`.
- Add `websocket`, `websocket-cached`, and `auto` transport handling.
- Implement WebSocket header construction and request IDs.
- Implement connection establishment timeout and idle timeout behavior.
- Add session-scoped reusable connection caching with expiry.
- Add cached continuation state:
  - last request body
  - last response ID
  - last response items
  - delta-input detection using prior request + response state
- Add session-scoped fallback to SSE after WebSocket transport failures.
- Preserve the upstream rule:
  - fail over to SSE only if WebSocket failed before any output was emitted
  - once output started, surface the transport error rather than replaying

Acceptance:

- Local fake WebSocket tests cover full request, cached continuation, connection reuse, idle expiry, pre-start fallback to SSE, and post-start no-fallback behavior.
- `auto` prefers WebSocket when healthy and SSE when the session is marked degraded.

Why this is one commit:

- WebSocket transport is real additional functionality, not just a detail inside SSE.
- It deserves its own review and should not muddy the MVP commit.

---

## Security and completeness checklist

Before marking Step 1.2 done, verify:

- No token appears in logs, diagnostics, snapshots, or test failure output.
- `auth.json` writes use `0600`; parent dir uses `0700`.
- No environment variable can provide Codex/OpenAI credentials.
- No OpenAI pay-as-you-go API-key path is accepted.
- No unsupported provider can be registered by builtin setup.
- Cancellation closes response bodies and stops parser goroutines.
- Cross-process auth refresh locking is released on success and failure.

## Definition of done

Step 1.2 is complete when:

- `pkg/ai` exposes a registered `openai-codex-responses` adapter.
- SSE Codex streaming works end-to-end against local fixtures using OAuth/Codex bearer tokens resolved from either `StreamOptions.APIKey` or dynamically from auth storage.
- Request conversion preserves text, images, tools, tool results, reasoning signatures, text signatures, tool-call IDs, usage, and stop reasons.
- The implementation has no unsupported provider/API-key/env fallback path.
- WebSocket support is either fully implemented per Step 1.2.8 or explicitly deferred without exposing a broken transport mode.
