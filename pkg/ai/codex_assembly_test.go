package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseStreamingJson(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
	}{
		{
			name:     "empty input",
			input:    "",
			expected: map[string]any{},
		},
		{
			name:     "complete json",
			input:    `{"a": 1, "b": "hello"}`,
			expected: map[string]any{"a": float64(1), "b": "hello"},
		},
		{
			name:     "partial json - unclosed brace",
			input:    `{"a": 1`,
			expected: map[string]any{"a": float64(1)},
		},
		{
			name:     "partial json - unclosed string",
			input:    `{"a": 1, "b": "hello`,
			expected: map[string]any{"a": float64(1), "b": "hello"},
		},
		{
			name:     "partial json - unclosed string with trailing backslash",
			input:    `{"a": 1, "b": "hello \`,
			expected: map[string]any{"a": float64(1), "b": "hello "},
		},
		{
			name:     "partial json - trailing colon",
			input:    `{"a": 1, "b":`,
			expected: map[string]any{"a": float64(1)},
		},
		{
			name:     "partial json - partial key",
			input:    `{"a": 1, "b`,
			expected: map[string]any{"a": float64(1)},
		},
		{
			name:     "partial json - unclosed nested brackets",
			input:    `{"a": [1, 2`,
			expected: map[string]any{"a": []any{float64(1), float64(2)}},
		},
		{
			name:     "partial json - trailing comma in array",
			input:    `{"a": [1, 2,`,
			expected: map[string]any{"a": []any{float64(1), float64(2)}},
		},
		{
			name:     "partial json - unclosed nested brace",
			input:    `{"a": {"b": 1`,
			expected: map[string]any{"a": map[string]any{"b": float64(1)}},
		},
		{
			name:     "partial json - trailing comma nested",
			input:    `{"a": {"b": 1,`,
			expected: map[string]any{"a": map[string]any{"b": float64(1)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStreamingJson(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("parseStreamingJson(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestStreamOpenAICodexResponses_Success(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Authorization header missing or mismatched: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("ChatGPT-Account-ID") != "acct_123" {
			t.Errorf("ChatGPT-Account-ID header missing or mismatched: %q", r.Header.Get("ChatGPT-Account-ID"))
		}
		if r.Header.Get("Originator") != "pi" {
			t.Errorf("Originator header missing or mismatched: %q", r.Header.Get("Originator"))
		}
		if r.Header.Get("OpenAI-Beta") != "responses=experimental" {
			t.Errorf("OpenAI-Beta header missing or mismatched: %q", r.Header.Get("OpenAI-Beta"))
		}

		// Verify body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		var reqBody map[string]any
		if err := json.Unmarshal(bodyBytes, &reqBody); err != nil {
			t.Errorf("failed to unmarshal request body: %v", err)
		}
		if reqBody["model"] != "gpt-5.3-codex-spark" {
			t.Errorf("unexpected model in request body: %v", reqBody["model"])
		}
		if reqBody["stream"] != true {
			t.Errorf("unexpected stream in request body: %v", reqBody["stream"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// Send events matching the full flow
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_999\"}}\n\n")
		// 1. Thinking block
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.added\", \"item\": {\"type\": \"reasoning\", \"id\": \"item_thinking\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.reasoning_text.delta\", \"delta\": \"Thinking delta\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.reasoning_summary_part.done\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.done\", \"item\": {\"type\": \"reasoning\", \"id\": \"item_thinking\", \"summary\": [{\"text\": \"Summary Thinking\"}]}}\n\n")
		// 2. Text block
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.added\", \"item\": {\"type\": \"message\", \"id\": \"item_message\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_text.delta\", \"delta\": \"Hello\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.done\", \"item\": {\"type\": \"message\", \"id\": \"item_message\", \"content\": [{\"type\": \"output_text\", \"text\": \"Hello, world!\"}], \"phase\": \"final_answer\"}}\n\n")
		// 3. Tool call block
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.added\", \"item\": {\"type\": \"function_call\", \"id\": \"item_fc\", \"call_id\": \"call_xyz\", \"name\": \"test_tool\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.function_call_arguments.delta\", \"delta\": \"{\\\"arg\\\"\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.function_call_arguments.done\", \"arguments\": \"{\\\"arg\\\": 42}\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.done\", \"item\": {\"type\": \"function_call\", \"id\": \"item_fc\", \"call_id\": \"call_xyz\", \"name\": \"test_tool\", \"arguments\": \"{\\\"arg\\\": 42}\"}}\n\n")
		// Done
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_999\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 100, \"output_tokens\": 50, \"total_tokens\": 150, \"input_tokens_details\": {\"cached_tokens\": 30}}}}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	// input cost: 1.75 / M, output cost: 14.0 / M, cacheRead cost: 0.175 / M
	model.Cost = ModelCost{
		Input:      1.75,
		Output:     14.0,
		CacheRead:  0.175,
		CacheWrite: 0.0,
	}

	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)

	var events []AssistantMessageEvent
	for ev := range stream.Events() {
		events = append(events, ev)
	}

	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected result error: %v", err)
	}

	// Verify events sequence
	expectedTypes := []AssistantMessageEventType{
		EventStart,
		EventThinkingStart,
		EventThinkingDelta,
		EventThinkingDelta, // summary part done
		EventThinkingEnd,
		EventTextStart,
		EventTextDelta,
		EventTextEnd,
		EventToolCallStart,
		EventToolCallDelta,
		EventToolCallDelta, // function_call_arguments.done delta
		EventToolCallEnd,
		EventDone,
	}

	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d. Types: %v", len(expectedTypes), len(events), eventTypesOf(events))
	}

	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("event[%d] type = %s, want %s", i, events[i].Type, et)
		}
	}

	// Verify final message content
	if len(msg.Content) != 3 {
		t.Fatalf("expected 3 content blocks, got %d", len(msg.Content))
	}

	thinking, ok := msg.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("expected Content[0] to be ThinkingContent, got %T", msg.Content[0])
	}
	if thinking.Thinking != "Summary Thinking" {
		t.Errorf("expected thinking to be 'Summary Thinking', got %q", thinking.Thinking)
	}
	if !strings.Contains(thinking.ThinkingSignature, "item_thinking") {
		t.Errorf("expected thinking signature to contain item details, got %q", thinking.ThinkingSignature)
	}

	text, ok := msg.Content[1].(TextContent)
	if !ok {
		t.Fatalf("expected Content[1] to be TextContent, got %T", msg.Content[1])
	}
	if text.Text != "Hello, world!" {
		t.Errorf("expected text to be 'Hello, world!', got %q", text.Text)
	}
	// Verify signature encoding: v=1, id="item_message", phase="final_answer"
	var sig struct {
		V     int    `json:"v"`
		ID    string `json:"id"`
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal([]byte(text.TextSignature), &sig); err != nil {
		t.Fatalf("failed to parse text signature: %v (sig: %s)", err, text.TextSignature)
	}
	if sig.V != 1 || sig.ID != "item_message" || sig.Phase != "final_answer" {
		t.Errorf("unexpected text signature: %+v", sig)
	}

	toolCall, ok := msg.Content[2].(ToolCall)
	if !ok {
		t.Fatalf("expected Content[2] to be ToolCall, got %T", msg.Content[2])
	}
	if toolCall.ID != "call_xyz|item_fc" || toolCall.Name != "test_tool" {
		t.Errorf("unexpected tool call details: %+v", toolCall)
	}
	if toolCall.Arguments["arg"] != float64(42) {
		t.Errorf("unexpected tool call arguments: %v", toolCall.Arguments)
	}

	// Verify stop reason: should be "toolUse" because there is a tool call in output content
	if msg.StopReason != StopReasonToolUse {
		t.Errorf("expected StopReason = %q, got %q", StopReasonToolUse, msg.StopReason)
	}

	// Verify usage: input = 100 - 30 = 70, output = 50, cacheRead = 30
	if msg.Usage.Input != 70 || msg.Usage.Output != 50 || msg.Usage.CacheRead != 30 {
		t.Errorf("unexpected usage counts: %+v", msg.Usage)
	}

	// Verify cost calculation
	// non-cached input: 70 * 1.75 / 1,000,000 = 0.0001225
	// output: 50 * 14.0 / 1,000,000 = 0.0007
	// cacheRead: 30 * 0.175 / 1,000,000 = 0.00000525
	// total = 0.00082775
	expectedCost := (70.0*1.75 + 50.0*14.0 + 30.0*0.175) / 1000000.0
	if mathAbs(msg.Usage.Cost.Total-expectedCost) > 1e-9 {
		t.Errorf("expected cost total %v, got %v", expectedCost, msg.Usage.Cost.Total)
	}
}

