package ai

import (
	"context"
	"errors"
	"fmt"
)

// CodexResponsesOptions contains options specific to the OpenAI Codex Responses API.
type CodexResponsesOptions struct {
	StreamOptions
	ReasoningEffort  string
	ReasoningSummary string
	ServiceTier      string
	TextVerbosity    string
}

// StreamOpenAICodexResponses streams responses from the OpenAI Codex Responses API.
// Before transport is implemented, it returns a pre-completed error stream.
func StreamOpenAICodexResponses(ctx context.Context, model Model, c Context, opts *CodexResponsesOptions) *AssistantStream {
	if model.Provider != ProviderIDOpenAICodex || model.API != APIIDOpenAICodexResponses {
		return newErrorStream(fmt.Errorf("invalid model provider %q or API %q", model.Provider, model.API))
	}
	return newErrorStream(errors.New("transport not implemented"))
}

// StreamSimpleOpenAICodexResponses streams responses using SimpleStreamOptions.
func StreamSimpleOpenAICodexResponses(ctx context.Context, model Model, c Context, opts *SimpleStreamOptions) *AssistantStream {
	if model.Provider != ProviderIDOpenAICodex || model.API != APIIDOpenAICodexResponses {
		return newErrorStream(fmt.Errorf("invalid model provider %q or API %q", model.Provider, model.API))
	}

	codexOpts := mapSimpleOptionsToCodex(model, opts)
	return StreamOpenAICodexResponses(ctx, model, c, codexOpts)
}

// mapSimpleOptionsToCodex converts SimpleStreamOptions to CodexResponsesOptions.
func mapSimpleOptionsToCodex(model Model, opts *SimpleStreamOptions) *CodexResponsesOptions {
	var baseOpts StreamOptions
	if opts != nil {
		baseOpts = BuildBaseOptions(model, opts)
	}

	var reasoningEffort string
	if opts != nil && opts.Reasoning != "" {
		clamped := ClampThinkingLevel(model, opts.Reasoning)
		if clamped != ModelThinkingLevelOff {
			reasoningEffort = mapThinkingLevel(model, clamped)
		}
	}

	return &CodexResponsesOptions{
		StreamOptions:   baseOpts,
		ReasoningEffort: reasoningEffort,
	}
}

// RegisterBuiltinProviders registers the OpenAI Codex Responses provider.
func RegisterBuiltinProviders() error {
	return RegisterApiProvider(ApiProvider{
		API: APIIDOpenAICodexResponses,
		Stream: func(ctx context.Context, model Model, c Context, opts *StreamOptions) *AssistantStream {
			var codexOpts *CodexResponsesOptions
			if opts != nil {
				codexOpts = &CodexResponsesOptions{
					StreamOptions: *opts,
				}
			}
			return StreamOpenAICodexResponses(ctx, model, c, codexOpts)
		},
		StreamSimple: StreamSimpleOpenAICodexResponses,
	})
}

// mapThinkingLevel converts a ModelThinkingLevel to its Codex-compatible string representation.
func mapThinkingLevel(model Model, level ModelThinkingLevel) string {
	if model.ThinkingLevelMap != nil {
		if mapped, ok := model.ThinkingLevelMap[level]; ok && mapped != nil {
			return *mapped
		}
	}
	switch level {
	case ModelThinkingLevelOff:
		return "none"
	case ModelThinkingLevelMinimal:
		return "minimal"
	case ModelThinkingLevelLow:
		return "low"
	case ModelThinkingLevelMedium:
		return "medium"
	case ModelThinkingLevelHigh:
		return "high"
	case ModelThinkingLevelXHigh:
		return "xhigh"
	default:
		return string(level)
	}
}
