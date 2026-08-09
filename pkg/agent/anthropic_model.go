package agent

import (
	"net/http"
	"strings"
	"time"

	adkanthropic "github.com/achetronic/adk-utils-go/genai/anthropic"
	"google.golang.org/adk/v2/model"
)

// NewAnthropicModel instantiates an Anthropic model adapter for ADK,
// backed by official Anthropic SDK via achetronic/adk-utils-go.
func NewAnthropicModel(runtimeCfg *RuntimeConfig) model.LLM {
	effort := runtimeCfg.AnthropicThinkingEffort
	if effort == "" {
		effort = runtimeCfg.ThinkingEffort
	}
	mode := runtimeCfg.AnthropicThinkingMode
	if mode == "" {
		mode = runtimeCfg.ThinkingMode
	}
	budget := runtimeCfg.AnthropicThinkingBudgetTokens
	if budget == nil {
		budget = runtimeCfg.ThinkingBudgetTokens
	}

	timeoutSec := runtimeCfg.TimeoutSeconds
	if timeoutSec <= 0 {
		timeoutSec = DefaultHTTPTimeoutSeconds
	}

	anthropicCfg := adkanthropic.Config{
		APIKey:         runtimeCfg.APIKey,
		BaseURL:        strings.TrimSuffix(runtimeCfg.Endpoint, "/"),
		ModelName:      runtimeCfg.Model,
		ThinkingMode:   mode,
		ThinkingEffort: effort,
		HTTPOptions: adkanthropic.HTTPOptions{
			Client: &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		},
	}
	if budget != nil {
		anthropicCfg.ThinkingBudgetTokens = *budget
	}
	return adkanthropic.New(anthropicCfg)
}