func TestStreamOpenAICodexResponses_PricingMultipliers(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	runPricingTest := func(t *testing.T, modelId string, serviceTier string, expectedMultiplier float64) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_1\", \"status\": \"completed\", \"service_tier\": \""+serviceTier+"\", \"usage\": {\"input_tokens\": 1000, \"output_tokens\": 500, \"total_tokens\": 1500}}}\n\n")
		}))
		defer srv.Close()

		model := Model{
			ID:       modelId,
			Provider: ProviderIDOpenAICodex,
			API:      APIIDOpenAICodexResponses,
			BaseURL:  srv.URL,
			Cost: ModelCost{
				Input:  10.0,
				Output: 20.0,
			},
		}

		opts := &CodexResponsesOptions{
			StreamOptions: StreamOptions{
				APIKey: token,
			},
			ServiceTier: serviceTier,
		}

		stream := StreamOpenAICodexResponses(context.Background(), model, Context{}, opts)
		msg, err := stream.Result()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// base input cost = 1000 * 10 / 1M = 0.01
		// base output cost = 500 * 20 / 1M = 0.01
		// base total = 0.02
		expectedCost := 0.02 * expectedMultiplier
		if mathAbs(msg.Usage.Cost.Total-expectedCost) > 1e-9 {
			t.Errorf("model=%s, tier=%s: expected total cost %v, got %v", modelId, serviceTier, expectedCost, msg.Usage.Cost.Total)
		}
	}

	t.Run("default (1x)", func(t *testing.T) {
		runPricingTest(t, "gpt-5.4", "", 1.0)
	})

	t.Run("flex (0.5x)", func(t *testing.T) {
		runPricingTest(t, "gpt-5.4", "flex", 0.5)
	})

	t.Run("priority for gpt-5.4 (2.0x)", func(t *testing.T) {
		runPricingTest(t, "gpt-5.4", "priority", 2.0)
	})

	t.Run("priority for gpt-5.5 (2.5x)", func(t *testing.T) {
		runPricingTest(t, "gpt-5.5", "priority", 2.5)
	})
}

