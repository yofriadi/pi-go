package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Message is the interface representing any conversation message.
type Message interface {
	messageRole() Role
}

// UserMessage represents a message sent by the user.
type UserMessage struct {
	Content   any   `json:"content"`   // string or []UserContent; required
	Timestamp int64 `json:"timestamp"` // Unix epoch milliseconds
}

func (m UserMessage) messageRole() Role {
	return RoleUser
}

// AssistantMessage represents a message sent by the assistant.
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

func (m AssistantMessage) messageRole() Role {
	return RoleAssistant
}

type ToolResultMessage struct {
	ToolCallID string              `json:"toolCallId"` // required
	ToolName   string              `json:"toolName"`   // required
	Content    []ToolResultContent `json:"content,omitempty"`
	Details    any                 `json:"details,omitempty"`
	IsError    bool                `json:"isError,omitempty"`
	Timestamp  int64               `json:"timestamp"` // Unix epoch milliseconds
}

func (m ToolResultMessage) messageRole() Role {
	return RoleToolResult
}

// UserContent defines content blocks allowed in UserMessage.Content.
type UserContent interface {
	userContent()
	deepCopyUserContent() UserContent
}

// AssistantContent defines content blocks allowed in AssistantMessage.Content.
type AssistantContent interface {
	assistantContent()
	deepCopyAssistantContent() AssistantContent
}

// ToolResultContent defines content blocks allowed in ToolResultMessage.Content.
type ToolResultContent interface {
	toolResultContent()
	deepCopyToolResultContent() ToolResultContent
}

// TextContent represents a plain text block.
type TextContent struct {
	Text          string `json:"text"`
	TextSignature string `json:"textSignature,omitempty"`
}

func (t TextContent) userContent()       {}
func (t TextContent) assistantContent()  {}
func (t TextContent) toolResultContent() {}

func (t TextContent) deepCopyUserContent() UserContent {
	return TextContent{Text: t.Text, TextSignature: t.TextSignature}
}

func (t TextContent) deepCopyAssistantContent() AssistantContent {
	return TextContent{Text: t.Text, TextSignature: t.TextSignature}
}

func (t TextContent) deepCopyToolResultContent() ToolResultContent {
	return TextContent{Text: t.Text, TextSignature: t.TextSignature}
}

func (t TextContent) MarshalJSON() ([]byte, error) {
	type Alias TextContent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  "text",
		Alias: Alias(t),
	})
}

// ThinkingContent represents the reasoning/thinking block of an assistant.
type ThinkingContent struct {
	Thinking          string `json:"thinking"`
	ThinkingSignature string `json:"thinkingSignature,omitempty"`
	Redacted          bool   `json:"redacted,omitempty"`
}

func (t ThinkingContent) assistantContent() {}

func (t ThinkingContent) deepCopyAssistantContent() AssistantContent {
	return ThinkingContent{
		Thinking:          t.Thinking,
		ThinkingSignature: t.ThinkingSignature,
		Redacted:          t.Redacted,
	}
}

func (t ThinkingContent) MarshalJSON() ([]byte, error) {
	type Alias ThinkingContent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  "thinking",
		Alias: Alias(t),
	})
}

// ImageContent represents a reference to visual input.
type ImageContent struct {
	Data     string `json:"data"` // base64 representation
	MimeType string `json:"mimeType"`
}

func (i ImageContent) userContent()       {}
func (i ImageContent) toolResultContent() {}

func (i ImageContent) deepCopyUserContent() UserContent {
	return ImageContent{Data: i.Data, MimeType: i.MimeType}
}

func (i ImageContent) deepCopyToolResultContent() ToolResultContent {
	return ImageContent{Data: i.Data, MimeType: i.MimeType}
}

func (i ImageContent) MarshalJSON() ([]byte, error) {
	type Alias ImageContent
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  "image",
		Alias: Alias(i),
	})
}

// ToolCall represents a request to execute an external tool.
type ToolCall struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Arguments        map[string]any `json:"arguments"`
	ThoughtSignature string         `json:"thoughtSignature,omitempty"`
}

func (t ToolCall) assistantContent() {}

