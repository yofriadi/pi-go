package ai

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	_ Message = UserMessage{}
	_ Message = AssistantMessage{}
	_ Message = ToolResultMessage{}
)

func TestJSONRoundTrip(t *testing.T) {
	// 1. UserMessage with string content
	userMsgStr := UserMessage{
		Content:   "Hello from user",
		Timestamp: 1716382103400,
	}
	data, err := json.Marshal(userMsgStr)
	if err != nil {
		t.Fatalf("failed to marshal UserMessage with string: %v", err)
	}

	// Verify role field injection
	var rawUser map[string]any
	if err := json.Unmarshal(data, &rawUser); err != nil {
		t.Fatalf("failed to unmarshal into map: %v", err)
	}
	if rawUser["role"] != "user" {
		t.Errorf("expected role 'user', got '%v'", rawUser["role"])
	}
	// Verify timestamp is serialized as a number, not string
	if _, ok := rawUser["timestamp"].(float64); !ok {
		t.Errorf("expected timestamp to be a float64/number in JSON, got %T", rawUser["timestamp"])
	}

	var userMsgStrDest UserMessage
	if err := json.Unmarshal(data, &userMsgStrDest); err != nil {
		t.Fatalf("failed to unmarshal UserMessage: %v", err)
	}
	if userMsgStrDest.Content != "Hello from user" {
		t.Errorf("expected Content 'Hello from user', got '%v'", userMsgStrDest.Content)
	}
	if userMsgStrDest.Timestamp != 1716382103400 {
		t.Errorf("expected timestamp 1716382103400, got %d", userMsgStrDest.Timestamp)
	}

	// 2. UserMessage with []UserContent (mixed text + image blocks)
	userMsgBlocks := UserMessage{
		Content: []UserContent{
			TextContent{Text: "Analyze this image", TextSignature: "sig-123"},
			ImageContent{Data: "base64data", MimeType: "image/png"},
		},
		Timestamp: 1716382103500,
	}
	dataBlocks, err := json.Marshal(userMsgBlocks)
	if err != nil {
		t.Fatalf("failed to marshal UserMessage with blocks: %v", err)
	}

	var userMsgBlocksDest UserMessage
	if err := json.Unmarshal(dataBlocks, &userMsgBlocksDest); err != nil {
		t.Fatalf("failed to unmarshal UserMessage with blocks: %v", err)
	}
	blocks, ok := userMsgBlocksDest.Content.([]UserContent)
	if !ok {
		t.Fatalf("expected Content to be []UserContent, got %T", userMsgBlocksDest.Content)
	}
	if len(blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(blocks))
	}
	tc, ok := blocks[0].(TextContent)
	if !ok {
		t.Errorf("expected first block to be TextContent, got %T", blocks[0])
	} else {
		if tc.Text != "Analyze this image" || tc.TextSignature != "sig-123" {
			t.Errorf("unexpected TextContent: %+v", tc)
		}
	}
	ic, ok := blocks[1].(ImageContent)
	if !ok {
		t.Errorf("expected second block to be ImageContent, got %T", blocks[1])
	} else {
		if ic.Data != "base64data" || ic.MimeType != "image/png" {
			t.Errorf("unexpected ImageContent: %+v", ic)
		}
	}

	// 3. AssistantMessage with text, thinking, and tool call blocks
	assistantMsg := AssistantMessage{
		Content: []AssistantContent{
			TextContent{Text: "Let me search that for you"},
			ThinkingContent{Thinking: "User wants to search...", ThinkingSignature: "think-sig"},
			ToolCall{ID: "call-1", Name: "search", Arguments: map[string]any{"query": "golang"}},
		},
		API:           APIIDOpenAICodexResponses,
		Provider:      ProviderIDOpenAICodex,
		Model:         "gpt-4",
		ResponseModel: "gpt-4-turbo",
		ResponseID:    "resp-123",
		Diagnostics: []AssistantMessageDiagnostic{
			{Code: "diag-1", Message: "Slow response", Severity: "warning"},
		},
		Usage: Usage{
			Input:       100,
			Output:      50,
			TotalTokens: 150,
		},
		StopReason:   StopReasonToolUse,
		ErrorMessage: "",
		Timestamp:    1716382103600,
	}

	dataAsst, err := json.Marshal(&assistantMsg)
	if err != nil {
		t.Fatalf("failed to marshal AssistantMessage: %v", err)
	}

	var rawAsst map[string]any
	if err := json.Unmarshal(dataAsst, &rawAsst); err != nil {
		t.Fatalf("failed to unmarshal into map: %v", err)
	}
	if rawAsst["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got '%v'", rawAsst["role"])
	}

	var assistantMsgDest AssistantMessage
	if err := json.Unmarshal(dataAsst, &assistantMsgDest); err != nil {
		t.Fatalf("failed to unmarshal AssistantMessage: %v", err)
	}
	if len(assistantMsgDest.Content) != 3 {
		t.Errorf("expected 3 content blocks, got %d", len(assistantMsgDest.Content))
	}
	if assistantMsgDest.StopReason != StopReasonToolUse {
		t.Errorf("expected StopReasonToolUse, got %v", assistantMsgDest.StopReason)
	}
	if len(assistantMsgDest.Diagnostics) != 1 || assistantMsgDest.Diagnostics[0].Code != "diag-1" {
		t.Errorf("diagnostics unmarshalled incorrectly")
	}

	// 4. ToolResultMessage with text + image blocks
	toolMsg := ToolResultMessage{
		ToolCallID: "call-1",
		ToolName:   "search",
		Content: []ToolResultContent{
			TextContent{Text: "Found 10 results"},
			ImageContent{Data: "image-data-b64", MimeType: "image/jpeg"},
		},
		Details:   map[string]any{"status": "success"},
		IsError:   false,
		Timestamp: 1716382103700,
	}

	dataTool, err := json.Marshal(toolMsg)
	if err != nil {
		t.Fatalf("failed to marshal ToolResultMessage: %v", err)
	}

	var rawTool map[string]any
	if err := json.Unmarshal(dataTool, &rawTool); err != nil {
		t.Fatalf("failed to unmarshal into map: %v", err)
	}
	if rawTool["role"] != "toolResult" {
		t.Errorf("expected role 'toolResult', got '%v'", rawTool["role"])
	}

	var toolMsgDest ToolResultMessage
	if err := json.Unmarshal(dataTool, &toolMsgDest); err != nil {
		t.Fatalf("failed to unmarshal ToolResultMessage: %v", err)
	}
	if toolMsgDest.ToolCallID != "call-1" || len(toolMsgDest.Content) != 2 {
		t.Errorf("unexpected ToolResultMessage details: expected 2 blocks, got %d", len(toolMsgDest.Content))
	}
	txtBlock, ok := toolMsgDest.Content[0].(TextContent)
	if !ok {
		t.Errorf("expected first block to be TextContent, got %T", toolMsgDest.Content[0])
	} else if txtBlock.Text != "Found 10 results" {
		t.Errorf("unexpected TextContent: %+v", txtBlock)
	}
	imgBlock, ok := toolMsgDest.Content[1].(ImageContent)
	if !ok {
		t.Errorf("expected second block to be ImageContent, got %T", toolMsgDest.Content[1])
	} else if imgBlock.Data != "image-data-b64" || imgBlock.MimeType != "image/jpeg" {
		t.Errorf("unexpected ImageContent: %+v", imgBlock)
	}

	// 5. Nil *AssistantMessage MarshalJSON regression test
	var nilAsst *AssistantMessage
	nilAsstBytes, err := json.Marshal(nilAsst)
	if err != nil {
		t.Fatalf("failed to marshal nil AssistantMessage: %v", err)
	}
	if string(nilAsstBytes) != "null" {
		t.Errorf("expected nil *AssistantMessage to marshal to 'null', got '%s'", string(nilAsstBytes))
	}

	// 6. AssistantMessage value (not pointer) still injects role
	asstVal := AssistantMessage{
		Content:   []AssistantContent{TextContent{Text: "value test"}},
		Timestamp: 1716382103800,
	}
	dataVal, err := json.Marshal(asstVal)
	if err != nil {
		t.Fatalf("failed to marshal AssistantMessage value: %v", err)
	}
	var rawVal map[string]any
	if err := json.Unmarshal(dataVal, &rawVal); err != nil {
		t.Fatalf("failed to unmarshal value-marshaled AssistantMessage: %v", err)
	}
	if rawVal["role"] != "assistant" {
		t.Errorf("expected role 'assistant' on value-marshaled AssistantMessage, got '%v'", rawVal["role"])
	}
}

func TestPolymorphicMessageUnmarshal(t *testing.T) {
	ctxJSON := `{
		"systemPrompt": "You are a helpful assistant",
		"messages": [
			{
				"role": "user",
				"content": "Hello",
				"timestamp": 1716382103400
			},
			{
				"role": "assistant",
				"content": [
					{
						"type": "text",
						"text": "Hi there!"
					}
				],
				"timestamp": 1716382103500
			},
			{
				"role": "toolResult",
				"toolCallId": "call-1",
				"toolName": "search",
				"content": [
					{
						"type": "text",
						"text": "Result data"
					}
				],
				"timestamp": 1716382103600
			}
		],
		"tools": [
			{
				"name": "search",
				"description": "web search"
			}
		]
	}`

	var ctx Context
	if err := json.Unmarshal([]byte(ctxJSON), &ctx); err != nil {
		t.Fatalf("failed to unmarshal Context: %v", err)
	}

	if ctx.SystemPrompt != "You are a helpful assistant" {
		t.Errorf("expected systemPrompt, got %q", ctx.SystemPrompt)
	}
	if len(ctx.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(ctx.Messages))
	}
	if len(ctx.Tools) != 1 || ctx.Tools[0].Name != "search" {
		t.Errorf("expected 1 tool 'search', got %+v", ctx.Tools)
	}

	// Verify polymorphic message types
	userMsg, ok := ctx.Messages[0].(UserMessage)
	if !ok {
		t.Errorf("expected first message to be UserMessage, got %T", ctx.Messages[0])
	} else if userMsg.Content != "Hello" {
		t.Errorf("expected first message content 'Hello', got %v", userMsg.Content)
	}

	asstMsg, ok := ctx.Messages[1].(AssistantMessage)
	if !ok {
		t.Errorf("expected second message to be AssistantMessage, got %T", ctx.Messages[1])
	} else if len(asstMsg.Content) != 1 || asstMsg.Content[0].(TextContent).Text != "Hi there!" {
		t.Errorf("unexpected assistant message content")
	}

	toolMsg, ok := ctx.Messages[2].(ToolResultMessage)
	if !ok {
		t.Errorf("expected third message to be ToolResultMessage, got %T", ctx.Messages[2])
	} else if toolMsg.ToolCallID != "call-1" {
		t.Errorf("unexpected tool result message ID")
	}
}

