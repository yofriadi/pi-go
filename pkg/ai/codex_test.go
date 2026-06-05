package ai

import (
	"context"
	"strings"
	"testing"
)

func TestRegisterBuiltinProviders(t *testing.T) {
	// Clean slate
	ClearApiProviders()
	defer ClearApiProviders()

	// Ensure no providers registered initially
	providers := GetApiProviders()
	if len(providers) != 0 {
		t.Fatalf("expected 0 providers, got %d", len(providers))
	}

	// Register
	err := RegisterBuiltinProviders()
	if err != nil {
		t.Fatalf("expected no error registering builtin providers, got %v", err)
	}

	// Ensure only APIIDOpenAICodexResponses is registered
	providers = GetApiProviders()
	if len(providers) != 1 {
		t.Fatalf("expected exactly 1 provider, got %d", len(providers))
	}

	if providers[0].API != APIIDOpenAICodexResponses {
		t.Errorf("expected registered provider API %q, got %q", APIIDOpenAICodexResponses, providers[0].API)
	}

	// Check registry lookup
	_, ok := GetApiProvider(APIIDOpenAICodexResponses)
	if !ok {
		t.Errorf("expected GetApiProvider to find %q", APIIDOpenAICodexResponses)
	}

	_, ok = GetApiProvider("invalid-provider-api")
	if ok {
		t.Errorf("expected GetApiProvider to return false for invalid provider")
	}

	// Verify duplicate registration failure
	err = RegisterBuiltinProviders()
	if err == nil {
		t.Error("expected error when registering duplicate API provider, got nil")
	} else if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("expected duplicate error message to contain 'already registered', got %q", err.Error())
	}
}

func TestRegisterBuiltinProviders_Dispatch(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	err := RegisterBuiltinProviders()
	if err != nil {
		t.Fatalf("failed to register builtin provider: %v", err)
	}

	validModel, ok := GetModel("gpt-5.3-codex-spark")
	if !ok {
		t.Fatal("failed to get gpt-5.3-codex-spark from registry")
	}

	ctx := context.Background()

	// 1. Dispatch Stream (routed to StreamOpenAICodexResponses)
	s := Stream(ctx, validModel, Context{}, &StreamOptions{})
	_, err = s.Result()
	if err == nil {
		t.Error("expected stream result error, got nil")
	} else if !strings.Contains(err.Error(), "transport not implemented") {
		t.Errorf("expected 'transport not implemented', got %v", err)
	}

	// 2. Dispatch StreamSimple (routed to StreamSimpleOpenAICodexResponses)
	sSimple := StreamSimple(ctx, validModel, Context{}, &SimpleStreamOptions{})
	_, err = sSimple.Result()
	if err == nil {
		t.Error("expected stream simple result error, got nil")
	} else if !strings.Contains(err.Error(), "transport not implemented") {
		t.Errorf("expected 'transport not implemented', got %v", err)
	}
}

func TestStreamOpenAICodexResponses_Validation(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	ctx := context.Background()

	// 1. Invalid provider
	badProviderModel := Model{
		ID:       "bad-model",
		Provider: "some-other-provider",
		API:      APIIDOpenAICodexResponses,
	}
	s1 := StreamOpenAICodexResponses(ctx, badProviderModel, Context{}, nil)
	_, err := s1.Result()
	if err == nil {
		t.Error("expected error for invalid provider, got nil")
	} else if !strings.Contains(err.Error(), "invalid model provider") {
		t.Errorf("expected error containing 'invalid model provider', got: %v", err)
	}

	// 2. Invalid API
	badAPIModel := Model{
		ID:       "bad-model",
		Provider: ProviderIDOpenAICodex,
		API:      "some-other-api",
	}
	s2 := StreamOpenAICodexResponses(ctx, badAPIModel, Context{}, nil)
	_, err = s2.Result()
	if err == nil {
		t.Error("expected error for invalid API, got nil")
	} else if !strings.Contains(err.Error(), "invalid model provider") {
		t.Errorf("expected error containing 'invalid model provider', got: %v", err)
	}

	// 3. Valid model (returns transport not implemented)
	validModel, ok := GetModel("gpt-5.3-codex-spark")
	if !ok {
		t.Fatal("failed to get gpt-5.3-codex-spark from registry")
	}
	s3 := StreamOpenAICodexResponses(ctx, validModel, Context{}, nil)
	_, err = s3.Result()
	if err == nil {
		t.Error("expected error for transport not implemented, got nil")
	} else if !strings.Contains(err.Error(), "transport not implemented") {
		t.Errorf("expected error containing 'transport not implemented', got: %v", err)
	}
}

