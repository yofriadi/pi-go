package agent

import (
	"pi-go/pkg/ai"
)

// AgentEventType defines the category of an agent event.
type AgentEventType string

const (
	// EventTurnStart is emitted when a new loop iteration (turn) begins.
	EventTurnStart AgentEventType = "turn_start"

	// EventTurnEnd is emitted when a loop iteration (turn) completes.
	EventTurnEnd AgentEventType = "turn_end"

	// EventStreamStart is emitted when the assistant response stream starts.
	EventStreamStart AgentEventType = "stream_start"

	// EventStreamDelta is emitted for each partial update (delta) in the assistant stream.
	EventStreamDelta AgentEventType = "stream_delta"

	// EventStreamEnd is emitted when the assistant response stream finishes.
	EventStreamEnd AgentEventType = "stream_end"

	// EventToolExecutionStart is emitted before executing a tool.
	EventToolExecutionStart AgentEventType = "tool_execution_start"

	// EventToolExecutionEnd is emitted after a tool finishes execution.
	// In parallel mode, this is emitted in completion order.
	EventToolExecutionEnd AgentEventType = "tool_execution_end"

	// EventError is emitted when an error occurs during execution.
	EventError AgentEventType = "error"
)

// AgentEvent represents an event occurring during agent execution.
type AgentEvent struct {
	Type        AgentEventType            `json:"type"`
	StreamEvent *ai.AssistantMessageEvent `json:"streamEvent,omitempty"`
	ToolName    string                    `json:"toolName,omitempty"`
	ToolCallID  string                    `json:"toolCallId,omitempty"`
	ToolOutput  any                       `json:"toolOutput,omitempty"`
	IsError     bool                      `json:"isError,omitempty"`
	Error       error                     `json:"-"`
}
