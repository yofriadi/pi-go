package ai

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func makeFakeJWT(t *testing.T, claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("failed to marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signature := base64.RawURLEncoding.EncodeToString([]byte("signature"))
	return header + "." + payload + "." + signature
}

func TestExtractChatGPTAccountID_Success(t *testing.T) {
	// 1. Root-level claim
	claimsRoot := map[string]any{
		"chatgpt_account_id": "acct_root_123",
	}
	tokenRoot := makeFakeJWT(t, claimsRoot)
	id, err := ExtractChatGPTAccountID(tokenRoot)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "acct_root_123" {
		t.Errorf("expected acct_root_123, got %q", id)
	}

	// 2. Nested claim
	claimsNested := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_nested_456",
		},
	}
	tokenNested := makeFakeJWT(t, claimsNested)
	id, err = ExtractChatGPTAccountID(tokenNested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "acct_nested_456" {
		t.Errorf("expected acct_nested_456, got %q", id)
	}

	// 3. Root-level takes precedence
	claimsBoth := map[string]any{
		"chatgpt_account_id": "acct_root_both",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_nested_both",
		},
	}
	tokenBoth := makeFakeJWT(t, claimsBoth)
	id, err = ExtractChatGPTAccountID(tokenBoth)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "acct_root_both" {
		t.Errorf("expected acct_root_both, got %q", id)
	}
}

func TestExtractChatGPTAccountID_MalformedAndErrors(t *testing.T) {
	tests := []struct {
		name      string
		token     string
		expectErr string
	}{
		{"empty token", "", "empty token"},
		{"one segment", "abc", "invalid token segment count"},
		{"two segments", "abc.def", "invalid token segment count"},
		{"four segments", "abc.def.ghi.jkl", "invalid token segment count"},
		{"invalid base64", "abc.def_invalid@@.ghi", "failed to decode token payload"},
		{"invalid JSON", "abc.e2ludmFsaWQ=.signature", "failed to parse token JSON claims"},
		{"missing claim", makeFakeJWT(t, map[string]any{"other_claim": 123}), "chatgpt_account_id claim not found in token"},
		{"non-string root claim", makeFakeJWT(t, map[string]any{"chatgpt_account_id": 123}), "chatgpt_account_id claim is not a string"},
		{"non-string nested claim", makeFakeJWT(t, map[string]any{
			"https://api.openai.com/auth": map[string]any{
				"chatgpt_account_id": true,
			},
		}), "nested chatgpt_account_id claim is not a string"},
		{"nested auth is not object", makeFakeJWT(t, map[string]any{
			"https://api.openai.com/auth": "not-an-object",
		}), "auth claim is not an object"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ExtractChatGPTAccountID(tc.token)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.expectErr) {
				t.Errorf("expected error containing %q, got %q", tc.expectErr, err.Error())
			}

			// Verify error is secret-safe: it shouldn't contain the token content
			if tc.token != "" && strings.Contains(err.Error(), tc.token) {
				t.Errorf("error leaks token: %v", err)
			}
		})
	}
}

func TestResolveCodexUrl(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex", "https://chatgpt.com/backend-api/codex/responses"},
		{"https://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex/responses"},
		{"http://custom.local", "http://custom.local/codex/responses"},
	}

	for _, tc := range tests {
		res := ResolveCodexUrl(tc.input)
		if res != tc.expect {
			t.Errorf("ResolveCodexUrl(%q) = %q, expected %q", tc.input, res, tc.expect)
		}
	}
}

func TestResolveCodexWebSocketUrl(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"https://chatgpt.com/backend-api", "wss://chatgpt.com/backend-api/codex/responses"},
		{"http://custom.local", "ws://custom.local/codex/responses"},
	}

	for _, tc := range tests {
		res, err := ResolveCodexWebSocketUrl(tc.input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res != tc.expect {
			t.Errorf("ResolveCodexWebSocketUrl(%q) = %q, expected %q", tc.input, res, tc.expect)
		}
	}
}

