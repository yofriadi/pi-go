# PLAN: pi-go implementation roadmap

## Source requirements

This plan consolidates `SPEC.md` and `RESEARCH.md` into the execution roadmap for recreating `pi` in Go.

Objective: build a terminal-native Go coding agent that supports non-interactive print mode, branchable JSONL sessions, structured context compaction, an interactive retained-mode TUI, subprocess JSON-RPC extensions, and Agent Skills metadata execution.

Hard provider boundary:
- Support only OpenAI Codex subscription/OAuth provider flows.
- Public provider ID: `openai-codex`.
- Required API dialect: `openai-codex-responses`.
- Never add direct OpenAI API-key usage, `CODEX_ACCESS_TOKEN`, `OPENAI_API_KEY`, Anthropic, Gemini, Mistral, Bedrock, OpenRouter, Copilot, OpenCode, provider aliases, or custom extension providers.

Required commands:
- Build: `go build -o bin/pi ./cmd/pi`
- Test: `go test -v ./pkg/...`
- Vet: `go vet ./pkg/...`
- Format: `go fmt ./pkg/...`
- Dev run: `go run ./cmd/pi`

## Key findings from current repository

Current code exists only under `pkg/ai` plus root project files. `go.mod` declares `go 1.25`.

### Completed work

**Step 1.1 is complete.** All files below exist with passing tests (~2455 lines in `ai_test.go`):

- `pkg/ai/ai.go`: core protocol identifiers (`Role`, `APIID`, `ProviderID`, `StopReason`, `ThinkingLevel`, `Transport`, `CacheRetention`), `Usage`, `UsageCost`, `Context` with polymorphic JSON, `ToolDefinition`, stream event types.
- `pkg/ai/model.go`: `Model` struct, `GetSupportedThinkingLevels`, `ClampThinkingLevel`, `CalculateCost`, `ModelsAreEqual`.
- `pkg/ai/options.go`: `StreamOptions` (with `APIKey`, `OnRequest`, `OnResponse`, `OnPayload` hooks), `SimpleStreamOptions`, `BuildBaseOptions`, `AdjustMaxTokensForThinking`, `ThinkingBudgets`.
- `pkg/ai/stream.go`: `AssistantStream` with bounded push queue, drain goroutine, `Push`/`End`/`Error` producer API, `Events()`/`Result()` consumer API, deep-copy event isolation, stop-reason validation.
- `pkg/ai/registry.go`: `RegisterApiProvider`, `GetApiProvider`, `GetApiProviders` (sorted), `ClearApiProviders`, API-mismatch guard wrapping, validation on registration.
- `pkg/ai/dispatch.go`: `Stream`, `Complete`, `StreamSimple`, `CompleteSimple` dispatching by `model.API`.
- `pkg/ai/messages.go`: `Message` interface, `UserMessage`, `AssistantMessage`, `ToolResultMessage`, content block types (`TextContent`, `ThinkingContent`, `ImageContent`, `ToolCall`), `AssistantMessageDiagnostic`, full custom JSON marshal/unmarshal with role injection, content type discrimination, and validation.
- `pkg/ai/deepcopy.go`: deep-copy methods for all message and content types, recursive `deepCopyValue` for maps/slices.
- `pkg/ai/ai_test.go`: table tests covering JSON round-trips, content type discrimination, deep-copy isolation, stream event ordering, Result() blocking, Push after done, concurrent Result(), queue overflow, registry dispatch, API-mismatch guard, stop-reason partitioning, and race detection.

### Active work

**Step 1.2 is the current active step.** A commit-oriented sub-step breakdown exists in `STEP-1-2.md` (8 sub-steps: model registry, provider surface, auth store, request shaping, SSE transport, stream assembly, end-to-end milestone, WebSocket follow-up). No Step 1.2 code has been written yet.

### Missing top-level implementation areas from the spec

- `cmd/pi`
- `pkg/agent`
- `pkg/tools`
- `pkg/session`
- `pkg/tui`
- Codex OAuth/auth store and stream transport implementation (Step 1.2)
- Static Codex model registry entries (Step 1.2)
- Resource loader, skills, and extension RPC layer (Phase 4)

## Architecture decisions

Recommended approach: build in compatibility layers from the bottom up, preserving Pi-visible behavior while staying Go-native internally.

