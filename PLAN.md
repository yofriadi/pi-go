# Plan: Recreating Pi in Golang

This document is the technical roadmap for recreating `pi` in Go while intentionally narrowing the provider surface to the project policy in `AGENTS.md`: **OpenAI Codex** only. It follows the real TypeScript/Node/Bun `pi` architecture where that matters for compatibility, and chooses Go-native implementations where exact runtime parity would add unnecessary complexity.

> [!IMPORTANT]
> Provider integrations are intentionally constrained. Do **not** implement direct Anthropic, Google/Gemini, OpenAI usage-based API-key, Mistral, Bedrock, OpenRouter, Copilot, OpenCode, or other provider backends. OpenAI Codex is supported only through subscription/OAuth Codex flows, not pay-as-you-go OpenAI API keys.

---

## High-Level Architectural Mapping

```
pi-go/
├── go.mod
├── cmd/
│   └── pi/                  # CLI entrypoint, arg parsing, run modes
└── pkg/
    ├── ai/                  # Model registry, stream protocol, provider/API adapters
    ├── agent/               # Agent loop, hooks, queues, compaction orchestration
    ├── tools/               # Built-in coding tools and schemas
    ├── session/             # Versioned JSONL tree persistence
    └── tui/                 # Interactive terminal UI
```

| TypeScript Area | Go Package/Directory | Go Direction |
| :--- | :--- | :--- |
| `packages/ai` | `pkg/ai` | `net/http`, SSE/WebSocket stream decoders, API-adapter registry |
| `packages/agent` | `pkg/agent` | Context-aware loop, goroutines, channels, hooks, queue handling |
| `packages/coding-agent/src/core/tools` | `pkg/tools` | `os`, `io/fs`, `os/exec`, `regexp`, optional managed `rg`/`fd` |
| `packages/coding-agent/src/core/session-manager.ts` | `pkg/session` | Versioned newline-delimited JSON entries with `id`/`parentId` tree links |
| `packages/tui` + interactive mode | `pkg/tui` | Go-native TUI; Bubble Tea is acceptable, but not exact parity with TypeScript Pi's custom renderer |
| `packages/coding-agent/src/main.ts` | `cmd/pi` | Standard `flag` or Cobra; interactive, print, JSON/RPC modes over time |

---

## Provider Constraint and Model Registry

### Supported provider IDs

Only expose this provider ID:

- `openai-codex` — ChatGPT/Codex subscription OAuth integration only.

Do not expose the broader upstream `pi` provider list. The Go implementation may internally reuse protocol dialect code, but public provider selection, environment variable handling, config, docs, tests, and model registry entries must be filtered to this provider.

### Static filtered model registry (`pkg/ai`)

Model metadata is core infrastructure, not a nice-to-have. Define or generate a compact registry containing only Codex entries:

```go
type Model struct {
    ID               string
    Name             string
    Provider         ProviderID
    API              APIID
    BaseURL          string
    Input            []InputKind // text, image
    Reasoning        bool
    ThinkingLevelMap map[ModelThinkingLevel]*string
    Cost             ModelCost
    ContextWindow    int
    MaxTokens        int
    Headers          map[string]string
    Compat           any // API-specific compat shape; typed per-provider
}
```

Required API dialects for the constrained provider set:

- `openai-codex-responses` for Codex subscription/OAuth.

Current upstream Codex registry entries are manually listed, not fetched from an external model catalog:

- `gpt-5.3-codex-spark`
- `gpt-5.4`
- `gpt-5.4-mini`
- `gpt-5.5`

Keep this list explicit and reviewed. Do not expose OpenAI API-key `openai` models whose names contain "codex"; they are not the ChatGPT/Codex subscription provider.

---

## Phase 1: Minimal Coding Agent

*Goal: non-interactive `pi -p "..."` that streams assistant text/thinking, executes compatible tools, and exits when the agent reaches a natural stop.*

### Step 1.1: Unified AI stream protocol (`pkg/ai`)

Do not model the AI layer as one provider `Client`. Match Pi's API-adapter shape: dispatch by `model.API` to registered stream functions.

Core types:

