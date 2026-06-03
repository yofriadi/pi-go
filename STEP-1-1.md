### Step 1.1: Unified AI stream protocol (`pkg/ai`)

Match Pi's API-adapter shape: dispatch by `model.API` to registered stream functions. Do not model the AI layer as one provider `Client`.

**Reference**: `packages/ai/src/types.ts`, `packages/ai/src/stream.ts`, `packages/ai/src/api-registry.ts`, `packages/ai/src/utils/event-stream.ts` in the original Pi repo.

This step is broken down into the following sub-steps:

---

#### Step 1.1.1: Foundation types (`pkg/ai/ai.go`)

Define the package-level primitives that every other file in `pkg/ai` depends on. Nothing here is optional — all downstream sub-steps import these.

- `Role` string type: `RoleUser`, `RoleAssistant`, `RoleToolResult`. Timestamps and system prompt live in `Context` directly — there is no system message role or `SystemMessage` struct in Phase 1. JSON role field values are strictly: `user` for `RoleUser`, `assistant` for `RoleAssistant`, and `toolResult` for `RoleToolResult`.
- `APIID` string type (e.g. `"openai-codex-responses"`).
- `ProviderID` string type (e.g. `"openai-codex"`).
- `StopReason` string type with **all five** values from Pi: `"stop"`, `"length"`, `"toolUse"`, `"error"`, `"aborted"`.
- `ThinkingLevel` string type: `"minimal"`, `"low"`, `"medium"`, `"high"`, `"xhigh"`.
- `ModelThinkingLevel` string type: `"off"` plus all `ThinkingLevel` values.
- `InputKind` string type: `"text"`, `"image"`.
- `Transport` string type: `"sse"`, `"websocket"`, `"websocket-cached"`, `"auto"`.
- `CacheRetention` string type: `"none"`, `"short"`, `"long"`.
- `ToolDefinition` struct: `Name`, `Description`, `Parameters map[string]any`.
- `Usage` struct (match Pi exactly):
  ```go
  type Usage struct {
      Input       int       `json:"input"`
      Output      int       `json:"output"`
      CacheRead   int       `json:"cacheRead"`
      CacheWrite  int       `json:"cacheWrite"`
      TotalTokens int       `json:"totalTokens"`
      Cost        UsageCost `json:"cost"`
  }
  ```
- `ModelCost` struct: `Input`, `Output`, `CacheRead`, `CacheWrite` (all `float64`, representing $/million tokens).
- `UsageCost` struct: `Input`, `Output`, `CacheRead`, `CacheWrite`, `Total` (all `float64`).
- `Context` struct: `SystemPrompt`, `Messages []Message`, `Tools []ToolDefinition`. Because `Messages` contains polymorphic interfaces, `Context` must implement custom JSON marshalling/unmarshalling to instantiate the correct concrete message structs (e.g. `UserMessage`, `AssistantMessage`, `ToolResultMessage`).

---

#### Step 1.1.2: Model types (`pkg/ai/model.go`)

Define the `Model` struct and supporting types used by the registry and stream dispatch. These are consumed by every provider adapter.

```go
type Model struct {
    ID               string
    Name             string
    Provider         ProviderID
    API              APIID
    BaseURL          string
    Input            []InputKind
    Reasoning        bool
    ThinkingLevelMap map[ModelThinkingLevel]*string
    Cost             ModelCost
    ContextWindow    int
    MaxTokens        int
    Headers          map[string]string
    Compat           any // API-specific compat shape; typed per-provider
}
```

Also define:
- `ThinkingBudgets` struct: `Minimal`, `Low`, `Medium`, `High` (`*int` each).
- Helper functions: `GetSupportedThinkingLevels(Model)`, `ClampThinkingLevel(Model, ModelThinkingLevel)`, `CalculateCost(Model, Usage)`, `ModelsAreEqual(a, b *Model)`.

---

#### Step 1.1.3: Message types and Content blocks (`pkg/ai/messages.go`)

Define the three message types that form the conversation protocol, plus the content block types they contain. Timestamps must serialize as Unix milliseconds (`int64` representing milliseconds since epoch) for strict upstream compatibility.

