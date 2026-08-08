package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAnthropicModel(t *testing.T) {
	budget := 2048
	cfg := &RuntimeConfig{
		Provider:             "anthropic",
		APIKey:               "test-anthropic-key",
		Endpoint:             "http://localhost:1234/v1/",
		Model:                "claude-3-7-sonnet-20250219",
		ThinkingBudgetTokens: &budget,
		ThinkingEffort:       "high",
		ThinkingMode:         "enabled",
	}

	model := NewAnthropicModel(cfg)
	if model == nil {
		t.Fatalf("expected non-nil Anthropic model adapter")
	}
	if model.Name() != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected model name 'claude-3-7-sonnet-20250219', got %q", model.Name())
	}
}

func TestLoadRuntimeConfig_Providers(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Explicit Anthropic provider config with dedicated fields
	anthropicJSON := `{
		"provider": "anthropic",
		"model": "claude-3-7-sonnet",
		"apiKey": "sk-ant-123",
		"anthropicThinkingBudgetTokens": 1024,
		"anthropicThinkingEffort": "medium"
	}`
	aDir := filepath.Join(tmpDir, "agent_anthropic")
	if err := os.MkdirAll(aDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(aDir, "runtime.json"), []byte(anthropicJSON), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	cfgA, err := LoadRuntimeConfig(aDir)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig failed for Anthropic: %v", err)
	}
	if cfgA.Provider != "anthropic" {
		t.Errorf("expected Provider 'anthropic', got %q", cfgA.Provider)
	}
	if cfgA.AnthropicThinkingBudgetTokens == nil || *cfgA.AnthropicThinkingBudgetTokens != 1024 {
		t.Errorf("expected AnthropicThinkingBudgetTokens 1024, got %v", cfgA.AnthropicThinkingBudgetTokens)
	}
	if cfgA.AnthropicThinkingEffort != "medium" {
		t.Errorf("expected AnthropicThinkingEffort 'medium', got %q", cfgA.AnthropicThinkingEffort)
	}

	// 2. Explicit Gemini provider config with dedicated fields
	geminiJSON := `{
		"provider": "gemini",
		"model": "gemini-2.5-flash",
		"apiKey": "AIzaSy...",
		"geminiThinkingLevel": "HIGH",
		"geminiThinkingBudget": 2048
	}`
	gDir := filepath.Join(tmpDir, "agent_gemini")
	if err := os.MkdirAll(gDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gDir, "runtime.json"), []byte(geminiJSON), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	cfgG, err := LoadRuntimeConfig(gDir)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig failed for Gemini: %v", err)
	}
	if cfgG.Provider != "gemini" {
		t.Errorf("expected Provider 'gemini', got %q", cfgG.Provider)
	}
	if cfgG.GeminiThinkingLevel != "HIGH" {
		t.Errorf("expected GeminiThinkingLevel 'HIGH', got %q", cfgG.GeminiThinkingLevel)
	}
	if cfgG.GeminiThinkingBudget == nil || *cfgG.GeminiThinkingBudget != 2048 {
		t.Errorf("expected GeminiThinkingBudget 2048, got %v", cfgG.GeminiThinkingBudget)
	}

	// 3. Implicit OpenAI provider with reasoningEffort
	openaiJSON := `{
		"endpoint": "http://localhost:1234/v1",
		"model": "gpt-4o",
		"reasoningEffort": "high"
	}`
	oDir := filepath.Join(tmpDir, "agent_openai")
	if err := os.MkdirAll(oDir, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oDir, "runtime.json"), []byte(openaiJSON), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	cfgO, err := LoadRuntimeConfig(oDir)
	if err != nil {
		t.Fatalf("LoadRuntimeConfig failed for OpenAI: %v", err)
	}
	if cfgO.Provider != "openai" {
		t.Errorf("expected Provider 'openai', got %q", cfgO.Provider)
	}
	if cfgO.ReasoningEffort != "high" {
		t.Errorf("expected ReasoningEffort 'high', got %q", cfgO.ReasoningEffort)
	}
}

// TestLoadFolderAgent_AnthropicProvider exercises LoadFolderAgent's actual
// provider-dispatch switch, not just NewAnthropicModel/LoadRuntimeConfig in
// isolation - confirms provider: "anthropic" in a real runtime.json ends up
// constructing an agent successfully via that path.
func TestLoadFolderAgent_AnthropicProvider(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, "claude")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("Prompt Claude"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}
	runtimeJSON := `{
		"provider": "anthropic",
		"model": "claude-3-7-sonnet-20250219",
		"apiKey": "test-key",
		"endpoint": "http://localhost:1234/v1"
	}`
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(runtimeJSON), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	fa, err := LoadFolderAgent(wsDir, "claude", 1)
	if err != nil {
		t.Fatalf("LoadFolderAgent failed for anthropic provider: %v", err)
	}
	if fa.Model == nil {
		t.Fatalf("expected a non-nil model for anthropic provider")
	}
	if fa.Model.Name() != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected model name 'claude-3-7-sonnet-20250219', got %q", fa.Model.Name())
	}
}

// TestLoadFolderAgent_UnsupportedProvider confirms an unrecognized provider
// value fails loudly instead of silently falling back to some default.
func TestLoadFolderAgent_UnsupportedProvider(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, "bad-provider")
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("Prompt"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}
	runtimeJSON := `{"provider": "azure", "model": "gpt-4o", "apiKey": "test-key"}`
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(runtimeJSON), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	_, err := LoadFolderAgent(wsDir, "bad-provider", 1)
	if err == nil {
		t.Fatalf("expected LoadFolderAgent to fail for an unsupported provider, got nil error")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("expected an 'unsupported provider' error, got: %v", err)
	}
}