func TestClampOpenAIPromptCacheKey(t *testing.T) {
	k1 := "short-key"
	if clampOpenAIPromptCacheKey(k1) != k1 {
		t.Errorf("should not alter short key")
	}

	k2 := strings.Repeat("a", 100)
	clamped := clampOpenAIPromptCacheKey(k2)
	if len(clamped) != 64 {
		t.Errorf("expected clamped key length 64, got %d", len(clamped))
	}

	k3 := strings.Repeat("🌟", 70)
	clampedRunes := []rune(clampOpenAIPromptCacheKey(k3))
	if len(clampedRunes) != 64 {
		t.Errorf("expected clamped key runes length 64, got %d", len(clampedRunes))
	}
}

func TestSanitizeSurrogates(t *testing.T) {
	input := "Hello \xf0\x9f\x98\x80 World! Unpaired: \xed\xa0\xbd"
	expected := "Hello \xf0\x9f\x98\x80 World! Unpaired: "
	res := sanitizeSurrogates(input)
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}
}

func TestShortHash(t *testing.T) {
	h1 := shortHash("test-string-1")
	h2 := shortHash("test-string-1")
	h3 := shortHash("test-string-2")

	if h1 != h2 {
		t.Error("shortHash must be deterministic")
	}
	if h1 == h3 {
		t.Error("shortHash should not collide for different values")
	}
	if h1 == "" {
		t.Error("shortHash must return non-empty string")
	}
}

func TestParseTextSignature(t *testing.T) {
	// Nil / Empty
	if parseTextSignature("") != nil {
		t.Error("expected nil for empty signature")
	}

	// Plain String
	p := parseTextSignature("legacy_id_123")
	if p == nil || p.ID != "legacy_id_123" || p.Phase != "" {
		t.Errorf("expected legacy_id_123 with no phase, got %+v", p)
	}

	// Valid JSON V1
	j1 := parseTextSignature(`{"v":1,"id":"msg_xyz_123"}`)
	if j1 == nil || j1.ID != "msg_xyz_123" || j1.Phase != "" {
		t.Errorf("failed to parse valid JSON v1: %+v", j1)
	}

	// Valid JSON V1 with commentary phase
	j2 := parseTextSignature(`{"v":1,"id":"msg_xyz_123","phase":"commentary"}`)
	if j2 == nil || j2.ID != "msg_xyz_123" || j2.Phase != "commentary" {
		t.Errorf("failed to parse JSON v1 with phase: %+v", j2)
	}

	// Valid JSON V1 with final_answer phase
	j3 := parseTextSignature(`{"v":1,"id":"msg_xyz_123","phase":"final_answer"}`)
	if j3 == nil || j3.ID != "msg_xyz_123" || j3.Phase != "final_answer" {
		t.Errorf("failed to parse JSON v1 with phase: %+v", j3)
	}

	// Invalid phase in JSON V1
	j4 := parseTextSignature(`{"v":1,"id":"msg_xyz_123","phase":"invalid-phase"}`)
	if j4 == nil || j4.ID != "msg_xyz_123" || j4.Phase != "" {
		t.Errorf("expected phase to be ignored for invalid phase value, got %+v", j4)
	}

	// Bad JSON
	j5 := parseTextSignature(`{"v":1,"id":`)
	if j5 == nil || j5.ID != `{"v":1,"id":` {
		t.Errorf("failed to fall back to plain text signature on bad JSON: %+v", j5)
	}
}

func TestNormalizeIdPart(t *testing.T) {
	tests := []struct {
		input  string
		expect string
	}{
		{"simple-id", "simple-id"},
		{"id_with_special@@chars", "id_with_special__chars"},
		{strings.Repeat("a", 100), strings.Repeat("a", 64)},
		{"trailing_underscores___", "trailing_underscores"},
	}

	for _, tc := range tests {
		res := normalizeIdPart(tc.input)
		if res != tc.expect {
			t.Errorf("normalizeIdPart(%q) = %q, expected %q", tc.input, res, tc.expect)
		}
	}
}

