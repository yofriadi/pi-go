package ai

import "slices"

var staticModels = map[string]Model{
	"gpt-5.3-codex-spark": {
		ID:        "gpt-5.3-codex-spark",
		Name:      "GPT-5.3 Codex Spark",
		Provider:  ProviderIDOpenAICodex,
		API:       APIIDOpenAICodexResponses,
		BaseURL:   "https://chatgpt.com/backend-api",
		Input:     []InputKind{InputKindText},
		Reasoning: true,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelXHigh:   strPtr("xhigh"),
			ModelThinkingLevelMinimal: strPtr("low"),
		},
		Cost: ModelCost{
			Input:      1.75,
			Output:     14.0,
			CacheRead:  0.175,
			CacheWrite: 0.0,
		},
		ContextWindow: 128000,
		MaxTokens:     128000,
		Headers:       nil,
	},
	"gpt-5.4": {
		ID:        "gpt-5.4",
		Name:      "GPT-5.4",
		Provider:  ProviderIDOpenAICodex,
		API:       APIIDOpenAICodexResponses,
		BaseURL:   "https://chatgpt.com/backend-api",
		Input:     []InputKind{InputKindText, InputKindImage},
		Reasoning: true,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelXHigh:   strPtr("xhigh"),
			ModelThinkingLevelMinimal: strPtr("low"),
		},
		Cost: ModelCost{
			Input:      2.5,
			Output:     15.0,
			CacheRead:  0.25,
			CacheWrite: 0.0,
		},
		ContextWindow: 272000,
		MaxTokens:     128000,
		Headers:       nil,
	},
	"gpt-5.4-mini": {
		ID:        "gpt-5.4-mini",
		Name:      "GPT-5.4 mini",
		Provider:  ProviderIDOpenAICodex,
		API:       APIIDOpenAICodexResponses,
		BaseURL:   "https://chatgpt.com/backend-api",
		Input:     []InputKind{InputKindText, InputKindImage},
		Reasoning: true,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelXHigh:   strPtr("xhigh"),
			ModelThinkingLevelMinimal: strPtr("low"),
		},
		Cost: ModelCost{
			Input:      0.75,
			Output:     4.5,
			CacheRead:  0.075,
			CacheWrite: 0.0,
		},
		ContextWindow: 272000,
		MaxTokens:     128000,
		Headers:       nil,
	},
	"gpt-5.5": {
		ID:        "gpt-5.5",
		Name:      "GPT-5.5",
		Provider:  ProviderIDOpenAICodex,
		API:       APIIDOpenAICodexResponses,
		BaseURL:   "https://chatgpt.com/backend-api",
		Input:     []InputKind{InputKindText, InputKindImage},
		Reasoning: true,
		ThinkingLevelMap: map[ModelThinkingLevel]*string{
			ModelThinkingLevelXHigh:   strPtr("xhigh"),
			ModelThinkingLevelMinimal: strPtr("low"),
		},
		Cost: ModelCost{
			Input:      5.0,
			Output:     30.0,
			CacheRead:  0.5,
			CacheWrite: 0.0,
		},
		ContextWindow: 272000,
		MaxTokens:     128000,
		Headers:       nil,
	},
}

// GetModel retrieves a model by its ID.
func GetModel(id string) (Model, bool) {
	m, ok := staticModels[id]
	return m, ok
}

// GetModels returns all statically registered models sorted alphabetically by ID.
func GetModels() []Model {
	ids := make([]string, 0, len(staticModels))
	for id := range staticModels {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	models := make([]Model, 0, len(ids))
	for _, id := range ids {
		models = append(models, staticModels[id])
	}
	return models
}

func strPtr(s string) *string {
	return &s
}