func (t ToolCall) deepCopyAssistantContent() AssistantContent {
	return ToolCall{
		ID:               t.ID,
		Name:             t.Name,
		Arguments:        deepCopyMap(t.Arguments),
		ThoughtSignature: t.ThoughtSignature,
	}
}

func (t ToolCall) MarshalJSON() ([]byte, error) {
	type Alias ToolCall
	return json.Marshal(&struct {
		Type string `json:"type"`
		Alias
	}{
		Type:  "toolCall",
		Alias: Alias(t),
	})
}

// AssistantMessageDiagnostic represents diagnostic information returned by assistant.
type AssistantMessageDiagnostic struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Details  any    `json:"details,omitempty"`
}

// MarshalJSON custom JSON implementation
func (m UserMessage) MarshalJSON() ([]byte, error) {
	if err := validateUserMessageContent(m.Content); err != nil {
		return nil, err
	}
	type Alias UserMessage
	return json.Marshal(&struct {
		Role Role `json:"role"`
		Alias
	}{
		Role:  RoleUser,
		Alias: Alias(m),
	})
}

func validateUserMessageContent(content any) error {
	switch c := content.(type) {
	case string:
		return nil
	case nil:
		return fmt.Errorf("invalid UserMessage content: expected string or []UserContent, got nil")
	case []UserContent:
		if c == nil {
			return fmt.Errorf("invalid UserMessage content: expected string or []UserContent, got nil []UserContent")
		}
		for i, block := range c {
			if isNilContentBlock(block) {
				return fmt.Errorf("invalid UserMessage content: nil block at index %d", i)
			}
		}
		return nil
	default:
		return fmt.Errorf("invalid UserMessage content: expected string or []UserContent, got %T", content)
	}
}

func validateAssistantMessageContent(content []AssistantContent) error {
	for i, block := range content {
		if isNilContentBlock(block) {
			return fmt.Errorf("invalid AssistantMessage content: nil block at index %d", i)
		}
	}
	return nil
}

func validateToolResultMessageContent(content []ToolResultContent) error {
	for i, block := range content {
		if isNilContentBlock(block) {
			return fmt.Errorf("invalid ToolResultMessage content: nil block at index %d", i)
		}
	}
	return nil
}