func TestStreamSimpleOpenAICodexResponses_Validation(t *testing.T) {
	ClearApiProviders()
	defer ClearApiProviders()

	ctx := context.Background()

	// 1. Invalid provider
	badProviderModel := Model{
		ID:       "bad-model",
		Provider: "some-other-provider",
		API:      APIIDOpenAICodexResponses,
	}
	s1 := StreamSimpleOpenAICodexResponses(ctx, badProviderModel, Context{}, nil)
	_, err := s1.Result()
	if err == nil {
		t.Error("expected error for invalid provider, got nil")
	} else if !strings.Contains(err.Error(), "invalid model provider") {
		t.Errorf("expected error containing 'invalid model provider', got: %v", err)
	}

	// 2. Invalid API
	badAPIModel := Model{
		ID:       "bad-model",
		Provider: ProviderIDOpenAICodex,
		API:      "some-other-api",
	}
	s2 := StreamSimpleOpenAICodexResponses(ctx, badAPIModel, Context{}, nil)
	_, err = s2.Result()
	if err == nil {
		t.Error("expected error for invalid API, got nil")
	} else if !strings.Contains(err.Error(), "invalid model provider") {
		t.Errorf("expected error containing 'invalid model provider', got: %v", err)
	}

	// 3. Valid model (returns transport not implemented)
	validModel, ok := GetModel("gpt-5.3-codex-spark")
	if !ok {
		t.Fatal("failed to get gpt-5.3-codex-spark from registry")
	}
	s3 := StreamSimpleOpenAICodexResponses(ctx, validModel, Context{}, nil)
	_, err = s3.Result()
	if err == nil {
		t.Error("expected error for transport not implemented, got nil")
	} else if !strings.Contains(err.Error(), "transport not implemented") {
		t.Errorf("expected error containing 'transport not implemented', got: %v", err)
	}
}

func TestMapSimpleOptionsToCodex(t *testing.T) {
	model, ok := GetModel("gpt-5.3-codex-spark")
	if !ok {
		t.Fatal("failed to get gpt-5.3-codex-spark from registry")
	}

	// 1. Nil options
	opts := mapSimpleOptionsToCodex(model, nil)
	if opts.ReasoningEffort != "" {
		t.Errorf("expected empty ReasoningEffort for nil options, got %q", opts.ReasoningEffort)
	}

	// 2. Reasoning off (maps to empty / no effort request)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelOff,
	})
	if opts.ReasoningEffort != "" {
		t.Errorf("expected empty ReasoningEffort for ReasoningOff, got %q", opts.ReasoningEffort)
	}

	// 3. Reasoning minimal (maps to "low" via spark's ThinkingLevelMap)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelMinimal,
	})
	if opts.ReasoningEffort != "low" {
		t.Errorf("expected ReasoningEffort 'low' for ReasoningMinimal, got %q", opts.ReasoningEffort)
	}

	// 4. Reasoning low (maps to "low" directly)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelLow,
	})
	if opts.ReasoningEffort != "low" {
		t.Errorf("expected ReasoningEffort 'low' for ReasoningLow, got %q", opts.ReasoningEffort)
	}

	// 5. Reasoning medium (maps to "medium" directly)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelMedium,
	})
	if opts.ReasoningEffort != "medium" {
		t.Errorf("expected ReasoningEffort 'medium' for ReasoningMedium, got %q", opts.ReasoningEffort)
	}

	// 6. Reasoning high (maps to "high" directly)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelHigh,
	})
	if opts.ReasoningEffort != "high" {
		t.Errorf("expected ReasoningEffort 'high' for ReasoningHigh, got %q", opts.ReasoningEffort)
	}

	// 7. Reasoning xhigh (maps to "xhigh" via spark's ThinkingLevelMap)
	opts = mapSimpleOptionsToCodex(model, &SimpleStreamOptions{
		Reasoning: ModelThinkingLevelXHigh,
	})
	if opts.ReasoningEffort != "xhigh" {
		t.Errorf("expected ReasoningEffort 'xhigh' for ReasoningXHigh, got %q", opts.ReasoningEffort)
	}

	// 8. Assert that all other SimpleStreamOptions / StreamOptions fields map correctly (verification of BuildBaseOptions mapping)
	mockAPIKey := "test-oauth-token"
	mockSessionID := "test-session-123"
	mockHeaders := map[string]string{"X-Test": "value"}
	mockTransport := Transport("sse")
	customLimit := 50000

	simpleOpts := &SimpleStreamOptions{
		StreamOptions: StreamOptions{
			APIKey:         mockAPIKey,
			SessionID:      mockSessionID,
			Headers:        mockHeaders,
			Transport:      mockTransport,
			MaxTokens:      &customLimit,
			CacheRetention: CacheRetentionLong,
		},
		Reasoning: ModelThinkingLevelHigh,
	}

	mapped := mapSimpleOptionsToCodex(model, simpleOpts)
	if mapped.APIKey != mockAPIKey {
		t.Errorf("expected APIKey %q, got %q", mockAPIKey, mapped.APIKey)
	}
	if mapped.SessionID != mockSessionID {
		t.Errorf("expected SessionID %q, got %q", mockSessionID, mapped.SessionID)
	}
	if mapped.Headers["X-Test"] != "value" {
		t.Errorf("expected Header X-Test to be 'value', got %q", mapped.Headers["X-Test"])
	}
	if mapped.Transport != mockTransport {
		t.Errorf("expected Transport %q, got %q", mockTransport, mapped.Transport)
	}
	if mapped.CacheRetention != CacheRetentionLong {
		t.Errorf("expected CacheRetentionLong, got %q", mapped.CacheRetention)
	}

	// Verify that MaxTokens is correctly adjusted by BuildBaseOptions
	// gpt-5.3-codex-spark max tokens = 128000. high thinking budget defaults to 16384.
	// 50000 + 16384 = 66384.
	expectedMaxTokens := 66384
	if mapped.MaxTokens == nil {
		t.Error("expected MaxTokens to be non-nil")
	} else if *mapped.MaxTokens != expectedMaxTokens {
		t.Errorf("expected MaxTokens adjusted to %d, got %d", expectedMaxTokens, *mapped.MaxTokens)
	}
}