Chosen decisions:
- CLI: use standard `flag` initially; introduce Cobra only if subcommands become too cumbersome.
- AI dispatch: keep API-adapter registry keyed by `model.API`, not a single provider client.
- Provider scope: keep a static reviewed Codex-only model registry with `gpt-5.3-codex-spark`, `gpt-5.4`, `gpt-5.4-mini`, and `gpt-5.5`.
- Transport: implement Codex SSE first; add `github.com/coder/websocket` for WebSocket and cached WebSocket support when session reuse is ready.
- Sessions: use Pi-compatible version 3 JSONL tree entries, not flat transcripts.
- Compaction: summarize with the active constrained model; use actual usage when available and chars/4 fallback otherwise.
- Tools: implement `read`, `write`, `edit`, and `bash` first with pluggable operations. `grep`, `find`, and `ls` remain optional and must not be exposed by default.
- Search tools: require `rg` and `fd`; fail explicitly if missing. Do not add Go-native fallback searches.
- TUI: custom retained-mode differential renderer writing to stdout with ANSI cursor controls, backbuffer line comparisons, and synchronized output (`CSI ?2026h`/`CSI ?2026l`). Do not use Bubble Tea — the spec explicitly requires a custom renderer that preserves native terminal scrollback.
- Extensions: prefer subprocess JSON-RPC. Explicitly forbid provider/model/OAuth registration through extensions.

Tradeoff resolved: exact TypeScript runtime parity is not required when it adds complexity, but data shapes, CLI-visible behavior, session compatibility, tool schemas, and provider boundaries are compatibility-critical.

## Implementation phases

### Phase 1 — Minimal non-interactive coding agent

Goal: `pi -p "..."` streams assistant text/thinking, executes tool batches, and exits at natural stop.

#### Step 1.1 — Complete unified AI stream protocol in `pkg/ai`

All files implemented and tested. See "Completed work" section above for details. Detailed sub-step breakdown in `STEP-1-1.md`.

#### Step 1.2 — OpenAI Codex provider/API implementation

Detailed commit-oriented sub-step breakdown in `STEP-1-2.md` (8 sub-steps). No code written yet.

Modify/add:
- `pkg/ai/codex.go`
- `pkg/ai/codex_sse.go`
- `pkg/ai/codex_ws.go` later
- `pkg/ai/auth.go` or `pkg/auth` if separation becomes clearer
- `pkg/ai/model_registry.go`
- `pkg/ai/*_test.go`

Requirements:
- Register only `openai-codex-responses`.
- Load OAuth/Codex auth from Pi `auth.json`; refresh atomically and securely.
- Store credential directories as `0700`, files as `0600`.
- Do not read `CODEX_ACCESS_TOKEN` or `OPENAI_API_KEY` as bypasses.
- Preserve `StreamOptions.apiKey` name for per-request OAuth bearer token supplied by `getApiKey(provider)` hook.
- Extract `chatgpt-account-id` from OAuth access-token JWT.
- Set `Authorization`, `chatgpt-account-id`, `originator: pi`, `User-Agent`, and transport-specific `OpenAI-Beta` headers.
- Implement SSE first with response-header timeout and robust event parsing.
- Preserve reasoning item IDs/signatures.
- Add WebSocket and cached WebSocket only after SSE path is stable.
- Provide debug hooks for inspecting request payloads/responses in tests without logging secrets.

Verification:
- `httptest.Server` SSE tests for request payloads, headers, deltas, tool calls, errors, cancellation, and final result.
- Auth tests for permissions, atomic write/refresh, malformed token handling, and no env-var bypass.
- `go test -v ./pkg/ai`

#### Step 1.3 — Core agent loop in `pkg/agent`

Add:
- `pkg/agent/agent.go`
- `pkg/agent/events.go`
- `pkg/agent/messages.go`
- `pkg/agent/tools.go`
- `pkg/agent/hooks.go`
- `pkg/agent/queue.go`
- `pkg/agent/*_test.go`

Requirements:
- Define `AgentMessage` superset for UI/custom messages.
- Convert through `convertToLlm([]AgentMessage) []ai.Message` before every provider request.
- Add context transform hook for compaction/pruning.
- Forward streaming assistant partial updates as agent events.
- Execute tool calls as batches.
- Run tools in parallel by default, unless global/per-tool sequential mode requires ordering.
- Emit `tool_execution_end` in completion order for parallel execution.
- Append final tool-result messages in assistant source order.
- Terminate early only when all tool results in a batch have `Terminate=true`.
- Support steering and follow-up queues.
- Support queue drain modes `all` and `one-at-a-time`.
- Hooks: `prepareNextTurn`, `shouldStopAfterTurn`, `beforeToolCall`, `afterToolCall`, `getSteeringMessages`, `getFollowUpMessages`, `getApiKey`.