**Message interface and concrete types:**
```go
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

**Content block types** (each carries a `type` field in JSON via custom `MarshalJSON`):
- `TextContent{Text, TextSignature}` (JSON type: `"text"`) — implements `UserContent`, `AssistantContent`, `ToolResultContent`.
- `ThinkingContent{Thinking, ThinkingSignature, Redacted}` (JSON type: `"thinking"`) — implements `AssistantContent` only.
- `ImageContent{Data, MimeType}` (JSON type: `"image"`) — implements `UserContent`, `ToolResultContent`.
- `ToolCall{ID, Name, Arguments map[string]any, ThoughtSignature}` (JSON type: `"toolCall"`) — implements `AssistantContent` only.
- `AssistantMessageDiagnostic{Code, Message, Severity, Details}`.

**Custom JSON marshalling/unmarshalling** for all message types:
- `UserMessage.Content` discriminates `string` vs `[]UserContent` on unmarshal (check if JSON is `"..."` or `[...]`).
- `AssistantMessage.Content` discriminates by `type` field in each array element.
- `ToolResultMessage.Content` discriminates by `type` field.
- All message types inject `role` field on marshal and validate it on unmarshal.

---

#### Step 1.1.4: Deep Copying (`pkg/ai/deepcopy.go`)

Implement thread-safe deep-copy helpers for all message and content types. Required because the stream iterator emits `partial` snapshots that share backing slices/maps with later events — callers who hold onto a partial must not see it mutated.

- `DeepCopy()` methods on `UserMessage`, `*AssistantMessage`, `ToolResultMessage`.
- Slice-level helpers: `deepCopyUserContentSlice`, `deepCopyAssistantContentSlice`, `deepCopyToolResultContentSlice`.
- `deepCopyValue(any) any` for recursive map/slice/primitive cloning (used for `ToolCall.Arguments`, `ToolResultMessage.Details`, `AssistantMessageDiagnostic.Details`).
- Content block interfaces include a `deepCopy*Content()` method so the slice helpers dispatch polymorphically.

---

#### Step 1.1.5: Stream Options (`pkg/ai/ai.go`)

Define `StreamOptions` and `SimpleStreamOptions`. These are passed through the registry dispatch into provider adapters.

```go
type StreamOptions struct {
    Temperature  *float64
    MaxTokens    *int
    APIKey       string                      // OAuth/subscription bearer token; upstream-compatible field name
    Headers      map[string]string
    Transport    Transport
    CacheRetention CacheRetention
    SessionID    string
    TimeoutMs    *int
    WebsocketConnectTimeoutMs *int
    MaxRetries   *int
    MaxRetryDelayMs *int
    Metadata     map[string]any
    OnRequest    func(*http.Request, []byte) `json:"-"`
    OnResponse   func(*http.Response)        `json:"-"`
    OnPayload    func(payload any, model Model) (replaced any, didReplace bool, err error) `json:"-"`
}

type SimpleStreamOptions struct {
    StreamOptions
    Reasoning       ModelThinkingLevel
    ThinkingBudgets *ThinkingBudgets
}
```

> **Note**: Keep the upstream-compatible `APIKey` field name even though this project only supports OpenAI Codex via OAuth/subscription. Credential injection (OAuth token → `APIKey`) is handled entirely in Step 1.2. `StreamOptions` carries only a Codex OAuth bearer token for the provider adapter to consume, and Step 1.1 does not populate it.
> Also: `buildBaseOptions(Model, *SimpleStreamOptions) StreamOptions` helper (mirrors `simple-options.ts`), and `adjustMaxTokensForThinking(...)` for token budget clamping.

---

#### Step 1.1.6: Stream Event Protocol (`pkg/ai/ai.go`)

Define the event types that flow through the stream iterator. Pi uses a discriminated union; Go uses a flat struct with typed event codes and optional fields.

```go
type AssistantMessageEventType string

