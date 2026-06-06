package agent

import "pi-go/pkg/ai"

// AgentMessage represents a message in the agent layer. It can wrap a standard
// ai.Message or represent UI-only / metadata messages.
type AgentMessage interface {
	Role() ai.Role
}

// LlmConvertible is an optional interface for AgentMessage types that can be
// converted to standard ai.Message types.
type LlmConvertible interface {
	ToLlm() (ai.Message, bool)
}

// UserMessage wraps ai.UserMessage for use in the agent layer.
type UserMessage struct {
	ai.UserMessage
}

func (m UserMessage) Role() ai.Role {
	return ai.RoleUser
}

func (m UserMessage) ToLlm() (ai.Message, bool) {
	return m.UserMessage, true
}

// AssistantMessage wraps ai.AssistantMessage for use in the agent layer.
type AssistantMessage struct {
	ai.AssistantMessage
}

func (m AssistantMessage) Role() ai.Role {
	return ai.RoleAssistant
}

func (m AssistantMessage) ToLlm() (ai.Message, bool) {
	return m.AssistantMessage, true
}

// ToolResultMessage wraps ai.ToolResultMessage for use in the agent layer.
type ToolResultMessage struct {
	ai.ToolResultMessage
}

func (m ToolResultMessage) Role() ai.Role {
	return ai.RoleToolResult
}

func (m ToolResultMessage) ToLlm() (ai.Message, bool) {
	return m.ToolResultMessage, true
}

// CustomMessage represents UI-only or metadata messages that are not sent to the LLM.
type CustomMessage struct {
	MsgRole ai.Role        `json:"role"`
	Content string         `json:"content"`
	Details map[string]any `json:"details,omitempty"`
}

func (m CustomMessage) Role() ai.Role {
	return m.MsgRole
}

// convertToLlm filters and converts a slice of AgentMessages to standard ai.Messages.
func convertToLlm(messages []AgentMessage) []ai.Message {
	var llmMessages []ai.Message
	for _, m := range messages {
		if m == nil {
			continue
		}
		if conv, ok := m.(LlmConvertible); ok {
			if am, ok2 := conv.ToLlm(); ok2 {
				llmMessages = append(llmMessages, am)
			}
		}
	}
	return llmMessages
}

// Wrap wraps an ai.Message into its corresponding AgentMessage wrapper.
func Wrap(msg ai.Message) AgentMessage {
	if msg == nil {
		return nil
	}
	switch m := msg.(type) {
	case ai.UserMessage:
		return UserMessage{m}
	case ai.AssistantMessage:
		return AssistantMessage{m}
	case ai.ToolResultMessage:
		return ToolResultMessage{m}
	case *ai.UserMessage:
		if m != nil {
			return UserMessage{*m}
		}
	case *ai.AssistantMessage:
		if m != nil {
			return AssistantMessage{*m}
		}
	case *ai.ToolResultMessage:
		if m != nil {
			return ToolResultMessage{*m}
		}
	}
	return nil
}