func TestBuildCodexRequestBody_DefaultsAndShape(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text", "image"},
	}

	context := Context{
		SystemPrompt: "Test System Prompt",
		Messages: []Message{
			UserMessage{
				Content: "Hello",
			},
		},
	}

	opts := &CodexResponsesOptions{
		ServiceTier: "flex",
	}

	body, err := buildCodexRequestBody(model, context, opts)
	if err != nil {
		t.Fatalf("failed to build request body: %v", err)
	}

	if body["model"] != "gpt-5.4" {
		t.Errorf("expected model gpt-5.4, got %v", body["model"])
	}
	if body["store"] != false {
		t.Errorf("expected store to be false")
	}
	if body["stream"] != true {
		t.Errorf("expected stream to be true")
	}
	if body["instructions"] != "Test System Prompt" {
		t.Errorf("expected instructions to be Test System Prompt, got %v", body["instructions"])
	}
	if body["service_tier"] != "flex" {
		t.Errorf("expected service_tier flex, got %v", body["service_tier"])
	}

	textOpts, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatal("expected text options block")
	}
	if textOpts["verbosity"] != "low" {
		t.Errorf("expected text verbosity low, got %v", textOpts["verbosity"])
	}

	input, ok := body["input"].([]map[string]any)
	if !ok {
		t.Fatal("expected input messages array")
	}
	if len(input) != 1 {
		t.Fatalf("expected 1 converted message, got %d", len(input))
	}
	if input[0]["role"] != "user" {
		t.Errorf("expected role user, got %v", input[0]["role"])
	}
}

func TestBuildCodexRequestBody_ToolSchemaConversion(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	context := Context{
		Tools: []ToolDefinition{
			{
				Name:        "get_weather",
				Description: "Gets weather info",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		},
	}

	body, err := buildCodexRequestBody(model, context, nil)
	if err != nil {
		t.Fatalf("failed to build request body: %v", err)
	}

	tools, ok := body["tools"].([]map[string]any)
	if !ok {
		t.Fatal("expected tools block")
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	tool := tools[0]
	if tool["type"] != "function" {
		t.Errorf("expected tool type function, got %v", tool["type"])
	}
	if tool["name"] != "get_weather" {
		t.Errorf("expected name get_weather, got %v", tool["name"])
	}
	if tool["description"] != "Gets weather info" {
		t.Errorf("expected description, got %v", tool["description"])
	}
	if tool["strict"] != nil {
		t.Errorf("expected strict to be nil")
	}
}

func TestBuildCodexRequestBody_ReasoningEffortMapping(t *testing.T) {
	mappedLow := "low"
	mappedHigh := "xhigh"
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelMinimal: &mappedLow,
			ModelThinkingLevelXHigh:   &mappedHigh,
		},
	}

	context := Context{}

	// Case 1: Mapped reasoning effort
	opts1 := &CodexResponsesOptions{
		ReasoningEffort: "xhigh",
	}
	body1, err := buildCodexRequestBody(model, context, opts1)
	if err != nil {
		t.Fatalf("failed to build body: %v", err)
	}
	reasoning1, ok := body1["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("expected reasoning block")
	}
	if reasoning1["effort"] != "xhigh" {
		t.Errorf("expected effort xhigh, got %v", reasoning1["effort"])
	}
	if reasoning1["summary"] != "auto" {
		t.Errorf("expected summary auto, got %v", reasoning1["summary"])
	}

	// Case 2: Custom reasoning summary
	opts2 := &CodexResponsesOptions{
		ReasoningEffort:  "minimal",
		ReasoningSummary: "none",
	}
	body2, err := buildCodexRequestBody(model, context, opts2)
	if err != nil {
		t.Fatalf("failed to build body: %v", err)
	}
	reasoning2, ok := body2["reasoning"].(map[string]any)
	if !ok {
		t.Fatal("expected reasoning block")
	}
	if reasoning2["effort"] != "low" {
		t.Errorf("expected effort low, got %v", reasoning2["effort"])
	}
	if reasoning2["summary"] != "none" {
		t.Errorf("expected summary none, got %v", reasoning2["summary"])
	}
}

