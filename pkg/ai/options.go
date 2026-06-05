package ai

import (
	"net/http"
)

// StreamOptions contains request parameter options for provider streaming.
type StreamOptions struct {
	Temperature               *float64
	MaxTokens                 *int
	AccessToken               string // OAuth/subscription bearer token
	Headers                   map[string]string
	Transport                 Transport
	CacheRetention            CacheRetention
	SessionID                 string
	TimeoutMs                 *int
	WebsocketConnectTimeoutMs *int
	MaxRetries                *int
	MaxRetryDelayMs           *int
	Metadata                  map[string]any
	OnRequest                 func(*http.Request, []byte)                                               `json:"-"`
	OnResponse                func(*http.Response)                                                      `json:"-"`
	OnPayload                 func(payload any, model Model) (replaced any, didReplace bool, err error) `json:"-"`
}

// ThinkingBudgets configures custom token limits for reasoning levels.
type ThinkingBudgets struct {
	Minimal *int
	Low     *int
	Medium  *int
	High    *int
}

// SimpleStreamOptions wraps StreamOptions with thinking-budget parameters.
type SimpleStreamOptions struct {
	StreamOptions
	Reasoning       ModelThinkingLevel
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