const (
    EventStart         AssistantMessageEventType = "start"
    EventTextStart     AssistantMessageEventType = "text_start"
    EventTextDelta     AssistantMessageEventType = "text_delta"
    EventTextEnd       AssistantMessageEventType = "text_end"
    EventThinkingStart AssistantMessageEventType = "thinking_start"
    EventThinkingDelta AssistantMessageEventType = "thinking_delta"
    EventThinkingEnd   AssistantMessageEventType = "thinking_end"
    EventToolCallStart AssistantMessageEventType = "toolcall_start"
    EventToolCallDelta AssistantMessageEventType = "toolcall_delta"
    EventToolCallEnd   AssistantMessageEventType = "toolcall_end"
    EventDone          AssistantMessageEventType = "done"
    EventError         AssistantMessageEventType = "error"
)

type AssistantMessageEvent struct {
    Type         AssistantMessageEventType
    ContentIndex int
    Delta        string               // text_delta, thinking_delta, toolcall_delta
    Content      string               // text_end, thinking_end
    ToolCall     *ToolCall             // toolcall_end
    Partial      *AssistantMessage     // start, *_start, *_delta, *_end
    Message      *AssistantMessage     // done
    Error        *AssistantMessage     // error
    Reason       StopReason           // done reason or error reason (e.g. StopReasonError, StopReasonAborted)
}
```

---

#### Step 1.1.7: `AssistantStream` push-based iterator (`pkg/ai/stream.go`)

Implement `AssistantStream` — the Go equivalent of Pi's `EventStream<AssistantMessageEvent, AssistantMessage>`.

**Producer side** (called by provider adapters):
- `Push(event AssistantMessageEvent) error` — enqueues an event. If a `done` event carries a StopReason outside `"stop"`, `"length"`, or `"toolUse"`, or if an `error` event carries a StopReason outside `"error"` or `"aborted"`, `Push` returns a validation error and the event is rejected. Internally uses a thread-safe, bounded internal queue/buffer. A single drain goroutine per stream reads from this queue and writes to the consumer channel. If the queue reaches its safety bound (e.g., consumer is stuck or not reading), `Push` returns a non-nil queue-overflow error to prevent memory leaks.
- `End(result *AssistantMessage)` — explicit successful termination. Resolves `Result()` with the provided message and `nil` error.
- `Error(err error, partial *AssistantMessage)` — explicit failed termination. Resolves `Result()` with the provided partial message and the non-nil error.

**Consumer side** (called by agent loop / CLI):
- `Events() <-chan AssistantMessageEvent` — returns a read-only channel for range-based consumption.
- `Result() (AssistantMessage, error)` — blocks until the stream completes. If the stream completes normally (`done` event or `End` called), returns the final assembled `AssistantMessage` and `nil` error. If it fails (`error` event, `Error` called, or explicit error), returns the partial `AssistantMessage` and a non-nil error.
- When the channel is drained and the stream is complete, the channel is closed.

**Contract** (matching Pi's single `EventStream<AssistantMessageEvent, AssistantMessage>` object with explicit Go refinements):
- A completed stream always resolves `Result()`.
- Returning `(AssistantMessage, error)` from `Result()` and `Complete`/`CompleteSimple` is a Go-native semantic refinement over the JS `result()` promise (which resolves to the final message regardless of success or failure). This allows idiomatic Go error checking.
- The bounded internal queue is a Go-native safety guard, not upstream parity. It must be large enough for normal streaming and must fail visibly with a queue-overflow error instead of silently dropping events or growing memory without bound.
- Events are delivered in push order; no reordering.
- After `done`/`error` (or `End`/`Error`), further `Push` calls are no-ops.
---

#### Step 1.1.8: API-Adapter Registry (`pkg/ai/registry.go`)

Implement the registry that maps `APIID` → adapter. Match Pi's `api-registry.ts` shape: each registration stores **two** stream functions (`stream` and `streamSimple`), not one.

```go
type StreamFunc func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream
type StreamSimpleFunc func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream

type ApiProvider struct {
    API          APIID
    Stream       StreamFunc
    StreamSimple StreamSimpleFunc
}

