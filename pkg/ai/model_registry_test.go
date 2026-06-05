package ai

import (
	"slices"
	"testing"
)

func TestModelRegistry(t *testing.T) {
	// 1. Check GetModels returns exactly 4 models sorted by ID
	models := GetModels()
	expectedIDs := []string{"gpt-5.3-codex-spark", "gpt-5.4", "gpt-5.4-mini", "gpt-5.5"}

	if len(models) != len(expectedIDs) {
		t.Fatalf("expected %d models, got %d", len(expectedIDs), len(models))
	}

	for i, id := range expectedIDs {
		if models[i].ID != id {
			t.Errorf("expected model at index %d to be %q, got %q", i, id, models[i].ID)
		}
	}

	// 2. Test GetModel for each expected model
	for _, id := range expectedIDs {
		m, ok := GetModel(id)
		if !ok {
			t.Errorf("expected model %q to be found in registry", id)
			continue
		}

		if m.ID != id {
			t.Errorf("expected retrieved model ID to be %q, got %q", id, m.ID)
		}

		if m.Provider != ProviderIDOpenAICodex {
			t.Errorf("model %s: expected Provider %q, got %q", id, ProviderIDOpenAICodex, m.Provider)
		}

		if m.API != APIIDOpenAICodexResponses {
			t.Errorf("model %s: expected API %q, got %q", id, APIIDOpenAICodexResponses, m.API)
		}

		if m.BaseURL != "https://chatgpt.com/backend-api" {
			t.Errorf("model %s: expected BaseURL %q, got %q", id, "https://chatgpt.com/backend-api", m.BaseURL)
		}

		if !m.Reasoning {
			t.Errorf("model %s: expected Reasoning = true", id)
		}

		if m.Headers != nil {
			t.Errorf("model %s: expected Headers to be nil, got %v", id, m.Headers)
		}

		// Check ThinkingLevelMap
		if m.ThinkingLevelMap == nil {
			t.Errorf("model %s: expected ThinkingLevelMap to be defined", id)
		} else {
			xhigh, ok := m.ThinkingLevelMap[ModelThinkingLevelXHigh]
			if !ok || xhigh == nil || *xhigh != "xhigh" {
				t.Errorf("model %s: expected ThinkingLevelMap[xhigh] to be 'xhigh'", id)
			}
			minimal, ok := m.ThinkingLevelMap[ModelThinkingLevelMinimal]
			if !ok || minimal == nil || *minimal != "low" {
				t.Errorf("model %s: expected ThinkingLevelMap[minimal] to be 'low'", id)
			}
		}
	}

	// 3. Test specific model fields
	// gpt-5.3-codex-spark
	spark, _ := GetModel("gpt-5.3-codex-spark")
	if spark.Name != "GPT-5.3 Codex Spark" {
		t.Errorf("gpt-5.3-codex-spark: expected Name %q, got %q", "GPT-5.3 Codex Spark", spark.Name)
	}
	if !slices.Equal(spark.Input, []InputKind{InputKindText}) {
		t.Errorf("gpt-5.3-codex-spark: expected Input [text], got %v", spark.Input)
	}
	expectedSparkCost := ModelCost{Input: 1.75, Output: 14.0, CacheRead: 0.175, CacheWrite: 0.0}
	if spark.Cost != expectedSparkCost {
		t.Errorf("gpt-5.3-codex-spark: expected Cost %+v, got %+v", expectedSparkCost, spark.Cost)
	}
	if spark.ContextWindow != 128000 || spark.MaxTokens != 128000 {
		t.Errorf("gpt-5.3-codex-spark: expected window/max 128000/128000, got %d/%d", spark.ContextWindow, spark.MaxTokens)
	}

	// gpt-5.4
	gpt54, _ := GetModel("gpt-5.4")
	if gpt54.Name != "GPT-5.4" {
		t.Errorf("gpt-5.4: expected Name %q, got %q", "GPT-5.4", gpt54.Name)
	}
	if !slices.Equal(gpt54.Input, []InputKind{InputKindText, InputKindImage}) {
		t.Errorf("gpt-5.4: expected Input [text, image], got %v", gpt54.Input)
	}
	expectedGPT54Cost := ModelCost{Input: 2.5, Output: 15.0, CacheRead: 0.25, CacheWrite: 0.0}
	if gpt54.Cost != expectedGPT54Cost {
		t.Errorf("gpt-5.4: expected Cost %+v, got %+v", expectedGPT54Cost, gpt54.Cost)
	}
	if gpt54.ContextWindow != 272000 || gpt54.MaxTokens != 128000 {
		t.Errorf("gpt-5.4: expected window/max 272000/128000, got %d/%d", gpt54.ContextWindow, gpt54.MaxTokens)
	}

	// gpt-5.4-mini
	mini, _ := GetModel("gpt-5.4-mini")
	if mini.Name != "GPT-5.4 mini" {
		t.Errorf("gpt-5.4-mini: expected Name %q, got %q", "GPT-5.4 mini", mini.Name)
	}
	if !slices.Equal(mini.Input, []InputKind{InputKindText, InputKindImage}) {
		t.Errorf("gpt-5.4-mini: expected Input [text, image], got %v", mini.Input)
	}
	expectedMiniCost := ModelCost{Input: 0.75, Output: 4.5, CacheRead: 0.075, CacheWrite: 0.0}
	if mini.Cost != expectedMiniCost {
		t.Errorf("gpt-5.4-mini: expected Cost %+v, got %+v", expectedMiniCost, mini.Cost)
	}
	if mini.ContextWindow != 272000 || mini.MaxTokens != 128000 {
		t.Errorf("gpt-5.4-mini: expected window/max 272000/128000, got %d/%d", mini.ContextWindow, mini.MaxTokens)
	}

	// gpt-5.5
	gpt55, _ := GetModel("gpt-5.5")
	if gpt55.Name != "GPT-5.5" {
		t.Errorf("gpt-5.5: expected Name %q, got %q", "GPT-5.5", gpt55.Name)
	}
	if !slices.Equal(gpt55.Input, []InputKind{InputKindText, InputKindImage}) {
		t.Errorf("gpt-5.5: expected Input [text, image], got %v", gpt55.Input)
	}
	expectedGPT55Cost := ModelCost{Input: 5.0, Output: 30.0, CacheRead: 0.5, CacheWrite: 0.0}
	if gpt55.Cost != expectedGPT55Cost {
		t.Errorf("gpt-5.5: expected Cost %+v, got %+v", expectedGPT55Cost, gpt55.Cost)
	}
	if gpt55.ContextWindow != 272000 || gpt55.MaxTokens != 128000 {
		t.Errorf("gpt-5.5: expected window/max 272000/128000, got %d/%d", gpt55.ContextWindow, gpt55.MaxTokens)
	}

	// 4. Test GetModel with invalid name
	_, ok := GetModel("invalid-model-name")
	if ok {
		t.Errorf("expected GetModel to return false for invalid model name")
	}
}
