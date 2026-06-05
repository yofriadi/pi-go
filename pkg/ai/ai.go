// Package ai provides the foundation protocol types, data structures, and
// interfaces for interacting with AI models, managing message history, and
// defining tools.
package ai

import (
	"encoding/json"
	"fmt"
)

// Role defines the role of a message in the conversation.
type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// APIID defines the specific API dialect identifier.
type APIID string

const (
	APIIDOpenAICodexResponses APIID = "openai-codex-responses"
)

// ProviderID defines the backend provider identifier.
type ProviderID string

const (
	ProviderIDOpenAICodex ProviderID = "openai-codex"
)

// StopReason defines why a stream or completion stopped.
type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// ThinkingLevel defines the budget/level for reasoning.
type ThinkingLevel string

const (
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	ThinkingLevelXHigh   ThinkingLevel = "xhigh"
)

// ModelThinkingLevel extends ThinkingLevel with an "off" option.
type ModelThinkingLevel string

const (
	ModelThinkingLevelOff     ModelThinkingLevel = "off"
	ModelThinkingLevelMinimal ModelThinkingLevel = "minimal"
	ModelThinkingLevelLow     ModelThinkingLevel = "low"
	ModelThinkingLevelMedium  ModelThinkingLevel = "medium"
	ModelThinkingLevelHigh    ModelThinkingLevel = "high"
	ModelThinkingLevelXHigh   ModelThinkingLevel = "xhigh"
)

// InputKind defines the modalities a model can receive.
type InputKind string

const (
	InputKindText  InputKind = "text"
	InputKindImage InputKind = "image"
)

// Transport defines the delivery mechanism for the stream.
type Transport string

const (
	TransportSSE             Transport = "sse"
	TransportWebSocket       Transport = "websocket"
	TransportWebSocketCached Transport = "websocket-cached"
	TransportAuto            Transport = "auto"
)

// CacheRetention defines the prompt cache strategy.
type CacheRetention string

const (
	CacheRetentionNone  CacheRetention = "none"
	CacheRetentionShort CacheRetention = "short"
	CacheRetentionLong  CacheRetention = "long"
)

// ToolDefinition describes a tool the agent can invoke.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Usage captures token counts and cost for a request.
type Usage struct {
	Input       int       `json:"input"`
	Output      int       `json:"output"`
	CacheRead   int       `json:"cacheRead"`
	CacheWrite  int       `json:"cacheWrite"`
	TotalTokens int       `json:"totalTokens"`
	Cost        UsageCost `json:"cost"`
}

// ModelCost defines pricing per million tokens.
type ModelCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// UsageCost captures computed financial cost of a run.
type UsageCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// Context contains system prompt, message history, and tools.
type Context struct {
	SystemPrompt string           `json:"systemPrompt,omitempty"`
	Messages     []Message        `json:"messages,omitempty"`
	Tools        []ToolDefinition `json:"tools,omitempty"`
}

// MarshalJSON marshals the Context into its JSON representation.
func (c Context) MarshalJSON() ([]byte, error) {
	type Alias Context
	return json.Marshal(&struct {
		Alias
	}{
		Alias: Alias(c),
	})
}

// UnmarshalJSON implements custom polymorphic unmarshalling for Context.Messages.
func (c *Context) UnmarshalJSON(data []byte) error {
	var raw struct {
		SystemPrompt string            `json:"systemPrompt,omitempty"`
		Messages     []json.RawMessage `json:"messages,omitempty"`
		Tools        []ToolDefinition  `json:"tools,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	c.SystemPrompt = raw.SystemPrompt
	c.Tools = raw.Tools

	if len(raw.Messages) > 0 {
		var msgs []Message
		for _, item := range raw.Messages {
			msg, err := unmarshalMessage(item)
			if err != nil {
				return err
			}
			msgs = append(msgs, msg)
		}
		c.Messages = msgs
	} else {
		c.Messages = nil
	}
	return nil
}

func unmarshalMessage(data []byte) (Message, error) {
	var roleDetector struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(data, &roleDetector); err != nil {
		return nil, err
	}
	switch roleDetector.Role {
	case RoleUser:
		var msg UserMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	case RoleAssistant:
		var msg AssistantMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	case RoleToolResult:
		var msg ToolResultMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return msg, nil
	case "":
		return nil, fmt.Errorf("missing role field in message")
	default:
		return nil, fmt.Errorf("unknown role %q in message", roleDetector.Role)
	}
}

// AssistantMessageEventType defines the type of a stream event.
type AssistantMessageEventType string

const (
	// EventStart indicates the start of the assistant response.
	EventStart AssistantMessageEventType = "start"
	// EventTextStart indicates that a text content block is starting.
	EventTextStart AssistantMessageEventType = "text_start"
	// EventTextDelta indicates a text block incremental chunk (delta).
	EventTextDelta AssistantMessageEventType = "text_delta"
	// EventTextEnd indicates that a text content block has completed.
	EventTextEnd AssistantMessageEventType = "text_end"
	// EventThinkingStart indicates that a thinking content block is starting.
	EventThinkingStart AssistantMessageEventType = "thinking_start"
	// EventThinkingDelta indicates a thinking block incremental chunk (delta).
	EventThinkingDelta AssistantMessageEventType = "thinking_delta"
	// EventThinkingEnd indicates that a thinking content block has completed.
	EventThinkingEnd AssistantMessageEventType = "thinking_end"
	// EventToolCallStart indicates that a tool call is starting.
	EventToolCallStart AssistantMessageEventType = "toolcall_start"
	// EventToolCallDelta indicates a tool call block incremental chunk (delta).
	EventToolCallDelta AssistantMessageEventType = "toolcall_delta"
	// EventToolCallEnd indicates that a tool call block has completed.
	EventToolCallEnd AssistantMessageEventType = "toolcall_end"
	// EventDone indicates the successful completion of the stream.
	EventDone AssistantMessageEventType = "done"
	// EventError indicates that a stream error has occurred.
	EventError AssistantMessageEventType = "error"
)

// AssistantMessageEvent represents a single event in the streaming protocol.
type AssistantMessageEvent struct {
	// Type is the event discriminator.
	Type AssistantMessageEventType `json:"type"`
	// ContentIndex references the index of the content block being updated.
	ContentIndex *int `json:"contentIndex,omitempty"`
	// Delta contains the incremental text chunk for text, thinking, or tool calls.
	Delta string `json:"delta,omitempty"`
	// Content contains the final full text of the block upon completion.
	Content string `json:"content,omitempty"`
	// ToolCall contains the final completed tool call details.
	ToolCall *ToolCall `json:"toolCall,omitempty"`
	// Partial contains the intermediate partial assistant message state.
	Partial *AssistantMessage `json:"partial,omitempty"`
	// Message contains the final complete assistant message.
	Message *AssistantMessage `json:"message,omitempty"`
	// Error contains the assistant message state when an error occurred.
	Error *AssistantMessage `json:"error,omitempty"`
	// Reason is the stop reason when the stream completes or errors.
	Reason StopReason `json:"reason,omitempty"`
}
