package ai

import "slices"

var extendedThinkingLevels = []ModelThinkingLevel{
	ModelThinkingLevelOff,
	ModelThinkingLevelMinimal,
	ModelThinkingLevelLow,
	ModelThinkingLevelMedium,
	ModelThinkingLevelHigh,
	ModelThinkingLevelXHigh,
}

// Model represents a registry entry describing model capabilities and limits.
type Model struct {
	ID               string                         `json:"id"`
	Name             string                         `json:"name"`
	Provider         ProviderID                     `json:"provider"`
	API              APIID                          `json:"api"`
	BaseURL          string                         `json:"baseUrl"`
	Input            []InputKind                    `json:"input"`
	Reasoning        bool                           `json:"reasoning"`
	ThinkingLevelMap map[ModelThinkingLevel]*string `json:"thinkingLevelMap,omitempty"`
	Cost             ModelCost                      `json:"cost"`
	ContextWindow    int                            `json:"contextWindow"`
	MaxTokens        int                            `json:"maxTokens"`
	Headers          map[string]string              `json:"headers,omitempty"`
	Compat           any                            `json:"compat,omitempty"` // API-specific compat shape; typed per-provider
}

// GetSupportedThinkingLevels resolves the thinking levels supported by the model.
func GetSupportedThinkingLevels(model Model) []ModelThinkingLevel {
	if !model.Reasoning {
		return []ModelThinkingLevel{ModelThinkingLevelOff}
	}

	result := make([]ModelThinkingLevel, 0, len(extendedThinkingLevels))
	for _, level := range extendedThinkingLevels {
		if model.ThinkingLevelMap != nil {
			mapped, exists := model.ThinkingLevelMap[level]
			if exists {
				if mapped == nil {
					continue
				}
			} else if level == ModelThinkingLevelXHigh {
				continue
			}
		} else if level == ModelThinkingLevelXHigh {
			continue
		}
		result = append(result, level)
	}
	return result
}

// ClampThinkingLevel resolves requested thinking level to the closest supported level.
func ClampThinkingLevel(model Model, level ModelThinkingLevel) ModelThinkingLevel {
	available := GetSupportedThinkingLevels(model)

	contains := func(slice []ModelThinkingLevel, val ModelThinkingLevel) bool {
		return slices.Contains(slice, val)
	}

	if contains(available, level) {
		return level
	}

	requestedIndex := -1
	for i, item := range extendedThinkingLevels {
		if item == level {
			requestedIndex = i
			break
		}
	}

	if requestedIndex == -1 {
		if len(available) > 0 {
			return available[0]
		}
		return ModelThinkingLevelOff
	}

	for i := requestedIndex; i < len(extendedThinkingLevels); i++ {
		candidate := extendedThinkingLevels[i]
		if contains(available, candidate) {
			return candidate
		}
	}

	for i := requestedIndex - 1; i >= 0; i-- {
		candidate := extendedThinkingLevels[i]
		if contains(available, candidate) {
			return candidate
		}
	}

	if len(available) > 0 {
		return available[0]
	}
	return ModelThinkingLevelOff
}

// CalculateCost computes financial costs based on usage.
func CalculateCost(model Model, usage Usage) UsageCost {
	cost := UsageCost{
		Input:      (model.Cost.Input / 1000000.0) * float64(usage.Input),
		Output:     (model.Cost.Output / 1000000.0) * float64(usage.Output),
		CacheRead:  (model.Cost.CacheRead / 1000000.0) * float64(usage.CacheRead),
		CacheWrite: (model.Cost.CacheWrite / 1000000.0) * float64(usage.CacheWrite),
	}
	cost.Total = cost.Input + cost.Output + cost.CacheRead + cost.CacheWrite
	return cost
}

// ModelsAreEqual compares two models for equivalence by ID and Provider.
func ModelsAreEqual(a, b *Model) bool {
	if a == nil || b == nil {
		return false
	}
	return a.ID == b.ID && a.Provider == b.Provider
}