func RegisterApiProvider(p ApiProvider) error
func GetApiProvider(api APIID) (ApiProvider, bool)
func GetApiProviders() []ApiProvider
func ClearApiProviders()
```

Registry is a `sync.RWMutex`-protected `map[APIID]ApiProvider`. Because provider integrations are restricted strictly to built-in `openai-codex` providers (with extensions forbidden from registering custom provider backends), registration does not support `sourceID` scoping or unregistration. If `p.API` is empty, or if `p.Stream` or `p.StreamSimple` are nil, the registration is rejected returning a non-nil validation error.
`GetApiProviders()` returns a slice of registered `ApiProvider`s sorted alphabetically by `APIID` to ensure deterministic output.

Registration wraps the provider's functions with an API-mismatch guard that returns a pre-completed error stream if `model.API != registered API` (no panics).

---

#### Step 1.1.9: Top-level Dispatch Functions (`pkg/ai/dispatch.go`)

Implement the four public entry points that resolve the provider from the registry and delegate. Step 1.1 does **not** inject credentials — that is Step 1.2's responsibility.

```go
func Stream(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream
func Complete(ctx context.Context, model Model, c Context, opts *StreamOptions) (AssistantMessage, error)
func StreamSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream
func CompleteSimple(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) (AssistantMessage, error)
```

- `Stream` / `StreamSimple` look up the provider via `GetApiProvider(model.API)`. If missing, they return a pre-completed `AssistantStream` immediately carrying an `error` event with `StopReason = "error"`.
- `Complete` / `CompleteSimple` call the corresponding stream function and block on `Result()`.

---

#### Step 1.1.10: Unit Tests (`pkg/ai/ai_test.go`)

Test the protocol layer in isolation — no real HTTP, no real providers. Register mock `StreamFunc` adapters that emit canned events.

**Required test cases:**

1. **JSON round-trip for all message types**:
   - `UserMessage` with string content.
   - `UserMessage` with `[]UserContent` (mixed text + image blocks).
   - `AssistantMessage` with text, thinking, and tool call blocks.
   - `ToolResultMessage` with text + image blocks.
   - Verify role field injection on marshal and validation on unmarshal.
   - Verify missing required `UserMessage.content`, `ToolResultMessage.toolCallId`, and `ToolResultMessage.toolName` fail unmarshal validation.
   - Verify timestamps serialize/deserialize correctly as milliseconds since epoch (`int64`).
   - Verify polymorphic `Message` unmarshaling via `Context` unmarshal behaves correctly.

2. **Content block type discrimination**:
   - Unknown `type` field returns an error, not silent loss.
   - Role mismatch returns an error.
   - Verify `type` of `ToolCall` is `"toolCall"`.

3. **DeepCopy isolation**:
   - Mutating a deep-copied `AssistantMessage.Content` slice does not affect the original.
   - Mutating a deep-copied `ToolCall.Arguments` map does not affect the original.

4. **AssistantStream behavior**:
   - Producer pushes events → consumer receives them in order via channel.
   - `Result()` blocks until `done` event, then returns the final message and `nil` error.
   - `Result()` returns the partial message and a non-nil error on `error` event.
   - `Push` after `done`/`error` is a no-op.
   - Multiple goroutines can call `Result()` concurrently without race.
   - `Push` returns queue overflow error if buffer fills.
   - Verify that calling `End(result)` terminates the stream and resolves `Result()` with `nil` error, and calling `Error(err, partial)` resolves it with the non-nil error and the partial message.

5. **Registry dispatch**:
   - `Stream()` with unregistered API returns an error stream (not a panic).
   - `Stream()` with registered API routes to the correct adapter.
   - `ClearApiProviders()` empties the registry.
   - API-mismatch guard returns an error stream (not a panic).
   - Verify `RegisterApiProvider` returns a non-nil error on invalid inputs (empty API ID or nil stream functions).
   - Verify that `GetApiProviders()` returns registered providers sorted alphabetically by `APIID`.

6. **StopReason partitioning**:
   - `done` events carry only `"stop"`, `"length"`, or `"toolUse"`.
   - `error` events carry only `"error"` or `"aborted"`.
   - Verify that pushing an event with a `StopReason` outside its allowed partition (e.g. `EventDone` with `"error"`, or `EventError` with `"stop"`) is rejected with a validation error on `Push`.

7. **Race detection**: Run all tests with `-race` flag.