func TestTransformMessages_ImageDowngrade(t *testing.T) {
	nonVisionModel := Model{
		ID:       "gpt-5.3-codex-spark",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"}, // No vision
	}

	visionModel := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text", "image"}, // Has vision
	}

	messages := []Message{
		UserMessage{
			Content: []UserContent{
				TextContent{Text: "Check this image:"},
				ImageContent{Data: "base64data", MimeType: "image/png"},
				ImageContent{Data: "base64data2", MimeType: "image/png"},
			},
		},
	}

	// Vision model preserves images
	resVision := transformMessages(messages, visionModel, nil)
	userMsgVision := resVision[0].(UserMessage)
	contentListVision := userMsgVision.Content.([]UserContent)
	if len(contentListVision) != 3 {
		t.Errorf("expected 3 content blocks, got %d", len(contentListVision))
	}
	if _, ok := contentListVision[1].(ImageContent); !ok {
		t.Error("expected ImageContent block to be preserved")
	}

	// Non-vision model downgrades images and merges consecutive ones
	resNonVision := transformMessages(messages, nonVisionModel, nil)
	userMsgNonVision := resNonVision[0].(UserMessage)
	contentListNonVision := userMsgNonVision.Content.([]UserContent)
	if len(contentListNonVision) != 2 {
		t.Errorf("expected consecutive images to be merged into a single placeholder, got %d blocks", len(contentListNonVision))
	}
	if textBlock, ok := contentListNonVision[1].(TextContent); ok {
		if textBlock.Text != "(image omitted: model does not support images)" {
			t.Errorf("expected placeholder text, got %q", textBlock.Text)
		}
	} else {
		t.Error("expected text block placeholder")
	}
}

func TestTransformMessages_OrphanedToolCallsSyntheticResults(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	messages := []Message{
		AssistantMessage{
			Content: []AssistantContent{
				ToolCall{
					ID:   "call_123|fc_456",
					Name: "get_weather",
				},
			},
		},
		UserMessage{
			Content: "Hello",
		},
	}

	res := transformMessages(messages, model, nil)

	// Since the tool call was not followed by a tool result, and was interrupted by a user message,
	// a synthetic tool result should have been inserted BEFORE the user message.
	if len(res) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(res))
	}

	if resultMsg, ok := res[1].(ToolResultMessage); ok {
		if resultMsg.ToolCallID != "call_123|fc_456" {
			t.Errorf("expected ToolCallID call_123|fc_456, got %s", resultMsg.ToolCallID)
		}
		if !resultMsg.IsError {
			t.Error("synthetic tool result must be marked as error")
		}
		if textBlock, ok := resultMsg.Content[0].(TextContent); ok {
			if textBlock.Text != "No result provided" {
				t.Errorf("expected 'No result provided', got %s", textBlock.Text)
			}
		} else {
			t.Error("expected TextContent block in synthetic tool result")
		}
	} else {
		t.Fatalf("expected ToolResultMessage at index 1, got %T", res[1])
	}
}