func TestContentBlockTypeDiscrimination(t *testing.T) {
	// Unknown content block type in UserMessage
	invalidUserJSON := `{
		"role": "user",
		"content": [
			{
				"type": "future-type",
				"data": "123"
			}
		],
		"timestamp": 12345
	}`
	var userMsg UserMessage
	err := json.Unmarshal([]byte(invalidUserJSON), &userMsg)
	if err == nil {
		t.Errorf("expected error when unmarshalling unknown content block type in UserMessage, got nil")
	} else if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("expected error message to contain 'unknown type', got: %v", err)
	}

	// Mismatched role for UserMessage (marshalled user json with role 'assistant')
	mismatchedRoleJSON := `{
		"role": "assistant",
		"content": "Hello",
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(mismatchedRoleJSON), &userMsg)
	if err == nil {
		t.Errorf("expected error for role mismatch, got nil")
	}

	// Mismatched role for AssistantMessage (role 'user')
	var asstMsg AssistantMessage
	mismatchedRoleAsstJSON := `{
		"role": "user",
		"content": [{"type": "text", "text": "hello"}],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(mismatchedRoleAsstJSON), &asstMsg)
	if err == nil {
		t.Errorf("expected error for AssistantMessage role mismatch, got nil")
	}

	// Mismatched role for ToolResultMessage (role 'user')
	var toolMsg ToolResultMessage
	mismatchedRoleToolJSON := `{
		"role": "user",
		"toolCallId": "call-1",
		"toolName": "search",
		"content": [{"type": "text", "text": "hello"}],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(mismatchedRoleToolJSON), &toolMsg)
	if err == nil {
		t.Errorf("expected error for ToolResultMessage role mismatch, got nil")
	}

	// Missing role for UserMessage
	missingRoleJSON := `{
		"content": "Hello",
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(missingRoleJSON), &userMsg)
	if err == nil {
		t.Errorf("expected error for missing role in UserMessage, got nil")
	}

	// Missing role for AssistantMessage
	var asstMsgMissing AssistantMessage
	missingRoleAsstJSON := `{
		"content": [{"type": "text", "text": "hello"}],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(missingRoleAsstJSON), &asstMsgMissing)
	if err == nil {
		t.Errorf("expected error for missing role in AssistantMessage, got nil")
	}

	// Missing role for ToolResultMessage
	var toolMsgMissing ToolResultMessage
	missingRoleToolJSON := `{
		"toolCallId": "call-1",
		"content": [{"type": "text", "text": "hello"}],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(missingRoleToolJSON), &toolMsgMissing)
	if err == nil {
		t.Errorf("expected error for missing role in ToolResultMessage, got nil")
	}

	// Unknown content block type in AssistantMessage
	invalidAsstJSON := `{
		"role": "assistant",
		"content": [
			{
				"type": "unsupported-block",
				"text": "hi"
			}
		],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(invalidAsstJSON), &asstMsg)
	if err == nil {
		t.Errorf("expected error when unmarshalling unknown content block type in AssistantMessage, got nil")
	}

	// Unknown content block type in ToolResultMessage
	invalidToolJSON := `{
		"role": "toolResult",
		"toolCallId": "call-1",
		"toolName": "search",
		"content": [
			{
				"type": "unsupported-tool-block",
				"text": "hi"
			}
		],
		"timestamp": 12345
	}`
	err = json.Unmarshal([]byte(invalidToolJSON), &toolMsg)
	if err == nil {
		t.Errorf("expected error when unmarshalling unknown content block type in ToolResultMessage, got nil")
	}

	// Verify type of ToolCall in JSON
	tc := ToolCall{ID: "1", Name: "test", Arguments: map[string]any{}}
	tcBytes, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("failed to marshal ToolCall: %v", err)
	}
	var tcMap map[string]any
	if err := json.Unmarshal(tcBytes, &tcMap); err != nil {
		t.Fatalf("failed to unmarshal ToolCall bytes: %v", err)
	}
	if tcMap["type"] != "toolCall" {
		t.Errorf("expected ToolCall type to be 'toolCall' in JSON, got '%v'", tcMap["type"])
	}
}

func TestMessageRejectsTypedNilMarshalContent(t *testing.T) {
	var userText *TextContent
	var userImage *ImageContent
	var assistantText *TextContent
	var assistantThinking *ThinkingContent
	var assistantToolCall *ToolCall
	var toolResultText *TextContent
	var toolResultImage *ImageContent

	tests := []struct {
		name string
		msg  any
	}{
		{name: "user typed nil text", msg: UserMessage{Content: []UserContent{userText}, Timestamp: 12345}},
		{name: "user typed nil image", msg: UserMessage{Content: []UserContent{userImage}, Timestamp: 12345}},
		{name: "assistant typed nil text", msg: AssistantMessage{Content: []AssistantContent{assistantText}, Timestamp: 12345}},
		{name: "assistant typed nil thinking", msg: AssistantMessage{Content: []AssistantContent{assistantThinking}, Timestamp: 12345}},
		{name: "assistant typed nil tool call", msg: AssistantMessage{Content: []AssistantContent{assistantToolCall}, Timestamp: 12345}},
		{name: "tool result typed nil text", msg: ToolResultMessage{ToolCallID: "call-1", ToolName: "search", Content: []ToolResultContent{toolResultText}, Timestamp: 12345}},
		{name: "tool result typed nil image", msg: ToolResultMessage{ToolCallID: "call-1", ToolName: "search", Content: []ToolResultContent{toolResultImage}, Timestamp: 12345}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := json.Marshal(tt.msg)
			if err == nil {
				t.Fatalf("expected typed nil message content to fail marshal")
			}
			if !strings.Contains(err.Error(), "nil block") {
				t.Fatalf("expected nil block error, got %v", err)
			}
		})
	}
}

func TestMessageRejectsInvalidMarshalContent(t *testing.T) {
	tests := []struct {
		name string
		msg  any
	}{
		{name: "user number", msg: UserMessage{Content: 42, Timestamp: 12345}},
		{name: "user object", msg: UserMessage{Content: map[string]any{"text": "hello"}, Timestamp: 12345}},
		{name: "user arbitrary slice", msg: UserMessage{Content: []string{"hello"}, Timestamp: 12345}},
		{name: "user nil block", msg: UserMessage{Content: []UserContent{nil}, Timestamp: 12345}},
		{name: "user nil slice", msg: UserMessage{Content: []UserContent(nil), Timestamp: 12345}},
		{name: "assistant nil block", msg: AssistantMessage{Content: []AssistantContent{nil}, Timestamp: 12345}},
		{name: "tool result nil block", msg: ToolResultMessage{ToolCallID: "call-1", ToolName: "search", Content: []ToolResultContent{nil}, Timestamp: 12345}},
		{name: "tool result empty call id", msg: ToolResultMessage{ToolCallID: "", ToolName: "search", Content: []ToolResultContent{TextContent{Text: "ok"}}, Timestamp: 12345}},
		{name: "tool result empty tool name", msg: ToolResultMessage{ToolCallID: "call-1", ToolName: "", Content: []ToolResultContent{TextContent{Text: "ok"}}, Timestamp: 12345}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := json.Marshal(tt.msg)
			if err == nil {
				t.Fatalf("expected invalid message content to fail marshal")
			}
			if !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("expected invalid content error, got %v", err)
			}
		})
	}
}

func TestDeepCopyIsolation(t *testing.T) {
	// Mutating a deep-copied AssistantMessage.Content slice does not affect the original
	origAsst := &AssistantMessage{
		Content: []AssistantContent{
			TextContent{Text: "orig text"},
		},
		Diagnostics: []AssistantMessageDiagnostic{
			{Code: "D1", Message: "Err", Details: map[string]any{"meta": "val"}},
		},
	}

	copiedMsg := origAsst.DeepCopy()
	copiedAsst, ok := copiedMsg.(*AssistantMessage)
	if !ok {
		t.Fatalf("expected *AssistantMessage from DeepCopy, got %T", copiedMsg)
	}

	// Mutate copy
	copiedAsst.Content[0] = TextContent{Text: "mutated text"}
	copiedAsst.Diagnostics[0].Details.(map[string]any)["meta"] = "mutated val"

	// Verify original is unaffected
	origText := origAsst.Content[0].(TextContent).Text
	if origText != "orig text" {
		t.Errorf("original content mutated: expected 'orig text', got '%s'", origText)
	}
	origMeta := origAsst.Diagnostics[0].Details.(map[string]any)["meta"]
	if origMeta != "val" {
		t.Errorf("original diagnostic details mutated: expected 'val', got '%v'", origMeta)
	}

	// Mutating a deep-copied ToolCall.Arguments map does not affect the original
	tc := ToolCall{
		ID:        "tc-1",
		Name:      "test",
		Arguments: map[string]any{"arg1": "val1", "nested": []any{1, 2}},
	}
	copiedTC := tc.deepCopyAssistantContent().(ToolCall)
	copiedTC.Arguments["arg1"] = "mutated arg1"
	copiedTC.Arguments["nested"].([]any)[0] = 99

	if tc.Arguments["arg1"] != "val1" {
		t.Errorf("original tool call arguments mutated: expected 'val1', got '%v'", tc.Arguments["arg1"])
	}
	if tc.Arguments["nested"].([]any)[0].(int) != 1 {
		t.Errorf("original nested slice in tool call arguments mutated: expected 1, got '%v'", tc.Arguments["nested"].([]any)[0])
	}

	// Mutating a deep-copied typed slice inside Arguments does not affect the original
	tcTyped := ToolCall{
		ID:        "tc-typed",
		Name:      "test",
		Arguments: map[string]any{"nums": []int{10, 20, 30}},
	}
	copiedTCTyped := tcTyped.deepCopyAssistantContent().(ToolCall)
	copiedNums := copiedTCTyped.Arguments["nums"].([]int)
	copiedNums[0] = 999
	origNums := tcTyped.Arguments["nums"].([]int)
	if origNums[0] != 10 {
		t.Errorf("original typed slice mutated: expected 10, got %d", origNums[0])
	}

	// Mutating a deep-copied typed map inside Arguments does not affect the original
	tcTypedMap := ToolCall{
		ID:        "tc-typed-map",
		Name:      "test",
		Arguments: map[string]any{"lookup": map[int]string{1: "one", 2: "two"}},
	}
	copiedTCTypedMap := tcTypedMap.deepCopyAssistantContent().(ToolCall)
	copiedMap := copiedTCTypedMap.Arguments["lookup"].(map[int]string)
	copiedMap[1] = "mutated-one"
	origMap := tcTypedMap.Arguments["lookup"].(map[int]string)
	if origMap[1] != "one" {
		t.Errorf("original typed map mutated: expected 'one', got '%s'", origMap[1])
	}
}

func TestModelHelpers(t *testing.T) {
	// Testing level clamping and support detection
	highLevel := "high"
	xhighLevel := "max"
	model := Model{
		ID:        "test-model",
		Reasoning: true,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelHigh:   &highLevel,
			ModelThinkingLevelXHigh:  &xhighLevel,
			ModelThinkingLevelMedium: nil, // explicitly disabled
		},
	}

	supported := GetSupportedThinkingLevels(model)
	// should contain off, low (since not disabled/nil), high, xhigh. Should not contain medium.
	contains := func(slice []ModelThinkingLevel, val ModelThinkingLevel) bool {
		return slices.Contains(slice, val)
	}

	if !contains(supported, ModelThinkingLevelOff) {
		t.Errorf("expected off to be supported")
	}
	if contains(supported, ModelThinkingLevelMedium) {
		t.Errorf("expected medium to be unsupported (nil mapped pointer)")
	}
	if !contains(supported, ModelThinkingLevelHigh) {
		t.Errorf("expected high to be supported")
	}
	if !contains(supported, ModelThinkingLevelXHigh) {
		t.Errorf("expected xhigh to be supported")
	}

	// Clamp test
	clamped := ClampThinkingLevel(model, ModelThinkingLevelMedium)
	// available are off, minimal, low, high, xhigh. medium is missing.
	// requested index for medium is 3. We search forward first -> high (index 4) which is available.
	if clamped != ModelThinkingLevelHigh {
		t.Errorf("expected medium to clamp to high, got %v", clamped)
	}

	// CalculateCost test
	model.Cost = ModelCost{
		Input:      15.0,
		Output:     60.0,
		CacheRead:  5.0,
		CacheWrite: 10.0,
	}
	usage := Usage{
		Input:      1000000,
		Output:     500000,
		CacheRead:  2000000,
		CacheWrite: 100000,
	}
	cost := CalculateCost(model, usage)
	if cost.Input != 15.0 {
		t.Errorf("expected input cost 15.0, got %v", cost.Input)
	}
	if cost.Output != 30.0 {
		t.Errorf("expected output cost 30.0, got %v", cost.Output)
	}
	if cost.CacheRead != 10.0 {
		t.Errorf("expected cacheRead cost 10.0, got %v", cost.CacheRead)
	}
	if cost.CacheWrite != 1.0 {
		t.Errorf("expected cacheWrite cost 1.0, got %v", cost.CacheWrite)
	}
	if cost.Total != 56.0 {
		t.Errorf("expected total cost 56.0, got %v", cost.Total)
	}

	nonReasoning := Model{Reasoning: false}
	nonReasoningSupported := GetSupportedThinkingLevels(nonReasoning)
	if !slices.Equal(nonReasoningSupported, []ModelThinkingLevel{ModelThinkingLevelOff}) {
		t.Fatalf("expected non-reasoning model to support only off, got %v", nonReasoningSupported)
	}

	defaultReasoning := Model{Reasoning: true}
	defaultSupported := GetSupportedThinkingLevels(defaultReasoning)
	if contains(defaultSupported, ModelThinkingLevelXHigh) {
		t.Fatalf("expected xhigh to require an explicit map entry")
	}
	if ClampThinkingLevel(defaultReasoning, ModelThinkingLevelXHigh) != ModelThinkingLevelHigh {
		t.Fatalf("expected xhigh to clamp down to high without explicit xhigh support")
	}

	sameModelA := &Model{ID: "model-a", Provider: ProviderIDOpenAICodex}
	sameModelB := &Model{ID: "model-a", Provider: ProviderIDOpenAICodex}
	otherProvider := &Model{ID: "model-a", Provider: "other-provider"}
	if !ModelsAreEqual(sameModelA, sameModelB) {
		t.Fatalf("expected same id and provider to compare equal")
	}
	if ModelsAreEqual(sameModelA, otherProvider) {
		t.Fatalf("expected different providers to compare unequal")
	}
	if ModelsAreEqual(sameModelA, nil) {
		t.Fatalf("expected nil model to compare unequal")
	}
}

func TestBuildBaseOptions(t *testing.T) {
	model := Model{
		MaxTokens: 4096,
	}
	var baseMaxTokens int = 2048
	simpleOpts := &SimpleStreamOptions{
		StreamOptions: StreamOptions{
			MaxTokens: &baseMaxTokens,
		},
		Reasoning: ModelThinkingLevelLow,
	}

	opts := BuildBaseOptions(model, simpleOpts)
	if opts.MaxTokens == nil {
		t.Fatalf("expected MaxTokens to not be nil")
	}
	// With low reasoning, default budget is 2048.
	// maxTokens is min(baseMaxTokens + budget, modelMaxTokens) = min(2048 + 2048, 4096) = 4096.
	if *opts.MaxTokens != 4096 {
		t.Errorf("expected adjusted MaxTokens to be 4096, got %d", *opts.MaxTokens)
	}

	// Verify original simpleOpts is not mutated.
	if *simpleOpts.StreamOptions.MaxTokens != 2048 {
		t.Errorf("original StreamOptions.MaxTokens was mutated: expected 2048, got %d", *simpleOpts.StreamOptions.MaxTokens)
	}

	// Test 2: Reasoning is empty string "" (omitted reasoning)
	simpleOptsEmpty := &SimpleStreamOptions{
		StreamOptions: StreamOptions{
			MaxTokens: &baseMaxTokens,
		},
		Reasoning: "",
	}
	optsEmpty := BuildBaseOptions(model, simpleOptsEmpty)
	if optsEmpty.MaxTokens == nil {
		t.Fatalf("expected MaxTokens to not be nil")
	}
	if *optsEmpty.MaxTokens != 2048 {
		t.Errorf("expected MaxTokens to be unchanged (2048) for empty reasoning, got %d", *optsEmpty.MaxTokens)
	}

	// Test 3: Reasoning is explicitly ModelThinkingLevelOff
	simpleOptsOff := &SimpleStreamOptions{
		StreamOptions: StreamOptions{
			MaxTokens: &baseMaxTokens,
		},
		Reasoning: ModelThinkingLevelOff,
	}
	optsOff := BuildBaseOptions(model, simpleOptsOff)
	if optsOff.MaxTokens == nil {
		t.Fatalf("expected MaxTokens to not be nil")
	}
	if *optsOff.MaxTokens != 2048 {
		t.Errorf("expected MaxTokens to be unchanged (2048) for off reasoning, got %d", *optsOff.MaxTokens)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := Context{
		SystemPrompt: "System prompt test",
		Messages: []Message{
			UserMessage{
				Content:   "User text content",
				Timestamp: 1000,
			},
			UserMessage{
				Content: []UserContent{
					TextContent{Text: "Text in user blocks", TextSignature: "user-sig"},
					ImageContent{Data: "img-data", MimeType: "image/png"},
				},
				Timestamp: 2000,
			},
			&AssistantMessage{
				Content: []AssistantContent{
					TextContent{Text: "Assistant text"},
					ThinkingContent{Thinking: "Thinking text", ThinkingSignature: "think-sig", Redacted: true},
					ToolCall{ID: "call-99", Name: "my-tool", Arguments: map[string]any{"x": float64(123)}},
				},
				API:        APIIDOpenAICodexResponses,
				Provider:   ProviderIDOpenAICodex,
				StopReason: StopReasonStop,
				Timestamp:  3000,
			},
			ToolResultMessage{
				ToolCallID: "call-99",
				ToolName:   "my-tool",
				Content: []ToolResultContent{
					TextContent{Text: "Tool response"},
				},
				Details:   map[string]any{"ok": true},
				Timestamp: 4000,
			},
		},
		Tools: []ToolDefinition{
			{
				Name:        "my-tool",
				Description: "some tool description",
				Parameters:  map[string]any{"type": "object"},
			},
			{
				Name:        "empty-tool",
				Description: "has empty/nil parameters",
				Parameters:  nil,
			},
		},
	}

	data, err := json.Marshal(ctx)
	if err != nil {
		t.Fatalf("failed to marshal Context: %v", err)
	}

	// Verify the raw JSON structure
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal raw context JSON: %v", err)
	}

	// Verify tools parameters is marshaled without omitempty
	toolsVal, ok := raw["tools"].([]any)
	if !ok || len(toolsVal) != 2 {
		t.Fatalf("expected 2 tools in raw context JSON")
	}
	t0, ok := toolsVal[0].(map[string]any)
	if !ok {
		t.Fatalf("expected tool 0 to be a map")
	}
	if _, exists := t0["parameters"]; !exists {
		t.Errorf("expected 'parameters' field in tool 0, but it was missing")
	}
	t1, ok := toolsVal[1].(map[string]any)
	if !ok {
		t.Fatalf("expected tool 1 to be a map")
	}
	if _, exists := t1["parameters"]; !exists {
		t.Errorf("expected 'parameters' field in tool 1, but it was missing (incorrect omitempty)")
	}

	// Check messages structure
	msgsVal, ok := raw["messages"].([]any)
	if !ok || len(msgsVal) != 4 {
		t.Fatalf("expected 4 messages in raw context JSON, got %v", msgsVal)
	}

	roles := []string{"user", "user", "assistant", "toolResult"}
	for i, mVal := range msgsVal {
		mMap, ok := mVal.(map[string]any)
		if !ok {
			t.Fatalf("expected message %d to be a map", i)
		}
		if mMap["role"] != roles[i] {
			t.Errorf("expected message %d role to be %s, got %v", i, roles[i], mMap["role"])
		}
	}

	// Unmarshal back and check equality
	var roundTrip Context
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("failed to unmarshal round-tripped Context: %v", err)
	}

	if roundTrip.SystemPrompt != ctx.SystemPrompt {
		t.Errorf("expected SystemPrompt %q, got %q", ctx.SystemPrompt, roundTrip.SystemPrompt)
	}

	if len(roundTrip.Tools) != 2 || roundTrip.Tools[0].Name != "my-tool" || roundTrip.Tools[1].Name != "empty-tool" {
		t.Fatalf("round-tripped tools mismatch")
	}
	if roundTrip.Tools[0].Parameters == nil || roundTrip.Tools[0].Parameters["type"] != "object" {
		t.Errorf("round-tripped tool parameters mismatch: got %v", roundTrip.Tools[0].Parameters)
	}

	if len(roundTrip.Messages) != 4 {
		t.Fatalf("expected 4 round-tripped messages, got %d", len(roundTrip.Messages))
	}

	// 1. UserMessage string content
	m0, ok := roundTrip.Messages[0].(UserMessage)
	if !ok {
		t.Errorf("expected UserMessage, got %T", roundTrip.Messages[0])
	} else {
		if m0.Content != "User text content" {
			t.Errorf("unexpected content for m0: %v", m0.Content)
		}
		if m0.Timestamp != 1000 {
			t.Errorf("unexpected timestamp for m0: %d", m0.Timestamp)
		}
	}

	// 2. UserMessage block content
	m1, ok := roundTrip.Messages[1].(UserMessage)
	if !ok {
		t.Errorf("expected UserMessage, got %T", roundTrip.Messages[1])
	} else {
		blocks, ok := m1.Content.([]UserContent)
		if !ok || len(blocks) != 2 {
			t.Fatalf("expected 2 UserContent blocks, got %T", m1.Content)
		}
		txt, ok := blocks[0].(TextContent)
		if !ok || txt.Text != "Text in user blocks" || txt.TextSignature != "user-sig" {
			t.Errorf("unexpected UserContent text block: %v", blocks[0])
		}
		img, ok := blocks[1].(ImageContent)
		if !ok || img.Data != "img-data" || img.MimeType != "image/png" {
			t.Errorf("unexpected UserContent image block: %v", blocks[1])
		}
	}

	// 3. AssistantMessage
	m2, ok := roundTrip.Messages[2].(AssistantMessage)
	if !ok {
		t.Errorf("expected AssistantMessage, got %T", roundTrip.Messages[2])
	} else {
		if m2.Timestamp != 3000 || m2.API != APIIDOpenAICodexResponses || m2.Provider != ProviderIDOpenAICodex {
			t.Errorf("unexpected assistant message metadata: %+v", m2)
		}
		if len(m2.Content) != 3 {
			t.Fatalf("expected 3 assistant content blocks, got %d", len(m2.Content))
		}
		txt, ok := m2.Content[0].(TextContent)
		if !ok || txt.Text != "Assistant text" {
			t.Errorf("unexpected TextContent block: %+v", m2.Content[0])
		}
		think, ok := m2.Content[1].(ThinkingContent)
		if !ok || think.Thinking != "Thinking text" || think.ThinkingSignature != "think-sig" || !think.Redacted {
			t.Errorf("unexpected ThinkingContent block: %+v", m2.Content[1])
		}
		tc, ok := m2.Content[2].(ToolCall)
		if !ok || tc.ID != "call-99" || tc.Name != "my-tool" || tc.Arguments["x"] != 123.0 {
			t.Errorf("unexpected ToolCall block: %+v", m2.Content[2])
		}
	}

	// 4. ToolResultMessage
	m3, ok := roundTrip.Messages[3].(ToolResultMessage)
	if !ok {
		t.Errorf("expected ToolResultMessage, got %T", roundTrip.Messages[3])
	} else {
		if m3.ToolCallID != "call-99" || m3.ToolName != "my-tool" || m3.Timestamp != 4000 {
			t.Errorf("unexpected tool result message metadata")
		}
		if len(m3.Content) != 1 {
			t.Fatalf("expected 1 content block in tool result")
		}
		txt, ok := m3.Content[0].(TextContent)
		if !ok || txt.Text != "Tool response" {
			t.Errorf("unexpected tool result content block: %+v", m3.Content[0])
		}
		detailsMap, ok := m3.Details.(map[string]any)
		if !ok || detailsMap["ok"] != true {
			t.Errorf("unexpected tool result details: %v", m3.Details)
		}
	}
}

func TestFoundationConstantsAndJSON(t *testing.T) {
	// 1. Roles
	if RoleUser != "user" {
		t.Errorf("RoleUser: expected 'user', got %q", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Errorf("RoleAssistant: expected 'assistant', got %q", RoleAssistant)
	}
	if RoleToolResult != "toolResult" {
		t.Errorf("RoleToolResult: expected 'toolResult', got %q", RoleToolResult)
	}

	// 2. Stop Reasons
	if StopReasonStop != "stop" {
		t.Errorf("StopReasonStop: expected 'stop', got %q", StopReasonStop)
	}
	if StopReasonLength != "length" {
		t.Errorf("StopReasonLength: expected 'length', got %q", StopReasonLength)
	}
	if StopReasonToolUse != "toolUse" {
		t.Errorf("StopReasonToolUse: expected 'toolUse', got %q", StopReasonToolUse)
	}
	if StopReasonError != "error" {
		t.Errorf("StopReasonError: expected 'error', got %q", StopReasonError)
	}
	if StopReasonAborted != "aborted" {
		t.Errorf("StopReasonAborted: expected 'aborted', got %q", StopReasonAborted)
	}

	// 3. Thinking Levels
	if ThinkingLevelMinimal != "minimal" {
		t.Errorf("ThinkingLevelMinimal: expected 'minimal', got %q", ThinkingLevelMinimal)
	}
	if ThinkingLevelLow != "low" {
		t.Errorf("ThinkingLevelLow: expected 'low', got %q", ThinkingLevelLow)
	}
	if ThinkingLevelMedium != "medium" {
		t.Errorf("ThinkingLevelMedium: expected 'medium', got %q", ThinkingLevelMedium)
	}
	if ThinkingLevelHigh != "high" {
		t.Errorf("ThinkingLevelHigh: expected 'high', got %q", ThinkingLevelHigh)
	}
	if ThinkingLevelXHigh != "xhigh" {
		t.Errorf("ThinkingLevelXHigh: expected 'xhigh', got %q", ThinkingLevelXHigh)
	}

	// 4. Model Thinking Levels
	if ModelThinkingLevelOff != "off" {
		t.Errorf("ModelThinkingLevelOff: expected 'off', got %q", ModelThinkingLevelOff)
	}
	if ModelThinkingLevelMinimal != "minimal" {
		t.Errorf("ModelThinkingLevelMinimal: expected 'minimal', got %q", ModelThinkingLevelMinimal)
	}
	if ModelThinkingLevelLow != "low" {
		t.Errorf("ModelThinkingLevelLow: expected 'low', got %q", ModelThinkingLevelLow)
	}
	if ModelThinkingLevelMedium != "medium" {
		t.Errorf("ModelThinkingLevelMedium: expected 'medium', got %q", ModelThinkingLevelMedium)
	}
	if ModelThinkingLevelHigh != "high" {
		t.Errorf("ModelThinkingLevelHigh: expected 'high', got %q", ModelThinkingLevelHigh)
	}
	if ModelThinkingLevelXHigh != "xhigh" {
		t.Errorf("ModelThinkingLevelXHigh: expected 'xhigh', got %q", ModelThinkingLevelXHigh)
	}

	// 5. Input Kinds
	if InputKindText != "text" {
		t.Errorf("InputKindText: expected 'text', got %q", InputKindText)
	}
	if InputKindImage != "image" {
		t.Errorf("InputKindImage: expected 'image', got %q", InputKindImage)
	}

	// 6. Transport
	if TransportSSE != "sse" {
		t.Errorf("TransportSSE: expected 'sse', got %q", TransportSSE)
	}
	if TransportWebSocket != "websocket" {
		t.Errorf("TransportWebSocket: expected 'websocket', got %q", TransportWebSocket)
	}
	if TransportWebSocketCached != "websocket-cached" {
		t.Errorf("TransportWebSocketCached: expected 'websocket-cached', got %q", TransportWebSocketCached)
	}
	if TransportAuto != "auto" {
		t.Errorf("TransportAuto: expected 'auto', got %q", TransportAuto)
	}

	// 7. Cache Retention
	if CacheRetentionNone != "none" {
		t.Errorf("CacheRetentionNone: expected 'none', got %q", CacheRetentionNone)
	}
	if CacheRetentionShort != "short" {
		t.Errorf("CacheRetentionShort: expected 'short', got %q", CacheRetentionShort)
	}
	if CacheRetentionLong != "long" {
		t.Errorf("CacheRetentionLong: expected 'long', got %q", CacheRetentionLong)
	}

	// 8. APIID and ProviderID
	if APIIDOpenAICodexResponses != "openai-codex-responses" {
		t.Errorf("APIIDOpenAICodexResponses: expected 'openai-codex-responses', got %q", APIIDOpenAICodexResponses)
	}
	if ProviderIDOpenAICodex != "openai-codex" {
		t.Errorf("ProviderIDOpenAICodex: expected 'openai-codex', got %q", ProviderIDOpenAICodex)
	}

	// 9. Usage and Cost JSON keys
	usage := Usage{
		Input:       10,
		Output:      20,
		CacheRead:   30,
		CacheWrite:  40,
		TotalTokens: 50,
		Cost: UsageCost{
			Input:      0.1,
			Output:     0.2,
			CacheRead:  0.3,
			CacheWrite: 0.4,
			Total:      1.0,
		},
	}

	data, err := json.Marshal(usage)
	if err != nil {
		t.Fatalf("failed to marshal Usage: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal Usage JSON: %v", err)
	}

	expectedUsageKeys := []string{"input", "output", "cacheRead", "cacheWrite", "totalTokens", "cost"}
	for _, key := range expectedUsageKeys {
		if _, ok := raw[key]; !ok {
			t.Errorf("expected Usage JSON to have key %q", key)
		}
	}

	costMap, ok := raw["cost"].(map[string]any)
	if !ok {
		t.Fatalf("expected cost field to be a map[string]any, got %T", raw["cost"])
	}

	expectedCostKeys := []string{"input", "output", "cacheRead", "cacheWrite", "total"}
	for _, key := range expectedCostKeys {
		if _, ok := costMap[key]; !ok {
			t.Errorf("expected UsageCost JSON to have key %q", key)
		}
	}
}

func TestUserMessageEmptyContentArrayRoundTrip(t *testing.T) {
	jsonData := `{"role":"user","content":[],"timestamp":12345}`

	var msg UserMessage
	if err := json.Unmarshal([]byte(jsonData), &msg); err != nil {
		t.Fatalf("failed to unmarshal empty content array: %v", err)
	}

	blocks, ok := msg.Content.([]UserContent)
	if !ok {
		t.Fatalf("expected []UserContent, got %T", msg.Content)
	}
	if blocks == nil {
		t.Fatalf("expected non-nil empty []UserContent")
	}
	if len(blocks) != 0 {
		t.Fatalf("expected empty []UserContent, got %d blocks", len(blocks))
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal empty content array: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("failed to unmarshal marshaled message: %v", err)
	}
	content, ok := raw["content"].([]any)
	if !ok {
		t.Fatalf("expected JSON content array, got %T (%v)", raw["content"], raw["content"])
	}
	if len(content) != 0 {
		t.Fatalf("expected empty JSON content array, got %d items", len(content))
	}

	var roundTrip UserMessage
	if err := json.Unmarshal(data, &roundTrip); err != nil {
		t.Fatalf("failed to round-trip empty content array: %v", err)
	}
}

func TestUnmarshalRequiredFields(t *testing.T) {
	t.Run("UserMessage missing content", func(t *testing.T) {
		jsonData := `{
			"role": "user",
			"timestamp": 12345
		}`
		var msg UserMessage
		err := json.Unmarshal([]byte(jsonData), &msg)
		if err == nil {
			t.Errorf("expected unmarshal to fail for missing content in UserMessage")
		} else if !strings.Contains(err.Error(), "content") {
			t.Errorf("expected error message to contain 'content', got: %v", err)
		}
	})

	t.Run("ToolResultMessage missing toolCallId", func(t *testing.T) {
		jsonData := `{
			"role": "toolResult",
			"toolName": "search",
			"content": [{"type": "text", "text": "result"}],
			"timestamp": 12345
		}`
		var msg ToolResultMessage
		err := json.Unmarshal([]byte(jsonData), &msg)
		if err == nil {
			t.Errorf("expected unmarshal to fail for missing toolCallId in ToolResultMessage")
		} else if !strings.Contains(err.Error(), "toolCallId") {
			t.Errorf("expected error message to contain 'toolCallId', got: %v", err)
		}
	})

	t.Run("ToolResultMessage missing toolName", func(t *testing.T) {
		jsonData := `{
			"role": "toolResult",
			"toolCallId": "call-1",
			"content": [{"type": "text", "text": "result"}],
			"timestamp": 12345
		}`
		var msg ToolResultMessage
		err := json.Unmarshal([]byte(jsonData), &msg)
		if err == nil {
			t.Errorf("expected unmarshal to fail for missing toolName in ToolResultMessage")
		} else if !strings.Contains(err.Error(), "toolName") {
			t.Errorf("expected error message to contain 'toolName', got: %v", err)
		}
	})
}

func TestAssistantMessageEventJSON(t *testing.T) {
	t.Run("EventTextDelta with ContentIndex 0", func(t *testing.T) {
		index := 0
		event := AssistantMessageEvent{
			Type:         EventTextDelta,
			ContentIndex: &index,
			Delta:        "hello",
			Partial: &AssistantMessage{
				Content: []AssistantContent{
					TextContent{Text: "hello"},
				},
				Timestamp: 1700000000000,
			},
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if raw["type"] != "text_delta" {
			t.Errorf("expected type 'text_delta', got %v", raw["type"])
		}
		if raw["delta"] != "hello" {
			t.Errorf("expected delta 'hello', got %v", raw["delta"])
		}
		if raw["contentIndex"] != float64(0) {
			t.Errorf("expected contentIndex to be 0, got %v", raw["contentIndex"])
		}

		var roundTrip AssistantMessageEvent
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal round-trip: %v", err)
		}
		if roundTrip.ContentIndex == nil || *roundTrip.ContentIndex != 0 {
			t.Errorf("expected ContentIndex 0, got %v", roundTrip.ContentIndex)
		}
	})

	t.Run("EventStart omitting ContentIndex", func(t *testing.T) {
		event := AssistantMessageEvent{
			Type: EventStart,
			Partial: &AssistantMessage{
				Timestamp: 1700000000000,
			},
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if _, exists := raw["contentIndex"]; exists {
			t.Errorf("expected contentIndex to be omitted for start event")
		}

		var roundTrip AssistantMessageEvent
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal round-trip: %v", err)
		}
		if roundTrip.ContentIndex != nil {
			t.Errorf("expected ContentIndex to be nil, got %v", roundTrip.ContentIndex)
		}
		if roundTrip.Partial == nil {
			t.Fatalf("expected round-tripped Partial")
		}
	})

	t.Run("EventDone omitting ContentIndex", func(t *testing.T) {
		event := AssistantMessageEvent{
			Type: EventDone,
			Message: &AssistantMessage{
				Content: []AssistantContent{
					TextContent{Text: "final response"},
				},
				StopReason: StopReasonStop,
				Timestamp:  1700000000000,
			},
			Reason: StopReasonStop,
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if _, exists := raw["contentIndex"]; exists {
			t.Errorf("expected contentIndex to be omitted for done event")
		}

		var roundTrip AssistantMessageEvent
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal round-trip: %v", err)
		}
		if roundTrip.ContentIndex != nil {
			t.Errorf("expected ContentIndex to be nil, got %v", roundTrip.ContentIndex)
		}
		if roundTrip.Message == nil || len(roundTrip.Message.Content) != 1 {
			t.Fatalf("expected round-tripped Message")
		}
	})

	t.Run("EventError with Error and StopReason", func(t *testing.T) {
		event := AssistantMessageEvent{
			Type: EventError,
			Error: &AssistantMessage{
				ErrorMessage: "some timeout",
				StopReason:   StopReasonError,
				Timestamp:    1700000000000,
			},
			Reason: StopReasonError,
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}

		if raw["reason"] != "error" {
			t.Errorf("expected reason 'error', got %v", raw["reason"])
		}

		var roundTrip AssistantMessageEvent
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal round-trip: %v", err)
		}
		if roundTrip.Error == nil || roundTrip.Error.ErrorMessage != "some timeout" {
			t.Errorf("expected Error payload")
		}
		if roundTrip.Reason != StopReasonError {
			t.Errorf("expected Reason StopReasonError")
		}
	})

	t.Run("EventToolCallEnd with ToolCall", func(t *testing.T) {
		index := 1
		event := AssistantMessageEvent{
			Type:         EventToolCallEnd,
			ContentIndex: &index,
			ToolCall: &ToolCall{
				ID:        "call-abc",
				Name:      "web_search",
				Arguments: map[string]any{"query": "antigravity"},
			},
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}

		var roundTrip AssistantMessageEvent
		if err := json.Unmarshal(data, &roundTrip); err != nil {
			t.Fatalf("failed to unmarshal round-trip: %v", err)
		}
		if roundTrip.ContentIndex == nil || *roundTrip.ContentIndex != 1 {
			t.Errorf("expected ContentIndex 1, got %v", roundTrip.ContentIndex)
		}
		if roundTrip.ToolCall == nil || roundTrip.ToolCall.ID != "call-abc" || roundTrip.ToolCall.Name != "web_search" {
			t.Errorf("expected ToolCall detail, got %+v", roundTrip.ToolCall)
		}
	})
}

func TestAssistantStreamBasic(t *testing.T) {
	s := NewAssistantStream(10)
	events := s.Events() // Start the drain loop before pushing events

	// Test basic push and consume
	var wg sync.WaitGroup
	wg.Add(1)

	var received []AssistantMessageEvent
	go func() {
		defer wg.Done()
		for ev := range events {
			received = append(received, ev)
		}
	}()
	err := s.Push(AssistantMessageEvent{Type: EventStart})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	err = s.Push(AssistantMessageEvent{Type: EventTextStart})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	err = s.Push(AssistantMessageEvent{
		Type:  EventTextDelta,
		Delta: "hello",
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	finalMsg := AssistantMessage{
		Model: "some-model",
	}
	err = s.Push(AssistantMessageEvent{
		Type:    EventDone,
		Message: &finalMsg,
		Reason:  StopReasonStop,
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	wg.Wait()

	if len(received) != 4 {
		t.Errorf("expected 4 events, got %d", len(received))
	}

	msg, err := s.Result()
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if msg.Model != "some-model" {
		t.Errorf("expected model 'some-model', got %q", msg.Model)
	}
}

func TestAssistantStreamError(t *testing.T) {
	s := NewAssistantStream(10)
	events := s.Events() // Start the drain loop before pushing events

	err := s.Push(AssistantMessageEvent{Type: EventStart})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	partialMsg := AssistantMessage{
		ErrorMessage: "something went wrong",
	}
	err = s.Push(AssistantMessageEvent{
		Type:   EventError,
		Error:  &partialMsg,
		Reason: StopReasonError,
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Consume to make sure we don't block
	for range events {
	}

	msg, err := s.Result()
	if err == nil {
		t.Fatalf("expected non-nil error, got nil")
	}
	if err.Error() != "something went wrong" {
		t.Errorf("expected error 'something went wrong', got %q", err.Error())
	}
	if msg.ErrorMessage != "something went wrong" {
		t.Errorf("expected message ErrorMessage to be set")
	}
}

func TestAssistantStreamNoOpAfterDone(t *testing.T) {
	s := NewAssistantStream(10)

	err := s.Push(AssistantMessageEvent{
		Type:   EventDone,
		Reason: StopReasonStop,
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Any subsequent push should be a no-op
	err = s.Push(AssistantMessageEvent{
		Type:  EventTextDelta,
		Delta: "should be ignored",
	})
	if err != nil {
		t.Errorf("push after done should be a no-op and return nil, got %v", err)
	}

	// Drain the stream
	var events []AssistantMessageEvent
	for ev := range s.Events() {
		events = append(events, ev)
	}

	if len(events) != 1 {
		t.Errorf("expected only 1 event, got %d", len(events))
	}
}

func TestAssistantStreamConcurrentResult(t *testing.T) {
	s := NewAssistantStream(10)

	var wg sync.WaitGroup
	const numWaiters = 5

	wg.Add(numWaiters)
	for i := 0; i < numWaiters; i++ {
		go func() {
			defer wg.Done()
			msg, err := s.Result()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if msg.ResponseModel != "model-x" {
				t.Errorf("unexpected response model: %s", msg.ResponseModel)
			}
		}()
	}

	s.End(&AssistantMessage{ResponseModel: "model-x"})

	wg.Wait()
}

func TestAssistantStreamQueueOverflow(t *testing.T) {
	// Create stream with a small queue limit
	s := NewAssistantStream(2)
	_ = s.Events() // Trigger lazy-start of the drain loop
	// Since we are not reading from Events(), the drain loop is blocked trying to send the first event.
	// The first event is popped from the queue and sent to eventsChan.
	// The channel is unbuffered, so sending blocks.
	// Then we push more events. They remain in the queue because the drain loop is blocked.
	err := s.Push(AssistantMessageEvent{Type: EventStart}) // Popped and blocked on send
	if err != nil {
		t.Fatalf("first push failed: %v", err)
	}

	// Wait until the drain loop has popped the first event and is blocked on sending.
	// We check the queue size under lock in a loop.
	drained := false
	for i := 0; i < 100; i++ {
		s.mu.Lock()
		qLen := len(s.queue)
		s.mu.Unlock()
		if qLen == 0 {
			drained = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !drained {
		t.Fatalf("timed out waiting for drain loop to pop first event")
	}

	// Now we fill the queue to its limit (2)
	err = s.Push(AssistantMessageEvent{Type: EventTextStart}) // Enqueued (queue size = 1)
	if err != nil {
		t.Fatalf("second push failed: %v", err)
	}

	err = s.Push(AssistantMessageEvent{Type: EventTextDelta}) // Enqueued (queue size = 2)
	if err != nil {
		t.Fatalf("third push failed: %v", err)
	}

	// The next push should exceed the queue limit and return a queue overflow error
	err = s.Push(AssistantMessageEvent{Type: EventTextDelta})
	if err == nil {
		t.Errorf("expected queue overflow error, got nil")
	} else if !strings.Contains(err.Error(), "queue overflow") {
		t.Errorf("expected queue overflow error, got: %v", err)
	}

	// Drain the queue to prevent goroutine leak
	go func() {
		for range s.Events() {
		}
	}()

	s.End(nil)
}

func TestAssistantStreamEndAndErrorDirect(t *testing.T) {
	t.Run("End", func(t *testing.T) {
		s := NewAssistantStream(10)
		s.End(&AssistantMessage{ResponseID: "resp-123"})

		msg, err := s.Result()
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if msg.ResponseID != "resp-123" {
			t.Errorf("expected ResponseID 'resp-123', got %q", msg.ResponseID)
		}
	})

	t.Run("Error", func(t *testing.T) {
		s := NewAssistantStream(10)
		customErr := errors.New("custom test error")
		s.Error(customErr, &AssistantMessage{ResponseID: "resp-err"})

		msg, err := s.Result()
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if !errors.Is(err, customErr) {
			t.Errorf("expected customErr, got %v", err)
		}
		if msg.ResponseID != "resp-err" {
			t.Errorf("expected ResponseID 'resp-err', got %q", msg.ResponseID)
		}
	})
}

func TestAssistantStreamStopReasonPartitioning(t *testing.T) {
	t.Run("Valid Done Reasons", func(t *testing.T) {
		reasons := []StopReason{StopReasonStop, StopReasonLength, StopReasonToolUse}
		for _, reason := range reasons {
			s := NewAssistantStream(10)
			err := s.Push(AssistantMessageEvent{
				Type:   EventDone,
				Reason: reason,
			})
			if err != nil {
				t.Errorf("expected valid done reason %q to be accepted, got %v", reason, err)
			}
		}
	})

	t.Run("Invalid Done Reasons", func(t *testing.T) {
		reasons := []StopReason{StopReasonError, StopReasonAborted, StopReason("invalid")}
		for _, reason := range reasons {
			s := NewAssistantStream(10)
			err := s.Push(AssistantMessageEvent{
				Type:   EventDone,
				Reason: reason,
			})
			if err == nil {
				t.Errorf("expected invalid done reason %q to be rejected, got nil", reason)
			}
			s.mu.Lock()
			qLen := len(s.queue)
			closed := s.pushClosed
			s.mu.Unlock()
			if qLen != 0 {
				t.Errorf("expected queue to be empty after rejected push, got length %d", qLen)
			}
			if closed {
				t.Errorf("expected pushClosed to remain false after rejected push")
			}
			msgDone := AssistantMessage{ResponseModel: "valid-done"}
			err = s.Push(AssistantMessageEvent{
				Type:    EventDone,
				Reason:  StopReasonStop,
				Message: &msgDone,
			})
			if err != nil {
				t.Errorf("expected stream to still accept valid done reason after rejection, got err: %v", err)
			}
			res, err := s.Result()
			if err != nil {
				t.Errorf("expected no error from Result(), got %v", err)
			}
			if res.ResponseModel != "valid-done" {
				t.Errorf("expected ResponseModel to be 'valid-done', got %q", res.ResponseModel)
			}
		}
	})

	t.Run("Valid Error Reasons", func(t *testing.T) {
		reasons := []StopReason{StopReasonError, StopReasonAborted}
		for _, reason := range reasons {
			s := NewAssistantStream(10)
			err := s.Push(AssistantMessageEvent{
				Type:   EventError,
				Reason: reason,
			})
			if err != nil {
				t.Errorf("expected valid error reason %q to be accepted, got %v", reason, err)
			}
		}
	})

	t.Run("Invalid Error Reasons", func(t *testing.T) {
		reasons := []StopReason{StopReasonStop, StopReasonLength, StopReasonToolUse, StopReason("invalid")}
		for _, reason := range reasons {
			s := NewAssistantStream(10)
			err := s.Push(AssistantMessageEvent{
				Type:   EventError,
				Reason: reason,
			})
			if err == nil {
				t.Errorf("expected invalid error reason %q to be rejected, got nil", reason)
			}
			s.mu.Lock()
			qLen := len(s.queue)
			closed := s.pushClosed
			s.mu.Unlock()
			if qLen != 0 {
				t.Errorf("expected queue to be empty after rejected push, got length %d", qLen)
			}
			if closed {
				t.Errorf("expected pushClosed to remain false after rejected push")
			}
			msgErr := AssistantMessage{ResponseModel: "valid-error", ErrorMessage: "some-error"}
			err = s.Push(AssistantMessageEvent{
				Type:   EventError,
				Reason: StopReasonError,
				Error:  &msgErr,
			})
			if err != nil {
				t.Errorf("expected stream to still accept valid error reason after rejection, got err: %v", err)
			}
			res, err := s.Result()
			if err == nil {
				t.Errorf("expected error from Result(), got nil")
			}
			if res.ResponseModel != "valid-error" {
				t.Errorf("expected ResponseModel to be 'valid-error', got %q", res.ResponseModel)
			}
		}
	})
}

func TestAssistantStreamSnapshotIsolation(t *testing.T) {
	s := NewAssistantStream(10)
	events := s.Events() // Trigger eventsWatched = true
	// Prepare a message and a tool call with mutable slice and map
	args := map[string]any{"key": "value"}
	tc := &ToolCall{
		ID:        "call-1",
		Name:      "test",
		Arguments: args,
	}
	content := []AssistantContent{tc}
	msg := &AssistantMessage{
		Content: content,
	}

	// Push the event
	err := s.Push(AssistantMessageEvent{
		Type:     EventToolCallEnd,
		ToolCall: tc,
		Partial:  msg,
	})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Mutate the original map and slice immediately after push
	args["key"] = "mutated"
	tc.ID = "mutated-id"
	content[0] = TextContent{Text: "mutated"}
	msg.Model = "mutated-model"

	// Now consume the event and check it was isolated
	go func() {
		s.End(nil)
	}()

	var ev AssistantMessageEvent
	for e := range events {
		if e.Type == EventToolCallEnd {
			ev = e
		}
	}

	// Assert the consumed event has the original unmutated values
	if ev.ToolCall == nil {
		t.Fatalf("expected tool call to be present")
	}
	if ev.ToolCall.ID != "call-1" {
		t.Errorf("ToolCall ID was mutated: got %q, expected 'call-1'", ev.ToolCall.ID)
	}
	val, _ := ev.ToolCall.Arguments["key"].(string)
	if val != "value" {
		t.Errorf("ToolCall Arguments was mutated: got %q, expected 'value'", val)
	}

	if ev.Partial == nil {
		t.Fatalf("expected partial message to be present")
	}
	if len(ev.Partial.Content) != 1 {
		t.Fatalf("expected partial content length 1")
	}
	tcFromMsg, ok := ev.Partial.Content[0].(ToolCall)
	if !ok {
		t.Fatalf("expected content to be ToolCall, got %T", ev.Partial.Content[0])
	}
	if tcFromMsg.ID != "call-1" {
		t.Errorf("message content ToolCall ID was mutated: got %q", tcFromMsg.ID)
	}
	if ev.Partial.Model != "" {
		t.Errorf("message Model was mutated: got %q", ev.Partial.Model)
	}
}

func TestAssistantStreamResultOnly(t *testing.T) {
	s := NewAssistantStream(10)

	// Simulate provider pushing some text deltas and then done without Events() being called
	err := s.Push(AssistantMessageEvent{Type: EventStart})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	err = s.Push(AssistantMessageEvent{Type: EventTextStart})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}
	err = s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "hello"})
	if err != nil {
		t.Fatalf("push failed: %v", err)
	}

	doneMsg := &AssistantMessage{
		ResponseID: "done-123",
	}
	s.End(doneMsg)

	// Verify Result resolves without hanging
	res, err := s.Result()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.ResponseID != "done-123" {
		t.Errorf("expected ResponseID 'done-123', got %q", res.ResponseID)
	}
}

func TestAssistantStreamEndErrorValidation(t *testing.T) {
	t.Run("End normalizes invalid stop reason", func(t *testing.T) {
		s := NewAssistantStream(10)
		s.End(&AssistantMessage{
			StopReason: StopReasonError, // Invalid for successful done
		})

		msg, err := s.Result()
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if msg.StopReason != StopReasonStop {
			t.Errorf("expected StopReason to be normalized to 'stop', got %q", msg.StopReason)
		}
	})

	t.Run("Error normalizes invalid stop reason", func(t *testing.T) {
		s := NewAssistantStream(10)
		s.Error(errors.New("fail"), &AssistantMessage{
			StopReason: StopReasonStop, // Invalid for error
		})

		msg, err := s.Result()
		if err == nil {
			t.Fatalf("expected non-nil error")
		}
		if msg.StopReason != StopReasonError {
			t.Errorf("expected StopReason to be normalized to 'error', got %q", msg.StopReason)
		}
	})
}

func TestAssistantStreamUnwatchedQueueOverflow(t *testing.T) {
	s := NewAssistantStream(2)

	if err := s.Push(AssistantMessageEvent{Type: EventStart}); err != nil {
		t.Fatalf("first push failed: %v", err)
	}
	if err := s.Push(AssistantMessageEvent{Type: EventTextStart}); err != nil {
		t.Fatalf("second push failed: %v", err)
	}

	err := s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "overflow"})
	if err == nil {
		t.Fatalf("expected queue overflow error, got nil")
	}
	if !strings.Contains(err.Error(), "queue overflow") {
		t.Fatalf("expected queue overflow error, got %v", err)
	}

	s.End(&AssistantMessage{ResponseID: "overflow-resp"})

	res, err := s.Result()
	if err != nil {
		t.Fatalf("expected nil result error, got %v", err)
	}
	if res.ResponseID != "overflow-resp" {
		t.Errorf("expected ResponseID 'overflow-resp', got %q", res.ResponseID)
	}
}

func TestAssistantStreamTerminalPushWhenQueueFull(t *testing.T) {
	t.Run("EventDone", func(t *testing.T) {
		s := NewAssistantStream(2)

		if err := s.Push(AssistantMessageEvent{Type: EventStart}); err != nil {
			t.Fatalf("first push failed: %v", err)
		}
		if err := s.Push(AssistantMessageEvent{Type: EventTextStart}); err != nil {
			t.Fatalf("second push failed: %v", err)
		}

		done := AssistantMessage{ResponseID: "done-when-full"}
		if err := s.Push(AssistantMessageEvent{
			Type:    EventDone,
			Message: &done,
			Reason:  StopReasonStop,
		}); err != nil {
			t.Fatalf("expected terminal push to succeed when queue is full, got %v", err)
		}

		msg, err := s.Result()
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if msg.ResponseID != "done-when-full" {
			t.Fatalf("expected ResponseID %q, got %q", "done-when-full", msg.ResponseID)
		}

		var events []AssistantMessageEvent
		for ev := range s.Events() {
			events = append(events, ev)
		}
		if len(events) != 3 {
			t.Fatalf("expected 3 events, got %d", len(events))
		}
		if events[2].Type != EventDone {
			t.Fatalf("expected final event type %q, got %q", EventDone, events[2].Type)
		}
		if events[2].Reason != StopReasonStop {
			t.Errorf("expected final event reason %q, got %q", StopReasonStop, events[2].Reason)
		}
		if events[2].Message == nil {
			t.Errorf("expected final event Message to be non-nil")
		} else if events[2].Message.ResponseID != "done-when-full" {
			t.Errorf("expected final event Message ResponseID %q, got %q", "done-when-full", events[2].Message.ResponseID)
		}
	})

	t.Run("EventError", func(t *testing.T) {
		s := NewAssistantStream(2)

		if err := s.Push(AssistantMessageEvent{Type: EventStart}); err != nil {
			t.Fatalf("first push failed: %v", err)
		}
		if err := s.Push(AssistantMessageEvent{Type: EventTextStart}); err != nil {
			t.Fatalf("second push failed: %v", err)
		}

		errPayload := AssistantMessage{
			ResponseID:   "err-when-full",
			ErrorMessage: "custom queue error",
		}
		if err := s.Push(AssistantMessageEvent{
			Type:   EventError,
			Error:  &errPayload,
			Reason: StopReasonError,
		}); err != nil {
			t.Fatalf("expected terminal error push to succeed when queue is full, got %v", err)
		}

		msg, err := s.Result()
		if err == nil {
			t.Fatalf("expected error, got nil")
		}
		if err.Error() != "custom queue error" {
			t.Fatalf("expected error message 'custom queue error', got %q", err.Error())
		}
		if msg.ResponseID != "err-when-full" {
			t.Fatalf("expected ResponseID %q, got %q", "err-when-full", msg.ResponseID)
		}

		var events []AssistantMessageEvent
		for ev := range s.Events() {
			events = append(events, ev)
		}
		if len(events) != 3 {
			t.Fatalf("expected 3 events, got %d", len(events))
		}
		if events[2].Type != EventError {
			t.Fatalf("expected final event type %q, got %q", EventError, events[2].Type)
		}
		if events[2].Reason != StopReasonError {
			t.Errorf("expected final event reason %q, got %q", StopReasonError, events[2].Reason)
		}
		if events[2].Error == nil {
			t.Errorf("expected final event Error to be non-nil")
		} else if events[2].Error.ResponseID != "err-when-full" {
			t.Errorf("expected final event Error ResponseID %q, got %q", "err-when-full", events[2].Error.ResponseID)
		}
	})
}

func TestAssistantStreamEventsAfterResult(t *testing.T) {
	s := NewAssistantStream(10)

	if err := s.Push(AssistantMessageEvent{Type: EventStart}); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if err := s.Push(AssistantMessageEvent{Type: EventTextStart}); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if err := s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "a"}); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	if err := s.Push(AssistantMessageEvent{Type: EventTextDelta, Delta: "b"}); err != nil {
		t.Fatalf("push failed: %v", err)
	}
	s.End(&AssistantMessage{ResponseModel: "model-y"})

	res, err := s.Result()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.ResponseModel != "model-y" {
		t.Errorf("expected model-y, got %s", res.ResponseModel)
	}

	var events []AssistantMessageEvent
	for ev := range s.Events() {
		events = append(events, ev)
	}

	if len(events) != 5 {
		t.Fatalf("expected 5 events, got %d", len(events))
	}
	want := []AssistantMessageEventType{
		EventStart,
		EventTextStart,
		EventTextDelta,
		EventTextDelta,
		EventDone,
	}
	for i, typ := range want {
		if events[i].Type != typ {
			t.Errorf("event %d type = %q, want %q", i, events[i].Type, typ)
		}
	}
}

func TestRegistry(t *testing.T) {
	// Ensure a clean state before starting
	ClearApiProviders()
	defer ClearApiProviders()

	// 1. Validation of inputs
	err := RegisterApiProvider(ApiProvider{
		API: "",
	})
	if err == nil {
		t.Error("expected error when registering provider with empty API ID")
	}

	dummyStream := func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
		return NewAssistantStream(10)
	}
	dummyStreamSimple := func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
		return NewAssistantStream(10)
	}

	err = RegisterApiProvider(ApiProvider{
		API:          "test-api",
		Stream:       nil,
		StreamSimple: dummyStreamSimple,
	})
	if err == nil {
		t.Error("expected error when registering provider with nil Stream function")
	}

	err = RegisterApiProvider(ApiProvider{
		API:          "test-api",
		Stream:       dummyStream,
		StreamSimple: nil,
	})
	if err == nil {
		t.Error("expected error when registering provider with nil StreamSimple function")
	}

	// 2. Successful registration
	originalStreamCalled := false
	originalStreamSimpleCalled := false

	testAPI := APIID("test-api")
	provider := ApiProvider{
		API: testAPI,
		Stream: func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
			originalStreamCalled = true
			s := NewAssistantStream(10)
			s.End(&AssistantMessage{ResponseModel: "stream-success"})
			return s
		},
		StreamSimple: func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
			originalStreamSimpleCalled = true
			s := NewAssistantStream(10)
			s.End(&AssistantMessage{ResponseModel: "simple-success"})
			return s
		},
	}

	err = RegisterApiProvider(provider)
	if err != nil {
		t.Fatalf("failed to register valid provider: %v", err)
	}

	// Test duplicate registration
	err = RegisterApiProvider(provider)
	if err == nil {
		t.Error("expected error when registering duplicate API provider, got nil")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected duplicate error message to contain 'already registered', got %q", err.Error())
	}

	// 3. Retrieval via GetApiProvider
	retrieved, ok := GetApiProvider(testAPI)
	if !ok {
		t.Fatalf("expected to retrieve registered provider %q", testAPI)
	}
	if retrieved.API != testAPI {
		t.Errorf("expected retrieved provider API %q, got %q", testAPI, retrieved.API)
	}

	// Test unregistered API retrieval
	_, ok = GetApiProvider("non-existent-api")
	if ok {
		t.Error("expected ok=false for non-existent provider")
	}

	// 4. Verification of API-mismatch guard on retrieved.Stream
	// A. Match: should call original function
	modelMatch := Model{API: testAPI}
	streamMatch := retrieved.Stream(context.Background(), modelMatch, Context{}, &StreamOptions{})
	resMatch, err := streamMatch.Result()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resMatch.ResponseModel != "stream-success" {
		t.Errorf("expected response model 'stream-success', got %q", resMatch.ResponseModel)
	}
	if !originalStreamCalled {
		t.Error("expected original Stream function to be called")
	}

	// B. Mismatch: should return error stream (mismatch guard)
	originalStreamCalled = false
	modelMismatch := Model{API: "other-api"}
	streamMismatch := retrieved.Stream(context.Background(), modelMismatch, Context{}, &StreamOptions{})
	resMismatch, err := streamMismatch.Result()
	if err == nil {
		t.Error("expected mismatch error, got nil")
	} else if !strings.Contains(err.Error(), "API mismatch") {
		t.Errorf("expected mismatch error message to contain 'API mismatch', got %q", err.Error())
	}
	if resMismatch.StopReason != StopReasonError {
		t.Errorf("expected stop reason %q, got %q", StopReasonError, resMismatch.StopReason)
	}
	if originalStreamCalled {
		t.Error("expected original Stream function NOT to be called on API mismatch")
	}

	// Verify that EventError is received via the Events channel
	var mismatchEvents []AssistantMessageEvent
	for ev := range streamMismatch.Events() {
		mismatchEvents = append(mismatchEvents, ev)
	}
	if len(mismatchEvents) != 1 {
		t.Fatalf("expected 1 event on mismatch stream, got %d", len(mismatchEvents))
	}
	if mismatchEvents[0].Type != EventError {
		t.Errorf("expected EventError, got %s", mismatchEvents[0].Type)
	}

	// 5. Verification of API-mismatch guard on retrieved.StreamSimple
	// A. Match
	streamSimpleMatch := retrieved.StreamSimple(context.Background(), modelMatch, Context{}, &SimpleStreamOptions{})
	resSimpleMatch, err := streamSimpleMatch.Result()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resSimpleMatch.ResponseModel != "simple-success" {
		t.Errorf("expected response model 'simple-success', got %q", resSimpleMatch.ResponseModel)
	}
	if !originalStreamSimpleCalled {
		t.Error("expected original StreamSimple function to be called")
	}

	// B. Mismatch
	originalStreamSimpleCalled = false
	streamSimpleMismatch := retrieved.StreamSimple(context.Background(), modelMismatch, Context{}, &SimpleStreamOptions{})
	resSimpleMismatch, err := streamSimpleMismatch.Result()
	if err == nil {
		t.Error("expected mismatch error, got nil")
	} else if !strings.Contains(err.Error(), "API mismatch") {
		t.Errorf("expected mismatch error message to contain 'API mismatch', got %q", err.Error())
	}
	if resSimpleMismatch.StopReason != StopReasonError {
		t.Errorf("expected stop reason %q, got %q", StopReasonError, resSimpleMismatch.StopReason)
	}
	if originalStreamSimpleCalled {
		t.Error("expected original StreamSimple function NOT to be called on API mismatch")
	}

	// 6. Verification of sorting in GetApiProviders
	// Clear and register multiple in non-alphabetical order
	ClearApiProviders()
	apisToRegister := []string{"charlie", "alpha", "bravo"}
	for _, apiName := range apisToRegister {
		err := RegisterApiProvider(ApiProvider{
			API:          APIID(apiName),
			Stream:       dummyStream,
			StreamSimple: dummyStreamSimple,
		})
		if err != nil {
			t.Fatalf("failed to register provider %q: %v", apiName, err)
		}
	}

	allProviders := GetApiProviders()
	if len(allProviders) != 3 {
		t.Fatalf("expected 3 registered providers, got %d", len(allProviders))
	}

	expectedOrder := []string{"alpha", "bravo", "charlie"}
	for i, expected := range expectedOrder {
		if string(allProviders[i].API) != expected {
			t.Errorf("expected provider at index %d to be %q, got %q", i, expected, allProviders[i].API)
		}
	}

	// 7. Verification of ClearApiProviders
	ClearApiProviders()
	if len(GetApiProviders()) != 0 {
		t.Error("expected registry to be empty after ClearApiProviders")
	}
}

func TestDispatch(t *testing.T) {
	// Ensure a clean state before starting
	ClearApiProviders()
	defer ClearApiProviders()

	testAPI := APIID("test-api")
	provider := ApiProvider{
		API: testAPI,
		Stream: func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
			s := NewAssistantStream(10)
			s.End(&AssistantMessage{ResponseModel: "stream-success", StopReason: StopReasonStop})
			return s
		},
		StreamSimple: func(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
			s := NewAssistantStream(10)
			s.End(&AssistantMessage{ResponseModel: "simple-success", StopReason: StopReasonStop})
			return s
		},
	}

	if err := RegisterApiProvider(provider); err != nil {
		t.Fatalf("failed to register provider: %v", err)
	}

	modelMatch := Model{API: testAPI}

	t.Run("Registered Stream", func(t *testing.T) {
		s := Stream(context.Background(), modelMatch, Context{}, &StreamOptions{})
		msg, err := s.Result()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if msg.ResponseModel != "stream-success" {
			t.Errorf("expected ResponseModel stream-success, got %q", msg.ResponseModel)
		}
	})

	t.Run("Registered Complete", func(t *testing.T) {
		msg, err := Complete(context.Background(), modelMatch, Context{}, &StreamOptions{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if msg.ResponseModel != "stream-success" {
			t.Errorf("expected ResponseModel stream-success, got %q", msg.ResponseModel)
		}
	})

	t.Run("Registered StreamSimple", func(t *testing.T) {
		s := StreamSimple(context.Background(), modelMatch, Context{}, &SimpleStreamOptions{})
		msg, err := s.Result()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if msg.ResponseModel != "simple-success" {
			t.Errorf("expected ResponseModel simple-success, got %q", msg.ResponseModel)
		}
	})

	t.Run("Registered CompleteSimple", func(t *testing.T) {
		msg, err := CompleteSimple(context.Background(), modelMatch, Context{}, &SimpleStreamOptions{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if msg.ResponseModel != "simple-success" {
			t.Errorf("expected ResponseModel simple-success, got %q", msg.ResponseModel)
		}
	})

	modelMismatch := Model{API: "missing-api"}

	t.Run("Unregistered Stream", func(t *testing.T) {
		s := Stream(context.Background(), modelMismatch, Context{}, &StreamOptions{})
		var errorEventObserved bool
		for ev := range s.Events() {
			if ev.Type == EventError {
				errorEventObserved = true
				if ev.Reason != StopReasonError {
					t.Errorf("expected EventError to have reason %q, got %q", StopReasonError, ev.Reason)
				}
				if ev.Error == nil {
					t.Error("expected EventError to have a non-nil Error message object")
				} else if ev.Error.StopReason != StopReasonError {
					t.Errorf("expected message StopReasonError, got %q", ev.Error.StopReason)
				}
			}
		}
		if !errorEventObserved {
			t.Error("expected error event to be emitted on Events() channel")
		}
		msg, err := s.Result()
		if err == nil {
			t.Error("expected error for unregistered API, got nil")
		}
		if msg.StopReason != StopReasonError {
			t.Errorf("expected StopReasonError, got %q", msg.StopReason)
		}
	})

	t.Run("Unregistered Complete", func(t *testing.T) {
		msg, err := Complete(context.Background(), modelMismatch, Context{}, &StreamOptions{})
		if err == nil {
			t.Error("expected error for unregistered API, got nil")
		}
		if msg.StopReason != StopReasonError {
			t.Errorf("expected StopReasonError, got %q", msg.StopReason)
		}
	})

	t.Run("Unregistered StreamSimple", func(t *testing.T) {
		s := StreamSimple(context.Background(), modelMismatch, Context{}, &SimpleStreamOptions{})
		var errorEventObserved bool
		for ev := range s.Events() {
			if ev.Type == EventError {
				errorEventObserved = true
				if ev.Reason != StopReasonError {
					t.Errorf("expected EventError to have reason %q, got %q", StopReasonError, ev.Reason)
				}
				if ev.Error == nil {
					t.Error("expected EventError to have a non-nil Error message object")
				} else if ev.Error.StopReason != StopReasonError {
					t.Errorf("expected message StopReasonError, got %q", ev.Error.StopReason)
				}
			}
		}
		if !errorEventObserved {
			t.Error("expected error event to be emitted on Events() channel")
		}
		msg, err := s.Result()
		if err == nil {
			t.Error("expected error for unregistered API, got nil")
		}
		if msg.StopReason != StopReasonError {
			t.Errorf("expected StopReasonError, got %q", msg.StopReason)
		}
	})

	t.Run("Unregistered CompleteSimple", func(t *testing.T) {
		msg, err := CompleteSimple(context.Background(), modelMismatch, Context{}, &SimpleStreamOptions{})
		if err == nil {
			t.Error("expected error for unregistered API, got nil")
		}
		if msg.StopReason != StopReasonError {
			t.Errorf("expected StopReasonError, got %q", msg.StopReason)
		}
	})
}

func TestAssistantStreamFixes(t *testing.T) {
	t.Run("StopReason propagation in resolveResultLocked", func(t *testing.T) {
		s := NewAssistantStream(10)
		msgPayload := AssistantMessage{
			ResponseModel: "model-z",
			StopReason:    "", // empty
		}
		err := s.Push(AssistantMessageEvent{
			Type:    EventDone,
			Reason:  StopReasonStop,
			Message: &msgPayload,
		})
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		res, err := s.Result()
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if res.StopReason != StopReasonStop {
			t.Errorf("expected StopReason to be propagated as %q, got %q", StopReasonStop, res.StopReason)
		}
	})

	t.Run("StopReason propagation in resolveResultLocked for EventError", func(t *testing.T) {
		s := NewAssistantStream(10)
		msgPayload := AssistantMessage{
			ResponseModel: "model-err",
			StopReason:    "", // empty
		}
		err := s.Push(AssistantMessageEvent{
			Type:   EventError,
			Reason: StopReasonError,
			Error:  &msgPayload,
		})
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		res, err := s.Result()
		if err == nil {
			t.Fatalf("expected non-nil error, got nil")
		}
		if res.StopReason != StopReasonError {
			t.Errorf("expected StopReason to be propagated as %q, got %q", StopReasonError, res.StopReason)
		}
	})

	t.Run("ContentIndex deep copy isolation", func(t *testing.T) {
		s := NewAssistantStream(10)
		events := s.Events()

		indexVal := 42
		err := s.Push(AssistantMessageEvent{
			Type:         EventTextDelta,
			ContentIndex: &indexVal,
			Delta:        "test",
		})
		if err != nil {
			t.Fatalf("push failed: %v", err)
		}

		// Mutate indexVal immediately
		indexVal = 99

		s.End(nil)

		var ev AssistantMessageEvent
		for e := range events {
			if e.Type == EventTextDelta {
				ev = e
			}
		}

		if ev.ContentIndex == nil {
			t.Fatalf("expected ContentIndex to be non-nil")
		}
		if *ev.ContentIndex != 42 {
			t.Errorf("expected ContentIndex to remain 42, got %d", *ev.ContentIndex)
		}
	})
}