func TestStreamOpenAICodexResponses_UsageLimitError(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	// Test HTTP 429 response
	srv429 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error": {"code": "usage_limit_reached", "plan_type": "free", "resets_at": 2000000000, "message": "Raw error message"}}`)
	}))
	defer srv429.Close()

	model := testModel(srv429.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	stream := StreamOpenAICodexResponses(context.Background(), model, Context{}, opts)
	_, err := stream.Result()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "You have hit your ChatGPT usage limit (free plan).") {
		t.Errorf("unexpected error message: %q", err.Error())
	}

	// Also verify that the stream's error msg does not leak any raw token
	// even if the error message string contains it.
	t.Run("token redaction", func(t *testing.T) {
		model := testModel("http://127.0.0.1:54321")
		opts := &CodexResponsesOptions{
			StreamOptions: StreamOptions{
				APIKey: token,
			},
		}
		opts.OnPayload = func(payload any, m Model) (any, bool, error) {
			return nil, false, fmt.Errorf("failed with token %s", token)
		}

		stream := StreamOpenAICodexResponses(context.Background(), model, Context{}, opts)
		_, err := stream.Result()
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if strings.Contains(err.Error(), token) {
			t.Errorf("token leaked in error: %q", err.Error())
		}
		if !strings.Contains(err.Error(), "[REDACTED]") {
			t.Errorf("expected [REDACTED] in error message, got %q", err.Error())
		}
	})
}