func TestTransformMessages_SignaturePreservationAndOmissions(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	messages := []Message{
		AssistantMessage{
			Provider: ProviderIDOpenAICodex,
			API:      APIIDOpenAICodexResponses,
			Model:    "gpt-5.4", // Same model
			Content: []AssistantContent{
				ThinkingContent{
					Thinking:          "Thinking process...",
					ThinkingSignature: `{"signature":"sig_123"}`,
				},
				TextContent{
					Text:          "Response text",
					TextSignature: `{"v":1,"id":"msg_123"}`,
				},
			},
		},
		AssistantMessage{
			Provider: ProviderIDOpenAICodex,
			API:      APIIDOpenAICodexResponses,
			Model:    "gpt-5.3-codex-spark", // Different model
			Content: []AssistantContent{
				ThinkingContent{
					Thinking:          "Cross model thinking...",
					ThinkingSignature: `{"signature":"sig_different"}`,
				},
				TextContent{
					Text:          "Cross model text",
					TextSignature: `{"v":1,"id":"msg_different"}`,
				},
				ThinkingContent{
					Thinking: "Redacted cross-model block",
					Redacted: true,
				},
			},
		},
	}

	res := transformMessages(messages, model, nil)

	// Message 1: Same model -> preserves signatures
	msg1 := res[0].(AssistantMessage)
	if thinkBlock, ok := msg1.Content[0].(ThinkingContent); ok {
		if thinkBlock.ThinkingSignature != `{"signature":"sig_123"}` {
			t.Errorf("expected thinking signature to be preserved, got %q", thinkBlock.ThinkingSignature)
		}
	} else {
		t.Error("expected ThinkingContent block")
	}

	if textBlock, ok := msg1.Content[1].(TextContent); ok {
		if textBlock.TextSignature != `{"v":1,"id":"msg_123"}` {
			t.Errorf("expected text signature to be preserved, got %q", textBlock.TextSignature)
		}
	} else {
		t.Error("expected TextContent block")
	}

	// Message 2: Different model -> drops/converts signatures
	msg2 := res[1].(AssistantMessage)

	// Since we converted thinking block signature to text, cross-model thinking converts to plain text block,
	// and redacted thinking blocks are dropped entirely.
	// Expected content blocks for msg2:
	// 1. TextContent: "Cross model thinking..." (converted from thinking)
	// 2. TextContent: "Cross model text" (text signature stripped)
	if len(msg2.Content) != 2 {
		t.Fatalf("expected 2 blocks for msg2, got %d", len(msg2.Content))
	}

	tb1 := msg2.Content[0].(TextContent)
	if tb1.Text != "Cross model thinking..." {
		t.Errorf("expected converted thinking text, got %q", tb1.Text)
	}
	if tb1.TextSignature != "" {
		t.Error("cross-model thinking signature must be stripped")
	}

	tb2 := msg2.Content[1].(TextContent)
	if tb2.Text != "Cross model text" {
		t.Errorf("expected text block, got %q", tb2.Text)
	}
	if tb2.TextSignature != "" {
		t.Error("cross-model text signature must be stripped")
	}
}

