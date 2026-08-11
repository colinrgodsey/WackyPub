package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultHTTPTimeoutSeconds is the default timeout (15 minutes) for HTTP client calls to LLM backends.
const DefaultHTTPTimeoutSeconds = 900

// RuntimeConfig represents the agent's runtime.json configuration.
type RuntimeConfig struct {
	// Provider selects the model provider: "openai" (default when Endpoint is set),
	// "gemini" (default when Endpoint is empty), or "anthropic".
	Provider string `json:"provider,omitempty"`

	Endpoint          string  `json:"endpoint"`
	Model             string  `json:"model"`
	APIKey            string  `json:"apiKey"`
	SessionCompactPct float64 `json:"sessionCompactPct"`
	ContextWindow     int     `json:"contextWindow"`

	// TimeoutSeconds sets the HTTP client timeout in seconds for API calls to the LLM backend.
	// Defaults to DefaultHTTPTimeoutSeconds (900s / 15 minutes) when unset or <= 0.
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	// Anthropic-specific thinking fields:
	AnthropicThinkingBudgetTokens *int   `json:"anthropicThinkingBudgetTokens,omitempty"`
	AnthropicThinkingEffort       string `json:"anthropicThinkingEffort,omitempty"`
	AnthropicThinkingMode         string `json:"anthropicThinkingMode,omitempty"`

	// Gemini-specific thinking fields:
	GeminiThinkingBudget  *int   `json:"geminiThinkingBudget,omitempty"`
	GeminiThinkingLevel   string `json:"geminiThinkingLevel,omitempty"`
	GeminiIncludeThoughts *bool  `json:"geminiIncludeThoughts,omitempty"`

	// OpenAI / OpenRouter-specific reasoning fields:
	ReasoningEffort          string         `json:"reasoningEffort,omitempty"`
	ReasoningEgress          string         `json:"reasoningEgress,omitempty"`
	ReasoningField           string         `json:"reasoningField,omitempty"`
	SupportsReasoningDetails bool           `json:"supportsReasoningDetails,omitempty"`
	ExtraBody                map[string]any `json:"extraBody,omitempty"`

	// Generic thinking aliases (fallback if provider-specific fields are unset):
	ThinkingBudgetTokens *int   `json:"thinkingBudgetTokens,omitempty"`
	ThinkingEffort       string `json:"thinkingEffort,omitempty"`
	ThinkingMode         string `json:"thinkingMode,omitempty"`

	// PreserveThinking should be set for backends that resend and bill for
	// prior reasoning/thinking text on every turn (e.g. Kimi K2 Thinking,
	// DeepSeek V4 thinking mode, or any provider used with reasoning egress
	// enabled). When true, EstimateTokens includes Thought-marked part text
	// in its count, since that text is actually replayed to the model on
	// every subsequent request and consumes real context budget. Leave false
	// for backends that drop or ignore reasoning_content in history by
	// default (e.g. Qwen3), where thinking never counts toward future
	// requests' token usage.
	PreserveThinking bool `json:"preserveThinking,omitempty"`
}

// LoadRuntimeConfig reads and unmarshals runtime.json for an agent.
// Loads workspace root and per-agent .env files, expands environment variables (${VAR} / $VAR) in runtime.json data,
// and handles symlinks transparently using os.ReadFile / filepath.EvalSymlinks.
func LoadRuntimeConfig(agentDir string) (*RuntimeConfig, error) {
	// 0. Load root and per-agent .env files into environment
	_, _ = LoadAgentDotEnv(agentDir)

	runtimePath := filepath.Join(agentDir, "runtime.json")

	// Resolve symlink if runtimePath is a symlink
	realPath, err := filepath.EvalSymlinks(runtimePath)
	if err != nil {
		if os.IsNotExist(err) {
			// If missing, check if runtimePath itself exists (in case EvalSymlinks failed because dest is missing)
			realPath = runtimePath
		} else {
			realPath = runtimePath
		}
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime config from %s: %w", runtimePath, err)
	}

	// Expand environment variables (${VAR} / $VAR) in runtime.json data
	expandedData := os.ExpandEnv(string(data))

	var cfg RuntimeConfig
	if err := json.Unmarshal([]byte(expandedData), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse runtime.json at %s: %w", runtimePath, err)
	}

	// Set defaults if empty
	if cfg.SessionCompactPct <= 0 {
		cfg.SessionCompactPct = 50.0
	}

	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		if cfg.Endpoint != "" {
			provider = "openai"
		} else {
			provider = "gemini"
		}
	}
	cfg.Provider = provider

	return &cfg, nil
}