Verification:
- Tests with fake local stream functions and local tool spies, not third-party mocking frameworks.
- Cover parallel completion order vs final result order, sequential override, all-terminate rule, steering insertion, follow-up drain modes, cancellation, and hook errors.
- `go test -v ./pkg/agent ./pkg/ai`

#### Step 1.4 — Built-in tools in `pkg/tools`

Add:
- `pkg/tools/tools.go`
- `pkg/tools/read.go`
- `pkg/tools/write.go`
- `pkg/tools/edit.go`
- `pkg/tools/bash.go`
- `pkg/tools/schema.go`
- `pkg/tools/*_test.go`

Requirements:
- Tool definitions match Pi names and schemas unless deliberately documented.
- Use pluggable operation interfaces for filesystem and shell execution.
- `read`: path, optional 1-indexed offset/limit, default truncation 2000 lines and 50 KiB.
- `write`: full content, recursively create parents.
- `edit`: `edits: [{oldText,newText}]`, JSON-string `edits`, legacy single edit, match original file, reject overlapping/nested edits, preserve line endings, strip/restore BOM, CRLF/CR normalization for matching, upstream-style fuzzy matching for trailing whitespace and Unicode normalization, emit diff/patch details.
- `bash`: platform shell, optional timeout in seconds, no hidden schema timeout, merged stdout/stderr, cancellation and process-tree kill, streaming UTF-8 decoder, bounded rolling tail, temp-file spooling for full output.
- Optional `grep`, `find`, `ls` stay opt-in and not exposed by default.

Verification:
- Table tests for offsets/limits/truncation, parent creation, exact/fuzzy edits, overlap rejection, BOM and line-ending preservation, Unicode normalization, bash timeout/cancellation, process kill, UTF-8 boundary handling, and full-output spooling.
- `go test -v ./pkg/tools`

#### Step 1.5 — CLI harness in `cmd/pi`

Add:
- `cmd/pi/main.go`
- `cmd/pi/flags.go`
- `cmd/pi/print.go`
- `cmd/pi/*_test.go` if package split allows

Requirements:
- Implement `-p` and `--print` non-interactive mode.
- Implement `--provider`, accepting only `openai-codex`.
- Implement `--model` using Codex-only registry.
- Implement `--thinking` with `off`, `minimal`, `low`, `medium`, `high`, `xhigh` where supported.
- Implement `--tools`, `--exclude-tools`, `--no-tools`, `--no-builtin-tools`.
- Plan for but do not overbuild: `--mode`, `--verbose`, `--version`, `--list-models`, `--system-prompt`, `--append-system-prompt`, `config`, `@file`, `--models`, unknown extension flag passthrough, `--export`, `--offline`, `--no-context-files`, extension/template/theme/skill toggles.
- Do not add `--api-key`.
- Do not add `--max-turns`.
- Print path streams text and thinking distinctly to stdout/stderr or structured output.
- Return non-zero only for runtime failure, not normal model/tool error messages represented in conversation.

Verification:
- CLI tests for flag parsing, provider rejection, forbidden flags absence, model selection, thinking clamp, tool include/exclude behavior, and print-mode exit-code semantics.
- `go test -v ./cmd/pi ./pkg/...`
- `go build -o bin/pi ./cmd/pi`

### Phase 2 — Sessions, branching, and compaction

Goal: persist and restore branchable session history with context-window management.

#### Step 2.1 — Versioned JSONL session tree

Add:
- `pkg/session/session.go`
- `pkg/session/entry.go`
- `pkg/session/store.go`
- `pkg/session/tree.go`
- `pkg/session/context.go`
- `pkg/session/*_test.go`

Requirements:
- Default session dir: `~/.pi/agent/sessions`.
- Env overrides: `PI_CODING_AGENT_SESSION_DIR`, `PI_CODING_AGENT_DIR`.
- Header version 3 with `type=session`, `id`, timestamp, cwd, optional `parentSession`.
- Entries contain `type`, `id`, `parentId`, timestamp.
- Entry types: `message`, `thinking_level_change`, `model_change`, `compaction`, `branch_summary`, `custom`, `custom_message`, `label`, `session_info`, `active_tools_change`, `leaf`.
- Rebuild context by walking selected leaf to root and applying compaction/branch summary rules.

Verification:
- Tests for append/load, tree reconstruction, leaf selection, path walking, malformed JSONL errors, branch summaries, and compaction entry application.
- `go test -v ./pkg/session`

#### Step 2.2 — Resume, continue, fork controls

Modify/add:
- `cmd/pi/flags.go`
- `cmd/pi/session.go`
- `pkg/session/*`

