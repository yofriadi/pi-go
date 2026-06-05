# Spec: Recreating Pi in Golang (pi-go)

## Objective
The goal is to recreate the `pi` coding agent in Golang (`pi-go`) while narrowing the provider surface strictly to **OpenAI Codex** (subscription-based, using OAuth/Codex). The target users are software developers looking for a fast, terminal-native agent with branchable history, context compaction, and powerful code editing capabilities.

Success is defined by:
- A non-interactive print mode (`pi -p "..."`) streaming assistant output and executing batch/parallel tools to completion.
- Safe session persistence in a branchable JSONL tree.
- Context-window compaction using structured summaries.
- An interactive terminal UI (TUI) with a custom retained-mode differential renderer that preserves the native terminal scrollback.
- A subprocess-based JSON-RPC extension interface and Agent Skills metadata execution.

---

## Tech Stack
- **Language**: Go 1.21+
- **CLI**: Standard `flag` library
- **HTTP/SSE/WebSocket**: `net/http` (stdlib) + `github.com/coder/websocket` for OAuth/Codex stream transports
- **JSON Schema**: Checked handwritten schemas or struct-based validation
- **TUI**: Custom retained-mode component system writing to stdout (using ANSI cursor controls, backbuffer line comparisons, and synchronized output sequences `CSI ?2026h`/`CSI ?2026l` to prevent flicker)
- **Markdown Rendering & Highlighting**: Custom parser/renderer mapping markdown ASTs directly into the styled cell-output stream, with optional integration of `github.com/alecthomas/chroma`
- **Session ID / Keys**: UUIDv7 generated using `github.com/google/uuid`

---

## Commands
- **Build**: `go build -o bin/pi ./cmd/pi`
- **Test**: `go test -v ./pkg/...`
- **Lint / Vet**: `go vet ./pkg/...` (or standard `golangci-lint run`)
- **Format**: `go fmt ./pkg/...`
- **Run (Dev)**: `go run ./cmd/pi`

---

## Project Structure
```
pi-go/
├── cmd/
│   └── pi/                  # CLI entrypoint, options parsing, print/interactive/RPC modes
└── pkg/
    ├── ai/                  # Message models, stream protocol, OpenAI Codex provider adapter
    ├── agent/               # Core agent loop, steering & follow-up queues, hook runner
    ├── tools/               # Built-in tool implementations (read, write, edit, bash)
    ├── session/             # Versioned JSONL tree persistence, branching, compaction
    └── tui/                 # Retained-mode components and custom differential renderer
```

---

## Code Style
Idiomatic, concurrent Go code prioritizing safety, performance, and low memory allocations.

### Naming & Formatting
- Standard `gofmt` code layout.
- Interfaces named with `-er` suffix where possible or descriptive nouns.
- Private fields/methods start with lowercase; exported fields/methods start with uppercase.
- Errors are returned as the last parameter, wrapped descriptively.

### Example Snippet
```go
package ai

import (
	"context"
	"fmt"
	"sync"
)

// ApiProvider defines the registered adapter mapping API ID to stream functions.
type ApiProvider struct {
	API          APIID
	Stream       StreamFunc
	StreamSimple StreamSimpleFunc
}

type Registry struct {
	mu        sync.RWMutex
	providers map[APIID]ApiProvider
}

func (r *Registry) Register(p ApiProvider) error {
	if p.API == "" || p.Stream == nil || p.StreamSimple == nil {
		return fmt.Errorf("invalid provider registration: missing required fields")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.API] = p
	return nil
}
```

---

## Testing Strategy
- **Framework**: Go standard `testing` package.
- **Unit Tests**: Emphasize table-driven tests for message parsing, options scaling, tool results, and stream validation.
- **Integration Tests**: Emphasize `httptest.Server` to mock Codex SSE/WebSocket streams and test end-to-end token refresh and response handling.
- **Mock Policy**: No third-party mocking frameworks (e.g. gomock) or fake fallbacks. Write clean interfaces and use local test servers/spies for network tests.
- **Assertions**: Standard Go comparisons, or light helper functions to compare structs/JSON.

---

## Boundaries

### Always Do
- Enforce the **OpenAI Codex** provider check (reject others with explicit errors).
- Store credential files securely (directory permissions `0700`, files `0600`).
- Release cross-process locks under all execution paths (success or error).
- Run the full test suite before committing code.
- Handle context cancellation and timeout limits gracefully (no orphaned goroutines).

### Ask First
- Adding external packages outside the recommended stack.
- Modifying session JSONL version layouts (version 3 is current upstream).
- Altering CLI flags or public APIs that change compatibility with upstream `pi`.

### Never Do
- Never implement direct OpenAI API-key path or other LLM providers (Anthropic, Gemini, Mistral, Bedrock, OpenRouter, Copilot).
- Never allow `CODEX_ACCESS_TOKEN` or `OPENAI_API_KEY` env vars to bypass OAuth token logic.
- Never write credentials/secrets to logs, diagnostics, or console outputs.
- Never suppress tests or bypass the compiler/linter checks to force a pass.
- Never implement Go-native fallback searches when `rg` and `fd` are missing (fail explicitly).

---

## Success Criteria
- Registered only `openai-codex-responses` API adapter inside `pkg/ai`.
- Safe, atomically updated `auth.json` loading and refresh token sequence.
- Non-interactive prompt executions run to a natural stop or completion limit.
- Read, write, and edit tools work precisely (matching fuzzy whitespaces and normalization).
- JSONL session branches load, fork, and write tree-linked messages correctly.
- Interactive TUI displays text, thinking, and running tools dynamically using the custom differential renderer.

---

## Resolved Architecture Decisions
1. **WebSocket client library**: `github.com/coder/websocket`.
2. **Context compaction estimation**: Simple character/4 approximation fallback for token estimations, combined with actual token usage returned by the API.
3. **RPC/Plugin Extension Design**: Subprocess/IPC interface communicating over JSON-RPC.
4. **Search tools**: Enforce requirement of `rg` and `fd` binaries on PATH, with no Go-native fallbacks.