func isNilContentBlock(block any) bool {
	if block == nil {
		return true
	}
	v := reflect.ValueOf(block)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

func (m *UserMessage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Role      Role            `json:"role"`
		Content   json.RawMessage `json:"content"` // content must be present
		Timestamp int64           `json:"timestamp"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Role != RoleUser {
		return fmt.Errorf("invalid role %q for UserMessage", raw.Role)
	}
	if len(raw.Content) == 0 {
		return fmt.Errorf("missing required field \"content\" in UserMessage")
	}
	m.Timestamp = raw.Timestamp
	trimmed := bytes.TrimSpace(raw.Content)
	if len(trimmed) == 0 {
		m.Content = nil
		return nil
	}

	firstChar := trimmed[0]
	switch firstChar {
	case '"':
		var str string
		if err := json.Unmarshal(raw.Content, &str); err != nil {
			return err
		}
		m.Content = str
	case '[':
		var list []json.RawMessage
		if err := json.Unmarshal(raw.Content, &list); err != nil {
			return err
		}
		blocks := make([]UserContent, 0, len(list))
		for _, item := range list {
			block, err := unmarshalUserContent(item)
			if err != nil {
				return err
			}
			blocks = append(blocks, block)
		}
		m.Content = blocks
	default:
		return fmt.Errorf("invalid content format for UserMessage")
	}
	return nil
}

func unmarshalUserContent(data []byte) (UserContent, error) {
	var typeDetector struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeDetector); err != nil {
		return nil, err
	}
	switch typeDetector.Type {
	case "text":
		var tc TextContent
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, err
		}
		return tc, nil
	case "image":
		var ic ImageContent
		if err := json.Unmarshal(data, &ic); err != nil {
			return nil, err
		}
		return ic, nil
	case "":
		return nil, fmt.Errorf("missing type in UserContent block")
	default:
		return nil, fmt.Errorf("unknown type %q in UserContent block", typeDetector.Type)
	}
}

// MarshalJSON custom JSON implementation
func (m AssistantMessage) MarshalJSON() ([]byte, error) {
	if err := validateAssistantMessageContent(m.Content); err != nil {
		return nil, err
	}
	type Alias AssistantMessage
	return json.Marshal(&struct {
		Role Role `json:"role"`
		Alias
	}{
		Role:  RoleAssistant,
		Alias: Alias(m),
	})
}

func (m *AssistantMessage) UnmarshalJSON(data []byte) error {
	type Alias AssistantMessage
	var raw struct {
		Role    Role              `json:"role"`
		Content []json.RawMessage `json:"content,omitempty"`
		*Alias
	}
	raw.Alias = (*Alias)(m)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Role != RoleAssistant {
		return fmt.Errorf("invalid role %q for AssistantMessage", raw.Role)
	}

	if len(raw.Content) > 0 {
		var blocks []AssistantContent
		for _, item := range raw.Content {
			block, err := unmarshalAssistantContent(item)
			if err != nil {
				return err
			}
			blocks = append(blocks, block)
		}
		m.Content = blocks
	} else {
		m.Content = nil
	}
	return nil
}

func unmarshalAssistantContent(data []byte) (AssistantContent, error) {
	var typeDetector struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeDetector); err != nil {
		return nil, err
	}
	switch typeDetector.Type {
	case "text":
		var tc TextContent
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, err
		}
		return tc, nil
	case "thinking":
		var tc ThinkingContent
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, err
		}
		return tc, nil
	case "toolCall":
		var tc ToolCall
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, err
		}
		return tc, nil
	case "":
		return nil, fmt.Errorf("missing type in AssistantContent block")
	default:
		return nil, fmt.Errorf("unknown type %q in AssistantContent block", typeDetector.Type)
	}
}

// MarshalJSON custom JSON implementation
func (m ToolResultMessage) MarshalJSON() ([]byte, error) {
	if m.ToolCallID == "" {
		return nil, fmt.Errorf("invalid ToolResultMessage: missing required field \"toolCallId\"")
	}
	if m.ToolName == "" {
		return nil, fmt.Errorf("invalid ToolResultMessage: missing required field \"toolName\"")
	}
	if err := validateToolResultMessageContent(m.Content); err != nil {
		return nil, err
	}
	type Alias ToolResultMessage
	return json.Marshal(&struct {
		Role Role `json:"role"`
		Alias
	}{
		Role:  RoleToolResult,
		Alias: Alias(m),
	})
}

func (m *ToolResultMessage) UnmarshalJSON(data []byte) error {
	type Alias ToolResultMessage
	var raw struct {
		Role       Role              `json:"role"`
		ToolCallID *string           `json:"toolCallId"` // pointer to detect presence
		ToolName   *string           `json:"toolName"`   // pointer to detect presence
		Content    []json.RawMessage `json:"content,omitempty"`
		*Alias
	}
	raw.Alias = (*Alias)(m)
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Role != RoleToolResult {
		return fmt.Errorf("invalid role %q for ToolResultMessage", raw.Role)
	}
	if raw.ToolCallID == nil || *raw.ToolCallID == "" {
		return fmt.Errorf("missing required field \"toolCallId\" in ToolResultMessage")
	}
	if raw.ToolName == nil || *raw.ToolName == "" {
		return fmt.Errorf("missing required field \"toolName\" in ToolResultMessage")
	}
	m.ToolCallID = *raw.ToolCallID
	m.ToolName = *raw.ToolName
	if len(raw.Content) > 0 {
		var blocks []ToolResultContent
		for _, item := range raw.Content {
			block, err := unmarshalToolResultContent(item)
			if err != nil {
				return err
			}
			blocks = append(blocks, block)
		}
		m.Content = blocks
	} else {
		m.Content = nil
	}
	return nil
}

func unmarshalToolResultContent(data []byte) (ToolResultContent, error) {
	var typeDetector struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeDetector); err != nil {
		return nil, err
	}
	switch typeDetector.Type {
	case "text":
		var tc TextContent
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, err
		}
		return tc, nil
	case "image":
		var ic ImageContent
		if err := json.Unmarshal(data, &ic); err != nil {
			return nil, err
		}
		return ic, nil
	case "":
		return nil, fmt.Errorf("missing type in ToolResultContent block")
	default:
		return nil, fmt.Errorf("unknown type %q in ToolResultContent block", typeDetector.Type)
	}
}