```go
type Context struct {
    SystemPrompt string
    Messages     []Message
    Tools        []ToolDefinition // optional when no tools are exposed
}

type Message interface{ messageRole() Role }

type UserMessage struct {
    Content   any   `json:"content"` // string or []UserContent; required
    Timestamp int64 `json:"timestamp"` // Unix epoch milliseconds
}

type AssistantMessage struct {
    Content       []AssistantContent           `json:"content,omitempty"`
    API           APIID                        `json:"api,omitempty"`
    Provider      ProviderID                   `json:"provider,omitempty"`
    Model         string                       `json:"model,omitempty"`
    ResponseModel string                       `json:"responseModel,omitempty"`
    ResponseID    string                       `json:"responseId,omitempty"`
    Diagnostics   []AssistantMessageDiagnostic `json:"diagnostics,omitempty"`
    Usage         Usage                        `json:"usage"`
    StopReason    StopReason                   `json:"stopReason,omitempty"`
    ErrorMessage  string                       `json:"errorMessage,omitempty"`
    Timestamp     int64                        `json:"timestamp"` // Unix epoch milliseconds
}

type ToolResultMessage struct {
    ToolCallID string              `json:"toolCallId"` // required
    ToolName   string              `json:"toolName"`   // required
    Content    []ToolResultContent `json:"content,omitempty"`
    Details    any                  `json:"details,omitempty"`
    IsError    bool                 `json:"isError,omitempty"`
    Timestamp  int64                `json:"timestamp"` // Unix epoch milliseconds
}
```

Content blocks:

- `TextContent{text, textSignature}`
- `ThinkingContent{thinking, thinkingSignature, redacted}`
- `ImageContent{data, mimeType}`
- `ToolCall{id, name, arguments, thoughtSignature}`

Stream events must support partial updates, not just final messages:

- `start`
- `text_start`, `text_delta`, `text_end`
- `thinking_start`, `thinking_delta`, `thinking_end`
- `toolcall_start`, `toolcall_delta`, `toolcall_end`
- `done`
- `error`

Expose four operations:

```go
type StreamFunc func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream

func Stream(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream
func Complete(ctx context.Context, model Model, c Context, opts *StreamOptions) (AssistantMessage, error)
func StreamSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream
func CompleteSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) (AssistantMessage, error)
```

`AssistantStream` should be a single object that supports iteration/channel consumption plus a final-result method, so callers can consume deltas and still retrieve the canonical final `AssistantMessage`. `Complete`/`CompleteSimple` are thin wrappers over that final-result method. `SimpleStreamOptions` should extend `StreamOptions` with reasoning level and optional thinking-budget overrides for models that support them.

### Step 1.2: Provider/API implementations (`pkg/ai`)

Implement only constrained provider access:

#### OpenAI Codex

- Authentication: OAuth/Codex subscription credentials only, loaded from the Pi auth store (`auth.json`) and refreshed through the OAuth provider flow. There is no upstream `CODEX_ACCESS_TOKEN` environment variable and no OpenAI pay-as-you-go API-key path.
- The harness resolves a per-request bearer token through a `getApiKey(provider)` hook and passes it down as `StreamOptions.apiKey`. Keep the upstream field name `apiKey` even though project policy restricts its value to a Codex OAuth access token.
- Support Codex stream transports:
  - SSE first for MVP.
  - WebSocket and WebSocket-cached once session reuse/prompt caching is implemented.
- Preserve reasoning item IDs/signatures where returned so future turns can maintain continuity.

`StreamOptions` should include at least:

- temperature, max tokens, headers
- `apiKey` containing the OAuth/Codex bearer token only; no OpenAI pay-as-you-go API-key source or fallback
- transport (`sse`, `websocket`, `websocket-cached`, `auto`)
- cache retention
- session ID
- request timeout / WebSocket connect timeout
- retry limits, including maximum retry delay
- metadata
- debug hooks for inspecting provider payload/response in tests

Codex-specific stream options should include:

- `reasoningEffort`
- `reasoningSummary`
- `serviceTier`
- `textVerbosity`

Codex request handling should preserve upstream protocol details:

- extract `chatgpt-account-id` from the OAuth access-token JWT
- set `Authorization`, `chatgpt-account-id`, `originator: pi`, `User-Agent`, and transport-specific `OpenAI-Beta` headers
- use an SSE response-header timeout
- support WebSocket beta headers, session request IDs, cached WebSocket continuation state, and fallback to SSE after session-scoped WebSocket failures

### Step 1.3: Core agent loop (`pkg/agent`)

Recreate the real Pi loop shape from the start. Keep it small, but do not build an incompatible sequential-only loop.

Required concepts:

- `AgentMessage` superset for UI/custom messages.
- Required `convertToLlm([]AgentMessage) []ai.Message` boundary before each provider request.
- Context transform hook for compaction/pruning before conversion.
- Streaming assistant partial updates forwarded as agent events.
- Tool calls executed as a batch.
- Parallel tool execution by default, with per-tool or global sequential mode.
- In parallel mode, `tool_execution_end` events are emitted in completion order, while final tool-result messages are emitted in assistant source order.
- Early termination only when all tool results in a batch carry `Terminate=true`.
- Steering queue: user/system messages injected between turns while the agent is still working.
- Follow-up queue: messages processed after the agent would otherwise stop.
- Queue drain modes for both steering and follow-up queues: `"all"` vs. `"one-at-a-time"`.

Core hooks:

- `prepareNextTurn`
- `shouldStopAfterTurn`
- `beforeToolCall`
- `afterToolCall`
- `getSteeringMessages`
- `getFollowUpMessages`
- `getApiKey`

### Step 1.4: Built-in tools (`pkg/tools`)

Match Pi's built-in tool names and schemas unless there is a deliberate compatibility break.

Required MVP tool set:

- `read`
  - path plus optional 1-indexed offset/limit.
  - default truncation: 2000 lines and 50 KiB.
  - no partial-line output except where explicitly documented.
- `write`
  - path plus full content.
  - recursively create parent directories.
- `edit`
  - path plus `edits: [{ oldText, newText }]`.
  - match against original file, not incrementally.
  - reject overlapping/nested edits.
  - preserve line endings.
  - generate display diff/patch as result details.
  - support JSON-string `edits` and legacy single-edit `{oldText, newText}` inputs for model compatibility.
  - strip BOM before matching and restore it after writing.
  - normalize CRLF/CR to LF for matching, then restore original line endings.
  - implement upstream-style fuzzy matching for trailing whitespace and Unicode normalization (`NFKC`, smart quotes, Unicode dashes, special spaces).
- `bash`
  - command plus optional timeout in seconds; no hidden default timeout in schema.
  - execute through platform shell.
  - capture stdout/stderr together.
  - support context cancellation and process-tree kill.
  - use a streaming UTF-8 decoder, bounded rolling tail, and temp-file spooling for full output preservation.

Optional early parity tools (opt-in only, do not expose to the model by default):

- `grep`
  - regex/literal pattern, optional path, glob, ignoreCase, literal, context, limit.
  - Prefer managed `rg` for parity; a Go-regexp fallback is acceptable only if behavior is documented.
- `find`
  - pattern/path/limit.
  - Prefer managed `fd`; Go filepath walking fallback is acceptable only if behavior is documented.
- `ls`
  - directory listing with limit.

All tools should expose pluggable operation interfaces so local filesystem/shell behavior can later be replaced by remote execution or extensions without changing schemas. Agent-side tool definitions should also support per-tool execution-mode overrides when a tool must force sequential execution inside an otherwise parallel batch.

### Step 1.5: CLI harness (`cmd/pi`)

Initial flags:

- `-p`, `--print`: non-interactive prompt and exit.
- `--provider`: only `openai-codex`.
- `--model`: model ID or provider/model selector from the filtered registry.
- `--thinking`: `off`, `minimal`, `low`, `medium`, `high`, `xhigh` where supported by the selected model.
- `--tools`, `--exclude-tools`, `--no-tools`, `--no-builtin-tools`.
- Additional parity-oriented flags/subcommands to plan for early: `--mode` (`text`, `json`, `rpc`), `--verbose`, `--version`, `--list-models`, `--system-prompt`, `--append-system-prompt`, `config` subcommand, `@file` args, model cycling via `--models`, extension flag passthrough for unknown `--flags`, `--export`, `--offline`, `--no-context-files`, `--extension` / `--no-extensions`, `--prompt-template` / `--no-prompt-templates`, `--theme` / `--no-themes`, `--skill` / `--no-skills`. Do not add `--api-key`; upstream has it, but this project intentionally forbids it because credential flow is OAuth/Codex subscription only. Do not add `--max-turns` for parity; upstream does not parse it.