func TestConvertResponsesMessages_ToolResultsWithVision(t *testing.T) {
	visionModel := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text", "image"},
	}

	nonVisionModel := Model{
		ID:       "gpt-5.3-codex-spark",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	messages := []Message{
		ToolResultMessage{
			ToolCallID: "call_abc|fc_xyz",
			ToolName:   "view_image",
			Content: []ToolResultContent{
				TextContent{Text: "Image metadata here"},
				ImageContent{Data: "imagedata", MimeType: "image/png"},
			},
		},
	}

	// Case 1: Vision Model -> preserves images in tool result
	resVision, err := convertResponsesMessages(visionModel, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outVision := resVision[0]
	if outVision["type"] != "function_call_output" {
		t.Errorf("expected type function_call_output, got %v", outVision["type"])
	}
	if outVision["call_id"] != "call_abc" {
		t.Errorf("expected call_id call_abc, got %v", outVision["call_id"])
	}

	outputList, ok := outVision["output"].([]map[string]any)
	if !ok {
		t.Fatalf("expected output to be list of parts, got %T", outVision["output"])
	}
	if len(outputList) != 2 {
		t.Fatalf("expected 2 output parts, got %d", len(outputList))
	}

	p1 := outputList[0]
	if p1["type"] != "input_text" || p1["text"] != "Image metadata here" {
		t.Errorf("expected input_text part, got %+v", p1)
	}

	p2 := outputList[1]
	if p2["type"] != "input_image" || p2["image_url"] != "data:image/png;base64,imagedata" {
		t.Errorf("expected input_image part, got %+v", p2)
	}

	// Case 2: Non-Vision Model -> collapses to plain text
	resNonVision, err := convertResponsesMessages(nonVisionModel, messages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	outNonVision := resNonVision[0]
	strOutput, ok := outNonVision["output"].(string)
	if !ok {
		t.Fatalf("expected output to be string, got %T", outNonVision["output"])
	}
	if strOutput != "Image metadata here" {
		t.Errorf("expected string output 'Image metadata here', got %q", strOutput)
	}
}

func TestBuildCodexHeaders(t *testing.T) {
	// 1. With account ID and SSE
	headers1 := BuildCodexHeaders("my-token", "my-account", "my-ua", true)
	if headers1["Authorization"] != "Bearer my-token" {
		t.Errorf("expected Bearer authorization, got %q", headers1["Authorization"])
	}
	if headers1["ChatGPT-Account-ID"] != "my-account" {
		t.Errorf("expected account ID my-account, got %q", headers1["ChatGPT-Account-ID"])
	}
	if headers1["originator"] != "pi" {
		t.Errorf("expected originator pi, got %q", headers1["originator"])
	}
	if headers1["User-Agent"] != "my-ua" {
		t.Errorf("expected User-Agent my-ua, got %q", headers1["User-Agent"])
	}
	if headers1["OpenAI-Beta"] != "responses=experimental" {
		t.Errorf("expected OpenAI-Beta responses=experimental, got %q", headers1["OpenAI-Beta"])
	}
	if headers1["Accept"] != "text/event-stream" {
		t.Errorf("expected Accept text/event-stream, got %q", headers1["Accept"])
	}
	if headers1["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", headers1["Content-Type"])
	}
	// 2. Without account ID, non-SSE, default User-Agent
	headers2 := BuildCodexHeaders("my-token-2", "", "", false)
	if headers2["Authorization"] != "Bearer my-token-2" {
		t.Errorf("expected Bearer authorization, got %q", headers2["Authorization"])
	}
	if _, ok := headers2["ChatGPT-Account-ID"]; ok {
		t.Errorf("did not expect ChatGPT-Account-ID")
	}
	if headers2["User-Agent"] != "pi-go/0.1.0" {
		t.Errorf("expected default User-Agent, got %q", headers2["User-Agent"])
	}
	if _, ok := headers2["OpenAI-Beta"]; ok {
		t.Errorf("did not expect OpenAI-Beta")
	}
	if _, ok := headers2["Accept"]; ok {
		t.Errorf("did not expect Accept")
	}
	if headers2["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", headers2["Content-Type"])
	}
}

func TestTransformMessages_SameModelThinkingWithoutSignature(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	messages := []Message{
		AssistantMessage{
			Provider: ProviderIDOpenAICodex,
			API:      APIIDOpenAICodexResponses,
			Model:    "gpt-5.4",
			Content: []AssistantContent{
				ThinkingContent{
					Thinking:          "Thinking without signature...",
					ThinkingSignature: "",
				},
			},
		},
	}

	// First verify transformation keeps the block
	transformed := transformMessages(messages, model, nil)
	msg := transformed[0].(AssistantMessage)
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(msg.Content))
	}
	if tc, ok := msg.Content[0].(ThinkingContent); ok {
		if tc.Thinking != "Thinking without signature..." {
			t.Errorf("expected thinking content to match, got %q", tc.Thinking)
		}
	} else {
		t.Errorf("expected ThinkingContent, got %T", msg.Content[0])
	}

	// Now verify convertResponsesMessages serializes it to a message block to prevent lossy replay
	converted, err := convertResponsesMessages(model, transformed)
	if err != nil {
		t.Fatalf("failed to convert: %v", err)
	}
	if len(converted) != 1 {
		t.Fatalf("expected 1 converted item, got %d", len(converted))
	}
	item := converted[0]
	if item["type"] != "message" {
		t.Errorf("expected type message, got %v", item["type"])
	}
	if item["role"] != "assistant" {
		t.Errorf("expected role assistant, got %v", item["role"])
	}
	contentList := item["content"].([]map[string]any)
	if len(contentList) != 1 || contentList[0]["type"] != "output_text" || contentList[0]["text"] != "Thinking without signature..." {
		t.Errorf("expected output_text matching thinking, got %+v", contentList)
	}
}

