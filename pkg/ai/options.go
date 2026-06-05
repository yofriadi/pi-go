package ai

import (
	"net/http"
)

// StreamOptions contains request parameter options for provider streaming.
type StreamOptions struct {
	// Temperature controls randomness of the generated tokens.
	Temperature *float64
	// MaxTokens is the maximum number of tokens to generate in the completion.
	MaxTokens *int
	// APIKey is the OAuth/subscription bearer token (upstream-compatible field name).
	APIKey string
	// Headers contains custom HTTP headers to pass to the provider request.
	Headers map[string]string
	// Transport defines the delivery mechanism (e.g. sse, websocket).
	Transport Transport
	// CacheRetention defines the prompt cache retention strategy (none, short, long).
	CacheRetention CacheRetention
	// SessionID is the unique identifier for the streaming session.
	SessionID string
	// TimeoutMs is the timeout in milliseconds for the HTTP request.
	TimeoutMs *int
	// WebsocketConnectTimeoutMs is the timeout in milliseconds for websocket connection establishment.
	WebsocketConnectTimeoutMs *int
	// MaxRetries is the maximum number of retry attempts for failed requests.
	MaxRetries *int
	// MaxRetryDelayMs is the maximum delay in milliseconds between retries.
	MaxRetryDelayMs *int
	// Metadata contains optional arbitrary metadata for the request.
	Metadata map[string]any
	// OnRequest is a callback triggered before sending the HTTP request.
	OnRequest func(*http.Request, []byte) `json:"-"`
	// OnResponse is a callback triggered after receiving the HTTP response.
	OnResponse func(*http.Response) `json:"-"`
	// OnPayload is a callback for mutating or inspecting raw payload events.
	OnPayload func(payload any, model Model) (replaced any, didReplace bool, err error) `json:"-"`
}

// ThinkingBudgets configures custom token limits for reasoning/thinking levels.
type ThinkingBudgets struct {
	// Minimal budget for thinking tokens.
	Minimal *int
	// Low budget for thinking tokens.
	Low *int
	// Medium budget for thinking tokens.
	Medium *int
	// High budget for thinking tokens.
	High *int
}

// SimpleStreamOptions wraps StreamOptions with thinking-budget parameters.
type SimpleStreamOptions struct {
	StreamOptions
	// Reasoning defines the desired thinking/reasoning level.
	Reasoning ModelThinkingLevel
	// ThinkingBudgets configures custom token limits for reasoning levels.
	ThinkingBudgets *ThinkingBudgets
}

// BuildBaseOptions extracts the embedded StreamOptions from SimpleStreamOptions.
func BuildBaseOptions(model Model, options *SimpleStreamOptions) StreamOptions {
	if options == nil {
		return StreamOptions{}
	}
	opts := options.StreamOptions
	maxTokens, _ := AdjustMaxTokensForThinking(
		opts.MaxTokens,
		model.MaxTokens,
		options.Reasoning,
		options.ThinkingBudgets,
	)
	opts.MaxTokens = &maxTokens
	return opts
}

// AdjustMaxTokensForThinking computes correct thinkingBudget and maxTokens.
func AdjustMaxTokensForThinking(
	baseMaxTokens *int,
	modelMaxTokens int,
	reasoningLevel ModelThinkingLevel,
	customBudgets *ThinkingBudgets,
) (int, int) {
	if reasoningLevel == "" || reasoningLevel == ModelThinkingLevelOff {
		var maxTokens int
		if baseMaxTokens == nil {
			maxTokens = modelMaxTokens
		} else {
			maxTokens = *baseMaxTokens
		}
		return maxTokens, 0
	}
	level := clampReasoning(reasoningLevel)
	thinkingBudget := defaultThinkingBudget(level)
	if customBudgets != nil {
		if b := customBudgetForLevel(level, customBudgets); b != nil {
			thinkingBudget = *b
		}
	}
	if thinkingBudget == 0 {
		thinkingBudget = 1024
	}
	minOutputTokens := 1024
	var maxTokens int
	if baseMaxTokens == nil {
		maxTokens = modelMaxTokens
	} else {
		maxTokens = min(*baseMaxTokens+thinkingBudget, modelMaxTokens)
	}
	if maxTokens <= thinkingBudget {
		thinkingBudget = max(maxTokens-minOutputTokens, 0)
	}
	return maxTokens, thinkingBudget
}

func defaultThinkingBudget(level ModelThinkingLevel) int {
	switch level {
	case ModelThinkingLevelMinimal:
		return 1024
	case ModelThinkingLevelLow:
		return 2048
	case ModelThinkingLevelMedium:
		return 8192
	case ModelThinkingLevelHigh:
		return 16384
	default:
		return 1024
	}
}

func customBudgetForLevel(level ModelThinkingLevel, b *ThinkingBudgets) *int {
	switch level {
	case ModelThinkingLevelMinimal:
		return b.Minimal
	case ModelThinkingLevelLow:
		return b.Low
	case ModelThinkingLevelMedium:
		return b.Medium
	case ModelThinkingLevelHigh:
		return b.High
	default:
		return nil
	}
}

func clampReasoning(effort ModelThinkingLevel) ModelThinkingLevel {
	if effort == ModelThinkingLevelXHigh {
		return ModelThinkingLevelHigh
	}
	return effort
}