func TestStreamOpenAICodexResponses_CompleteParity(t *testing.T) {
	// Clean slate and register providers
	ClearApiProviders()
	defer ClearApiProviders()
	if err := RegisterBuiltinProviders(); err != nil {
		t.Fatalf("failed to register builtin providers: %v", err)
	}

	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_999\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.added\", \"item\": {\"type\": \"message\", \"id\": \"item_msg\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_text.delta\", \"delta\": \"Hello parity\"}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.output_item.done\", \"item\": {\"type\": \"message\", \"id\": \"item_msg\", \"content\": [{\"type\": \"output_text\", \"text\": \"Hello parity\"}]}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_999\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 10, \"output_tokens\": 5, \"total_tokens\": 15}}}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	// 1. Streaming way
	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)
	msgFromStream, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected streaming result error: %v", err)
	}

	// 2. Complete way (via dispatch / Complete)
	baseOpts := &StreamOptions{
		APIKey: token,
	}
	msgFromComplete, err := Complete(ctx, model, Context{}, baseOpts)
	if err != nil {
		t.Fatalf("unexpected Complete result error: %v", err)
	}

	// Assert parity
	if msgFromStream.ResponseID != msgFromComplete.ResponseID {
		t.Errorf("ResponseID mismatch: stream = %q, complete = %q", msgFromStream.ResponseID, msgFromComplete.ResponseID)
	}
	if msgFromStream.StopReason != msgFromComplete.StopReason {
		t.Errorf("StopReason mismatch: stream = %q, complete = %q", msgFromStream.StopReason, msgFromComplete.StopReason)
	}
	if msgFromStream.Usage.Input != msgFromComplete.Usage.Input || msgFromStream.Usage.Output != msgFromComplete.Usage.Output {
		t.Errorf("Usage mismatch: stream = %+v, complete = %+v", msgFromStream.Usage, msgFromComplete.Usage)
	}
	if len(msgFromStream.Content) != len(msgFromComplete.Content) {
		t.Fatalf("Content block count mismatch: stream = %d, complete = %d", len(msgFromStream.Content), len(msgFromComplete.Content))
	}
	text1 := msgFromStream.Content[0].(TextContent).Text
	text2 := msgFromComplete.Content[0].(TextContent).Text
	if text1 != text2 {
		t.Errorf("Text content mismatch: stream = %q, complete = %q", text1, text2)
	}
}

func TestStreamOpenAICodexResponses_PreConnectionFailure(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	// A server that returns a 500 error immediately
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "Internal Server Error")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)

	var events []AssistantMessageEvent
	for ev := range stream.Events() {
		events = append(events, ev)
	}

	_, err := stream.Result()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify event sequence: there should be NO EventStart event!
	// First and only event must be EventError.
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 event on pre-connection failure, got %d. Types: %v", len(events), eventTypesOf(events))
	}

	if events[0].Type != EventError {
		t.Errorf("expected first event to be %q, got %q", EventError, events[0].Type)
	}
}

