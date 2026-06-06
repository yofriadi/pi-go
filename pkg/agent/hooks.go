package agent

import (
	"context"

	"pi-go/pkg/ai"
)

// Hooks provides custom callbacks for controlling and extending the agent loop.
// All hooks are optional.
type Hooks struct {
	// PrepareNextTurn is called before sending the conversation history to the LLM.
	// It can be used for context compaction, pruning, or injecting system messages.
	PrepareNextTurn func(ctx context.Context, history []AgentMessage) ([]AgentMessage, error)

	// ShouldStopAfterTurn is called at the end of each turn (after tool execution, if any).
	// If it returns true, the agent loop terminates early.
	ShouldStopAfterTurn func(ctx context.Context, history []AgentMessage) (bool, error)

	// BeforeToolCall is called before executing each tool call in a batch.
	// If it returns false, the tool execution is skipped.
	BeforeToolCall func(ctx context.Context, toolCall *ai.ToolCall) (bool, error)

	// AfterToolCall is called after each tool call completes.
	AfterToolCall func(ctx context.Context, toolCall *ai.ToolCall, result *ai.ToolResultMessage) error

	// GetSteeringMessages is called between turns to fetch messages that should be injected
	// into the conversation.
	GetSteeringMessages func(ctx context.Context) ([]AgentMessage, error)

	// GetFollowUpMessages is called when the agent would normally stop to fetch any
	// final follow-up messages that should be processed.
	GetFollowUpMessages func(ctx context.Context) ([]AgentMessage, error)

	// GetApiKey retrieves the OAuth/Codex token for the given provider.
	GetApiKey func(ctx context.Context, provider ai.ProviderID) (string, error)
}

// FillDefaults populates missing hooks with safe no-op implementations.
func (h *Hooks) FillDefaults() {
	if h.PrepareNextTurn == nil {
		h.PrepareNextTurn = func(ctx context.Context, history []AgentMessage) ([]AgentMessage, error) {
			return history, nil
		}
	}
	if h.ShouldStopAfterTurn == nil {
		h.ShouldStopAfterTurn = func(ctx context.Context, history []AgentMessage) (bool, error) {
			return false, nil
		}
	}
	if h.BeforeToolCall == nil {
		h.BeforeToolCall = func(ctx context.Context, toolCall *ai.ToolCall) (bool, error) {
			return true, nil
		}
	}
	if h.AfterToolCall == nil {
		h.AfterToolCall = func(ctx context.Context, toolCall *ai.ToolCall, result *ai.ToolResultMessage) error {
			return nil
		}
	}
	if h.GetSteeringMessages == nil {
		h.GetSteeringMessages = func(ctx context.Context) ([]AgentMessage, error) {
			return nil, nil
		}
	}
	if h.GetFollowUpMessages == nil {
		h.GetFollowUpMessages = func(ctx context.Context) ([]AgentMessage, error) {
			return nil, nil
		}
	}
	if h.GetApiKey == nil {
		h.GetApiKey = func(ctx context.Context, provider ai.ProviderID) (string, error) {
			return "", nil
		}
	}
}
