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
		return &msg, nil
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