Requirements:
- `--session <id-or-path>`
- `--session-id <id>`
- `--session-dir <path>`
- `--fork <id>` and `--fork`
- `--continue`, `-c`
- `--resume`, `-r`
- `--name`, `-n`
- `--no-session`
- Forking preserves parent relationships and stores `parentSession` as source session file path.
- Upstream-like fork copies selected path entries into new JSONL file.

Verification:
- Tests for session discovery, explicit path/id load, continue previous, resume state, fork by ID/current leaf, no-session mode, parentSession path preservation.
- `go test -v ./pkg/session ./cmd/pi`

#### Step 2.3 — Context compaction

Add:
- `pkg/session/compaction.go` or `pkg/agent/compaction.go`
- tests in owning package

Requirements:
- Estimate from latest assistant usage where available.
- Fall back to chars/4 for trailing messages.
- Trigger when context exceeds `contextWindow - reserveTokens`.
- Keep recent turns according to `keepRecentTokens`.
- Reserve output/summarization space with `reserveTokens`.
- Cut on turn-aware boundaries; never detach tool results from assistant tool calls.
- Generate structured summary through active constrained model/provider.
- Update previous compaction summary when present.
- Track read and modified file paths in details.

Verification:
- Tests for threshold behavior, usage vs chars/4 fallback, turn-safe cuts, prior-summary update, file-path tracking, and no orphaned tool results.
- `go test -v ./pkg/session ./pkg/agent`

### Phase 3 — Interactive TUI

Goal: interactive terminal app with streaming output, editable prompt input, tool status, session controls, and branch navigation.

Add:
- `pkg/tui/renderer.go`
- `pkg/tui/component.go`
- `pkg/tui/transcript.go`
- `pkg/tui/editor.go`
- `pkg/tui/tools.go`
- `pkg/tui/status.go`
- `pkg/tui/overlay.go`
- `pkg/tui/session.go`
- `cmd/pi/interactive.go`
- `pkg/tui/*_test.go`

Requirements:
- Retained-mode differential renderer writing to stdout.
- Preserve native terminal scrollback.
- Use ANSI cursor controls and synchronized output sequences `CSI ?2026h` / `CSI ?2026l` to prevent flicker.
- Prompt editor: multiline input, history, word navigation, bracketed paste, undo/redo, kill-ring, IME/hardware cursor positioning, optional autocomplete/slash commands.
- Transcript: streaming assistant text, distinct thinking, tool call/result blocks, markdown rendering, edit diff rendering.
- Tool UI: active indicators, partial bash updates, expandable truncated output backed by full-output references.
- Status bar: provider/model, thinking level, session ID/name, git branch, token/context estimate, key hints.
- Session navigation: continue/resume/fork labels and branch summaries.
- Overlay stack: session/tree/model/thinking/theme/config selectors and extension-provided widgets when extension API supports them.

Verification:
- Renderer golden tests for diff output and synchronized output wrapping.
- Component tests for editor state transitions, transcript streaming updates, tool status lifecycle, and overlay stack behavior.
- Manual smoke: `go run ./cmd/pi` in a terminal after automated tests pass.
- `go test -v ./pkg/tui ./cmd/pi`

### Phase 4 — Resource loading, skills, and extensions

Goal: reproduce useful extensibility without allowing unsupported provider paths.

#### Step 4.1 — Settings and resource loader

Add:
- `pkg/resource/loader.go`
- `pkg/resource/settings.go`
- `pkg/resource/templates.go`
- `pkg/resource/diagnostics.go`
- `pkg/resource/*_test.go`

Requirements:
- Load settings from global/project scopes.
- Filter model overrides to supported provider only.
- Load skills, prompt templates, themes, extension declarations, system-prompt fragments.
- Report diagnostics for invalid resources.
- Prompt templates: frontmatter `argument-hint`, `$1`, `$2`, `$@`, `$ARGUMENTS`, `${@:N}`, `${@:N:L}`.

Verification:
- Tests for global/project precedence, provider filtering, diagnostics, prompt-template substitutions and slices.
- `go test -v ./pkg/resource`

#### Step 4.2 — Agent Skills

Modify/add:
- `pkg/resource/skills.go`
- `pkg/agent/skills.go`

Requirements:
- Default user path: `~/.pi/agent/skills`.
- Default project path: `<cwd>/.pi/skills`.
- `--skill <path>` overrides/additions.
- `--no-skills` disables default discovery.
- Support `SKILL.md` roots and direct `.md` skills.
- Recurse nested `SKILL.md`.
- Respect `.gitignore`, `.ignore`, `.fdignore`.
- Validate frontmatter `name`, `description`, `disable-model-invocation`.
- Inject visible skills into system prompt as XML metadata pointing to skill file location.