func TestTransformMessages_GenericAnyPlaceholderOmission(t *testing.T) {
	nonVisionModel := Model{
		ID:       "gpt-5.3-codex-spark",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	messages := []Message{
		UserMessage{
			Content: []any{
				map[string]any{"type": "text", "text": "A text message"},
				map[string]any{"type": "image", "data": "base64data", "mimeType": "image/png"},
			},
		},
	}

	transformed := transformMessages(messages, nonVisionModel, nil)
	userMsg := transformed[0].(UserMessage)
	anyList, ok := userMsg.Content.([]any)
	if !ok {
		t.Fatalf("expected []any, got %T", userMsg.Content)
	}

	if len(anyList) != 2 {
		t.Fatalf("expected 2 items, got %d", len(anyList))
	}

	m2 := anyList[1].(map[string]any)
	if m2["type"] != "text" || m2["text"] != "(image omitted: model does not support images)" {
		t.Errorf("expected image block to be replaced by text placeholder in generic any map, got %+v", m2)
	}
}

func TestBuildCodexRequestBody_NegativeAssertions(t *testing.T) {
	model := Model{
		ID:       "gpt-5.4",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	context := Context{
		SystemPrompt: "System prompt",
		Messages: []Message{
			UserMessage{Content: "Hello"},
		},
	}

	body, err := buildCodexRequestBody(model, context, nil)
	if err != nil {
		t.Fatalf("failed to build: %v", err)
	}

	forbiddenFields := []string{
		"apiKey", "api_key", "token", "accessToken", "access_token",
		"auth", "credentials", "organization", "org", "user",
	}

	for _, f := range forbiddenFields {
		if _, ok := body[f]; ok {
			t.Errorf("forbidden field %q must not be serialized in request body", f)
		}
	}
}

func TestConvertResponsesMessages_ToolResultsNonVision(t *testing.T) {
	nonVisionModel := Model{
		ID:       "gpt-5.3-codex-spark",
		Provider: ProviderIDOpenAICodex,
		API:      APIIDOpenAICodexResponses,
		Input:    []InputKind{"text"},
	}

	// Case 1: Image only, no text -> converts to placeholder
	messages1 := []Message{
		ToolResultMessage{
			ToolCallID: "call_abc|fc_xyz",
			ToolName:   "view_image",
			Content: []ToolResultContent{
				ImageContent{Data: "imagedata", MimeType: "image/png"},
			},
		},
	}

	res1, err := convertResponsesMessages(nonVisionModel, messages1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out1 := res1[0]
	strOutput1, ok := out1["output"].(string)
	if !ok {
		t.Fatalf("expected output to be string, got %T", out1["output"])
	}
	if strOutput1 != "(image omitted: model does not support images)" {
		t.Errorf("expected placeholder for image-only tool output, got %q", strOutput1)
	}

	// Case 2: Text + Image -> collapses to text (since text is present)
	messages2 := []Message{
		ToolResultMessage{
			ToolCallID: "call_abc|fc_xyz",
			ToolName:   "view_image",
			Content: []ToolResultContent{
				TextContent{Text: "Image description"},
				ImageContent{Data: "imagedata", MimeType: "image/png"},
			},
		},
	}
	res2, err := convertResponsesMessages(nonVisionModel, messages2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out2 := res2[0]
	strOutput2, ok := out2["output"].(string)
	if !ok {
		t.Fatalf("expected output to be string, got %T", out2["output"])
	}
	expectedOutput2 := "Image description"
	if strOutput2 != expectedOutput2 {
		t.Errorf("expected text description, got %q", strOutput2)
	}
}