The print path should stream text and thinking distinctly to stdout/stderr or structured output, and should return a non-zero exit code only for real runtime failure, not for normal model/tool error messages encoded in the conversation.

---

## Phase 2: Sessions, Branching, and Compaction

*Goal: persist and restore whole coding sessions with branchable history and context-window management.*

### Step 2.1: Versioned JSONL session tree (`pkg/session`)

Use Pi-compatible tree entries, not a flat transcript.

Session directory:

- default: `~/.pi/agent/sessions`
- env override: `PI_CODING_AGENT_SESSION_DIR`
- agent dir override: `PI_CODING_AGENT_DIR`

Header:

```go
type SessionHeader struct {
    Type          string // "session"
    Version       int    // current upstream version is 3
    ID            string
    Timestamp     time.Time
    CWD           string
    ParentSession string `json:"parentSession,omitempty"`
}
```

Every session entry has tree links:

```go
type EntryBase struct {
    Type      string
    ID        string
    ParentID  *string
    Timestamp time.Time
}
```

Required entry types:

- `message`
- `thinking_level_change`
- `model_change`
- `compaction`
- `branch_summary`
- `custom`
- `custom_message`
- `label`
- `session_info`
- `active_tools_change`
- `leaf`

Rebuild context by walking from selected leaf to root, then applying compaction and branch summary rules along that path.

### Step 2.2: Resume, continue, and fork controls

Implement:

- `--session <id-or-path>`: load a specific session.
- `--session-id <id>`: force ID for a new session.
- `--session-dir <path>`: override session directory.
- `--fork <id>` or `--fork`: branch from an existing/current session.
- `--continue`, `-c`: continue previous session with an optional new prompt.
- `--resume`, `-r`: resume session state.
- `--name`, `-n`: set display name.
- `--no-session`: run without persistence.

Forking should preserve parent relationships through `parentSession` and/or entry `parentId`, but upstream also copies selected path entries into a new JSONL file. Store `parentSession` as the source session file path, not just an ID.

### Step 2.3: Compaction

Compaction must summarize rather than blindly truncate.

Required behavior:

- Estimate context size from latest assistant usage when available.
- Fall back to chars/4 estimation for trailing messages.
- Trigger compaction when estimated context exceeds `contextWindow - reserveTokens`.
- Keep recent turns according to configurable `keepRecentTokens`.
- Reserve output/summarization space via `reserveTokens`.
- Choose turn-aware cut points; never leave tool results detached from their assistant tool call.
- Generate a structured LLM summary with the current constrained model/provider.
- If a previous compaction summary exists, update it instead of discarding prior summarized context.
- Track read and modified file paths in compaction details.

Default settings should mirror Pi's intent:

```go
type CompactionSettings struct {
    Enabled          bool
    ReserveTokens    int
    KeepRecentTokens int
}
```

---

## Phase 3: Interactive TUI

*Goal: move from print mode to an interactive terminal application with streaming output, editable prompt input, tool status, session controls, and branch navigation.*

The TypeScript Pi TUI is a custom differential renderer, not Bubble Tea. In Go, using Bubble Tea/Bubbles/Lipgloss is acceptable if we preserve user-visible behavior instead of exact internals.

Required components:

1. **Prompt editor**
   - multi-line input
   - history navigation
   - word navigation
   - paste handling with bracketed paste/paste markers where the terminal supports it
   - undo/redo
   - kill-ring behavior
   - IME cursor marker / hardware cursor positioning
   - optional autocomplete/slash commands
2. **Streaming transcript viewer**
   - assistant text streaming
   - thinking/reasoning rendered distinctly
   - tool call and tool result blocks
   - markdown rendering
   - diff rendering for edits