Verification:
- Tests with temp skill directories, nested skills, ignore files, invalid frontmatter, disabled skills, explicit overrides, and system-prompt XML injection.
- `go test -v ./pkg/resource ./pkg/agent`

#### Step 4.3 — Subprocess JSON-RPC extensions

Add:
- `pkg/extension/rpc.go`
- `pkg/extension/process.go`
- `pkg/extension/registry.go`
- `pkg/extension/session.go`
- `pkg/extension/*_test.go`

Requirements:
- Stable subprocess/IPC JSON-RPC interface.
- Extensions may register tools, commands, shortcuts, message renderers, prompt/system fragments, and UI widgets for header/footer/editor surfaces.
- Extensions must not register providers, models, OAuth flows, or custom stream functions.
- Extension state uses `custom` and `custom_message` session entries.
- Expose lifecycle hooks equivalent to agent/session events.
- Embedded JavaScript is deferred; no fake extension support.

Verification:
- Tests for JSON-RPC handshake, tool registration/calls, lifecycle events, session custom entries, process shutdown, and rejection of provider/model/OAuth registration attempts.
- `go test -v ./pkg/extension ./pkg/session ./pkg/agent`

## Critical files and directories to modify

Existing:
- `go.mod`
- `pkg/ai/ai.go`
- `pkg/ai/messages.go`
- `pkg/ai/model.go`
- `pkg/ai/options.go`
- `pkg/ai/stream.go`
- `pkg/ai/registry.go`
- `pkg/ai/dispatch.go`
- `pkg/ai/deepcopy.go`
- `pkg/ai/ai_test.go`

New:
- `cmd/pi/`
- `pkg/agent/`
- `pkg/tools/`
- `pkg/session/`
- `pkg/tui/`
- `pkg/resource/`
- `pkg/extension/`

Potential external dependencies requiring explicit review before adding:
- `github.com/coder/websocket`
- `github.com/google/uuid`
- `github.com/alecthomas/chroma`
- TUI/markdown packages only if chosen later after measuring complexity/performance.

## Cross-cutting constraints

- Go 1.25 (per `go.mod`; spec says 1.21+ minimum, but project uses latest).
- Idiomatic, concurrent Go; avoid unnecessary allocations and copies.
- Standard `testing` package; no third-party mocking frameworks.
- Prefer `httptest.Server` and local spies for integration tests.
- Context cancellation must avoid orphaned goroutines/processes.
- Do not log credentials/secrets.
- Release cross-process locks on success and error paths.
- Run full tests before committing.
- Keep APIs boring and explicit; delete obsolete code instead of preserving aliases.

## Final verification matrix

Run by phase as features land:
- `go test -v ./pkg/ai`
- `go test -v ./pkg/agent ./pkg/ai`
- `go test -v ./pkg/tools`
- `go test -v ./cmd/pi ./pkg/...`
- `go test -v ./pkg/session ./cmd/pi`
- `go test -v ./pkg/tui ./cmd/pi`
- `go test -v ./pkg/resource ./pkg/agent`
- `go test -v ./pkg/extension ./pkg/session ./pkg/agent`

Final gates:
- `go fmt ./pkg/... ./cmd/...`
- `go vet ./pkg/... ./cmd/...`
- `go test -v ./pkg/... ./cmd/...`
- `go build -o bin/pi ./cmd/pi`

Manual smoke after automated gates:
- `go run ./cmd/pi --list-models` shows only Codex models.
- `go run ./cmd/pi -p "..."` streams output and runs tools to natural stop using OAuth/Codex credentials.
- `go run ./cmd/pi` starts interactive TUI without corrupting scrollback.

## Remaining todos

1. Finish `pkg/ai` protocol parity.
2. **Implement Codex provider: model registry, registration, auth store, request shaping, SSE transport, stream assembly, end-to-end milestone** (Step 1.2, sub-steps 1.2.1–1.2.7 in `STEP-1-2.md`).
3. Implement Codex WebSocket and cached-WebSocket transport (Step 1.2.8 in `STEP-1-2.md`).
4. Build `pkg/agent` loop with parallel tool batches and queues (Step 1.3).
5. Build MVP tools: `read`, `write`, `edit`, `bash` (Step 1.4).
6. Build `cmd/pi` print-mode CLI (Step 1.5).
7. Add versioned JSONL sessions, resume/continue/fork, and compaction (Phase 2).
8. Add custom retained-mode TUI (Phase 3).
9. Add resource loader, skills, and subprocess JSON-RPC extensions (Phase 4).
10. Run final verification matrix and fix failures at source.