func TestStreamOpenAICodexResponses_MalformedSSEJson(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	// An SSE server that sends malformed JSON frame
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Send malformed JSON frame containing potentially sensitive echoed parameters
		fmt.Fprint(w, "data: {invalid json frame with credentials: "+token+"}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	ctx := context.Background()
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)

	_, err := stream.Result()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Verify that the error message is a safe static parse error
	if !strings.Contains(err.Error(), "provider stream parse error") {
		t.Errorf("unexpected error message: %q", err.Error())
	}

	// Verify that the raw token / malformed JSON frame contents are NOT in the error message
	if strings.Contains(err.Error(), token) {
		t.Errorf("sensitive token leaked in error message: %q", err.Error())
	}
	if strings.Contains(err.Error(), "invalid json frame") {
		t.Errorf("raw SSE data leaked in error message: %q", err.Error())
	}
}

func eventTypesOf(events []AssistantMessageEvent) []string {
	res := make([]string, len(events))
	for i, e := range events {
		res[i] = string(e.Type)
	}
	return res
}

func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func TestStreamOpenAICodexResponses_AuthFallback(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "pi-assembly-fallback-*")
	if err != nil {
		t.Fatalf("failed to create temp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	t.Setenv("PI_CODING_AGENT_DIR", tempDir)

	claims := map[string]any{"chatgpt_account_id": "acct_fallback_999"}
	token := makeFakeJWT(t, claims)

	// Write auth.json
	authPath := filepath.Join(tempDir, "auth.json")
	creds := map[string]any{
		"openai-codex": map[string]any{
			"type":    "oauth",
			"access":  token,
			"refresh": "refresh-123",
			"expires": time.Now().UnixMilli() + 3600000,
		},
	}
	data, _ := json.Marshal(creds)
	_ = os.WriteFile(authPath, data, 0o600)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Authorization header missing or mismatched: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("ChatGPT-Account-ID") != "acct_fallback_999" {
			t.Errorf("ChatGPT-Account-ID header missing or mismatched: %q", r.Header.Get("ChatGPT-Account-ID"))
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_fallback\"}}\n\n")
		fmt.Fprint(w, "data: {\"type\": \"response.completed\", \"response\": {\"id\": \"resp_fallback\", \"status\": \"completed\", \"usage\": {\"input_tokens\": 10, \"output_tokens\": 10, \"total_tokens\": 20}}}\n\n")
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: "", // triggers fallback to auth.json
		},
	}

	stream := StreamOpenAICodexResponses(context.Background(), model, Context{}, opts)
	msg, err := stream.Result()
	if err != nil {
		t.Fatalf("unexpected streaming result error: %v", err)
	}

	if msg.ResponseID != "resp_fallback" {
		t.Errorf("expected ResponseID to be 'resp_fallback', got %q", msg.ResponseID)
	}
}

func TestStreamOpenAICodexResponses_Cancellation(t *testing.T) {
	claims := map[string]any{"chatgpt_account_id": "acct_123"}
	token := makeFakeJWT(t, claims)

	// Block channel to control server timing
	serverBlockChan := make(chan struct{})
	defer close(serverBlockChan)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, "data: {\"type\": \"response.created\", \"response\": {\"id\": \"resp_cancel\"}}\n\n")
		// Flush headers and initial event
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Keep request open until client cancels/closes, or parent exits
		select {
		case <-r.Context().Done():
		case <-serverBlockChan:
		}
	}))
	defer srv.Close()

	model := testModel(srv.URL)
	opts := &CodexResponsesOptions{
		StreamOptions: StreamOptions{
			APIKey: token,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := StreamOpenAICodexResponses(ctx, model, Context{}, opts)

	// Wait for stream to start and emit EventStart
	events := stream.Events()
	ev := <-events
	if ev.Type != EventStart {
		t.Fatalf("expected EventStart, got %v", ev.Type)
	}

	// Cancel the context to abort the stream
	cancel()

	// Drain remaining events to allow the stream loop to finish
	for range events {
	}

	msg, err := stream.Result()
	if err == nil {
		t.Fatal("expected error from cancelled stream, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	if msg.StopReason != StopReasonAborted {
		t.Errorf("expected StopReason to be %q, got %q", StopReasonAborted, msg.StopReason)
	}
}
