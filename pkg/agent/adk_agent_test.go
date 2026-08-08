package agent

import (
	"strings"
	"testing"
)

func TestGetGeminiThinkingConfig_BothBudgetAndLevelErrors(t *testing.T) {
	budget := 6000
	cfg := &RuntimeConfig{
		Provider:             "gemini",
		GeminiThinkingBudget: &budget,
		GeminiThinkingLevel:  "medium",
	}

	tc, err := getGeminiThinkingConfig(cfg)
	if err == nil {
		t.Fatalf("expected an error when both budget and level are set, got nil (config: %+v)", tc)
	}
	if !strings.Contains(err.Error(), "only one") {
		t.Errorf("expected an error mentioning 'only one', got: %v", err)
	}
	if tc != nil {
		t.Errorf("expected a nil config alongside the error, got %+v", tc)
	}
}

func TestGetGeminiThinkingConfig_LevelOnly(t *testing.T) {
	cfg := &RuntimeConfig{
		Provider:            "gemini",
		GeminiThinkingLevel: "high",
	}

	tc, err := getGeminiThinkingConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tc == nil {
		t.Fatalf("expected a non-nil config")
	}
	if tc.ThinkingLevel != convertThinkingLevel("high") {
		t.Errorf("expected ThinkingLevel to reflect 'high', got %v", tc.ThinkingLevel)
	}
	if tc.ThinkingBudget != nil {
		t.Errorf("expected no ThinkingBudget set, got %v", *tc.ThinkingBudget)
	}
}

func TestGetGeminiThinkingConfig_NonGeminiProviderIgnored(t *testing.T) {
	budget := 6000
	cfg := &RuntimeConfig{
		Provider:             "anthropic",
		GeminiThinkingBudget: &budget,
		GeminiThinkingLevel:  "medium",
	}

	tc, err := getGeminiThinkingConfig(cfg)
	if err != nil {
		t.Fatalf("expected no error for a non-gemini provider (should be ignored entirely), got: %v", err)
	}
	if tc != nil {
		t.Errorf("expected a nil config for a non-gemini provider, got %+v", tc)
	}
}
