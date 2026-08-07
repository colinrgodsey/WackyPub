package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RuntimeConfig represents the agent's runtime.json configuration.
type RuntimeConfig struct {
	Endpoint          string  `json:"endpoint"`
	Model             string  `json:"model"`
	APIKey            string  `json:"apiKey"`
	SessionCompactPct float64 `json:"sessionCompactPct"`
	ContextWindow     int     `json:"contextWindow"`
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
	// ReasoningEgress selects the wire shape used to send a thought Part back
	// as history: "native" (default, empty) sends reasoning as its own field
	// on the assistant message — required by DeepSeek V4 thinking mode and
	// Kimi K2 Thinking. "think_tags" folds it into "content" wrapped in a
	// <think> block instead, for backends that reject an unknown field with a
	// 400 (observed on Mistral, TensorRT-LLM, and some gateways). "omit"
	// sends no reasoning at all, for backends that ignore replayed reasoning
	// anyway (e.g. Qwen3 by default) or to save prompt tokens.
	ReasoningEgress string `json:"reasoningEgress,omitempty"`
	// ReasoningField names the provider's plain-text reasoning field, read on
	// ingest and written on egress. Empty means "reasoning_content" (the
	// adk-utils-go default). OpenRouter returns reasoning under "reasoning"
	// and only accepts "reasoning_content" as an input alias, so set this to
	// "reasoning" there or nothing is read back.
	ReasoningField string `json:"reasoningField,omitempty"`
	// SupportsReasoningDetails allows OpenRouter's structured
	// reasoning_details array (typed blocks, including encrypted/signed
	// reasoning) to be sent back as history. Leave off for backends that
	// don't know the field. Reasoning details found in a response are always
	// captured on the Part regardless of this setting, so enabling it later
	// still replays what earlier turns recorded.
	SupportsReasoningDetails bool `json:"supportsReasoningDetails,omitempty"`
	// ExtraBody carries provider extensions that Chat Completions does not
	// define, merged into the root of every request body. OpenRouter's
	// reasoning controls live here, e.g.:
	//
	//	"extraBody": {"reasoning": {"effort": "high"}}
	//
	// Values must be JSON-serialisable. A key that collides with a field the
	// adapter sets replaces it on the wire, so this is an extension point,
	// not a way to rewrite messages or model.
	ExtraBody map[string]any `json:"extraBody,omitempty"`
}

// LoadRuntimeConfig reads and unmarshals runtime.json for an agent.
// Handles symlinks transparently using os.ReadFile / filepath.EvalSymlinks.
func LoadRuntimeConfig(agentDir string) (*RuntimeConfig, error) {
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

	var cfg RuntimeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse runtime.json at %s: %w", runtimePath, err)
	}

	// Set defaults if empty
	if cfg.SessionCompactPct <= 0 {
		cfg.SessionCompactPct = 50.0
	}

	return &cfg, nil
}