3. **Tool execution UI**
   - active tool indicators
   - partial bash output updates
   - expandable truncated output backed by full-output references
4. **Footer/status bar**
   - provider/model
   - thinking level
   - session ID/name
   - git branch when available
   - token/context estimate
   - key hints
5. **Session navigation**
   - continue/resume/fork labels
   - branch summary display when navigating divergent history
6. **Overlay and selector infrastructure**
   - overlay stack for modal components
   - session selector and tree selector
   - model/thinking/theme/config selectors
   - extension-provided header/footer/editor widgets where supported by the chosen extension API

---

## Phase 4: Resource Loading, Skills, and Extensions

*Goal: reproduce Pi's extensibility where it is useful in Go, without accidentally adding unsupported provider paths.*

### Step 4.1: Settings and resource loader

Add a central resource loader responsible for:

- settings from global/project scopes
- model overrides, filtered to supported providers only
- skills
- prompt templates
- themes
- extension declarations
- system-prompt fragments
- diagnostics for invalid resources

Prompt template parity requirements:

- parse `argument-hint` frontmatter
- support `$1`, `$2`, ... positional substitutions
- support `$@` and `$ARGUMENTS` for all arguments
- support `${@:N}` and `${@:N:L}` bash-style argument slices

### Step 4.2: Skills

Implement Agent Skills style loading:

- default user path: `~/.pi/agent/skills`
- default project path: `<cwd>/.pi/skills`
- explicit `--skill <path>` overrides/additions
- `--no-skills` disables default discovery
- support `SKILL.md` skill roots
- support direct `.md` skills in root directories
- recurse for nested `SKILL.md`
- respect `.gitignore`, `.ignore`, and `.fdignore`
- validate frontmatter `name`, `description`, and `disable-model-invocation`
- inject visible skills into the system prompt as XML metadata pointing to the skill file location

### Step 4.3: Extensions

Do not assume `goja` provides TypeScript Pi extension parity. TypeScript Pi loads real TS/JS modules through `jiti`; Go needs an explicit design choice.

Preferred Go direction:

- define a stable extension API around process/RPC boundaries or compiled Go plugins where supported;
- allow extensions to register tools, commands, shortcuts, message renderers, prompt/system fragments, and custom UI widgets for header/footer/editor surfaces;
- explicitly diverge from upstream `pi.registerProvider()`: TypeScript Pi lets extensions register providers, models, OAuth flows, and custom `streamSimple`, but pi-go must strictly forbid custom provider backends or adapters to enforce the `openai-codex` restriction;
- keep extension state in `custom` and `custom_message` session entries;
- expose lifecycle hooks equivalent to agent/session events.

Embedded JavaScript may be added later for simple scripting, but it is not the default parity path.

---

## Recommended Technology Stack

- **CLI**: standard `flag` initially; Cobra only if subcommands become necessary.
- **HTTP/SSE/WebSocket**: `net/http`; `nhooyr.io/websocket` or `gorilla/websocket` if WebSocket support is needed.
- **JSON schema/tool params**: Go structs plus JSON Schema generation or checked handwritten schemas.
- **TUI**: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, `github.com/charmbracelet/lipgloss` as Go-native implementation choices.
- **Markdown**: Glamour or a lighter custom renderer; choose based on streaming performance.
- **Syntax highlighting**: Chroma where needed.
- **Search tools**: managed `rg` and `fd` for parity, with documented Go fallbacks if unavailable.
- **Token estimation**: usage-first; chars/4 fallback. Add tokenizer libraries only when they improve decisions for supported models.
- **UUIDs**: UUIDv7-compatible generation for ordered session entries.

---

## Non-Goals

- No direct Anthropic, Google/Gemini, OpenAI API-key, Mistral, Bedrock, OpenRouter, GitHub Copilot, OpenCode, or other provider integrations.
- No broad model catalog exposed at runtime.
- No provider aliases that bypass the Codex constraint.
- No hidden compatibility shims that accept unsupported provider credentials.
- No fake extension support: extension APIs must either work end-to-end or stay unimplemented.
