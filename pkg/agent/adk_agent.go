package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// DefaultMaxToolTurns is the default cap on consecutive tool-call turns
// within a single GenerateTurn call, used wherever a caller doesn't specify
// one explicitly (the --max-tool-turns CLI flag, AgentSDK.NewSDK, and
// BuildADKAgent/LoadFolderAgent's own <= 0 fallback).
const DefaultMaxToolTurns = 300

// CreateGeminiModel instantiates a native Gemini LLM model using Google ADK model package.
func CreateGeminiModel(ctx context.Context, modelName string, apiKey string) (model.LLM, error) {
	if modelName == "" {
		modelName = "gemini-2.5-flash"
	}

	clientCfg := &genai.ClientConfig{}
	if apiKey != "" {
		clientCfg.APIKey = apiKey
	}

	llmModel, err := gemini.NewModel(ctx, modelName, clientCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ADK Gemini model %q: %w", modelName, err)
	}

	return llmModel, nil
}

// convertThinkingLevel maps a string effort level ("low", "medium", "high", "minimal") to genai.ThinkingLevel.
func convertThinkingLevel(level string) genai.ThinkingLevel {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "MINIMAL":
		return genai.ThinkingLevelMinimal
	case "LOW":
		return genai.ThinkingLevelLow
	case "MEDIUM":
		return genai.ThinkingLevelMedium
	case "HIGH", "MAX":
		return genai.ThinkingLevelHigh
	default:
		return genai.ThinkingLevelUnspecified
	}
}

func getGeminiThinkingConfig(cfg *RuntimeConfig) (*genai.ThinkingConfig, error) {
	if cfg == nil || cfg.Provider != "gemini" {
		return nil, nil
	}

	budget := cfg.GeminiThinkingBudget
	if budget == nil {
		budget = cfg.ThinkingBudgetTokens
	}

	level := cfg.GeminiThinkingLevel
	if level == "" {
		level = cfg.ThinkingEffort
	}

	// Gemini's API rejects a request that sets both - "You can only set only
	// one of thinking budget and thinking level." Fail loudly here instead
	// of silently picking one: a runtime.json with both set is a config
	// mistake worth surfacing, not something to guess through.
	if budget != nil && level != "" {
		return nil, fmt.Errorf("runtime.json sets both geminiThinkingBudget/thinkingBudgetTokens and geminiThinkingLevel/thinkingEffort - Gemini only accepts one, set only one of them")
	}

	if budget == nil && level == "" && cfg.GeminiIncludeThoughts == nil {
		return nil, nil
	}

	include := true
	if cfg.GeminiIncludeThoughts != nil {
		include = *cfg.GeminiIncludeThoughts
	}

	tc := &genai.ThinkingConfig{
		IncludeThoughts: include,
	}
	if budget != nil {
		b := int32(*budget)
		tc.ThinkingBudget = &b
	}
	if level != "" {
		tc.ThinkingLevel = convertThinkingLevel(level)
	}
	return tc, nil
}

// BuildADKAgentWithConfig constructs a Google ADK LLMAgent for an agent directory, applying RuntimeConfig settings.
func BuildADKAgentWithConfig(agentID string, renderedPrompt string, maxToolTurns int, runtimeCfg *RuntimeConfig, llmModel model.LLM, tools ...tool.Tool) (agent.Agent, error) {
	if maxToolTurns <= 0 {
		maxToolTurns = DefaultMaxToolTurns
	}
	var modelCalls int

	thinkingConfig, err := getGeminiThinkingConfig(runtimeCfg)
	if err != nil {
		return nil, fmt.Errorf("invalid thinking config for agent %q: %w", agentID, err)
	}

	cfg := llmagent.Config{
		Name:        agentID,
		Description: fmt.Sprintf("Agent %s", agentID),
		Instruction: renderedPrompt,
		Model:       llmModel,
		Tools:       tools,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
				modelCalls++
				if thinkingConfig != nil {
					if req.Config == nil {
						req.Config = &genai.GenerateContentConfig{}
					}
					if req.Config.ThinkingConfig == nil {
						req.Config.ThinkingConfig = thinkingConfig
					}
				}
				// Check for deferred image response from get_scratchpad per D49
				if hasDef, deferredIDs := hasDeferredScratchpadResponse(req.Contents); hasDef {
					idList := strings.Join(deferredIDs, ", ")
					return &model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{Text: fmt.Sprintf("Image from scratchpad %s has been queued. It will be available in your next turn. Send another message to continue.", idList)},
							},
						},
					}, nil
				}

				// Mid-turn context budget check (D63)
				// If accumulated tool context reaches or exceeds contextWindow on subsequent tool turns (modelCalls > 1),
				// short-circuit early with a synthetic response so the next top-level turn gets a chance to trigger compaction.
				if modelCalls > 1 && runtimeCfg != nil && runtimeCfg.ContextWindow > 0 {
					estTokens := EstimateTokens(req.Contents, runtimeCfg.PreserveThinking)
					if estTokens >= runtimeCfg.ContextWindow {
						fmt.Fprintf(os.Stderr, "Warning: agent %q accumulated ~%d tokens in mid-turn tool context, reaching its context window limit (%d) - stopping early for compaction.\n", agentID, estTokens, runtimeCfg.ContextWindow)
						return &model.LLMResponse{
							Content: &genai.Content{
								Role: "model",
								Parts: []*genai.Part{
									{Text: fmt.Sprintf("[Accumulated tool context reached ~%d tokens (exceeding %d contextWindow budget) - stopping turn early to allow session compaction. Send another message (e.g. \"continue\") to proceed.]", estTokens, runtimeCfg.ContextWindow)},
								},
							},
						}, nil
					}
				}

				// First model call is initial prompt; subsequent model calls are tool loop turns.
				// Stop short rather than error: the caller (human or controlling agent) gets a
				// clear, successful turn back with a hint to send another message to continue,
				// instead of losing whatever the tool loop already accomplished.
				if modelCalls > maxToolTurns+1 {
					fmt.Fprintf(os.Stderr, "Warning: agent %q reached the maximum tool-call turn limit (%d) for this generation - stopping early. Send another message (e.g. \"continue\") to let it keep going.\n", agentID, maxToolTurns)
					return &model.LLMResponse{
						Content: &genai.Content{
							Role: "model",
							Parts: []*genai.Part{
								{Text: fmt.Sprintf("[Reached the maximum of %d consecutive tool calls for this turn - stopping here. Send another message (e.g. \"continue\") to keep going.]", maxToolTurns)},
							},
						},
					}, nil
				}
				return nil, nil
			},
		},
	}

	ag, err := llmagent.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build ADK agent %q: %w", agentID, err)
	}

	return ag, nil
}

// BuildADKAgent constructs a Google ADK LLMAgent for an agent directory.
// Name is agentID (unique within workspace), renderedPrompt is AGENTS.md system prompt, maxToolTurns caps tool executions.
func BuildADKAgent(agentID string, renderedPrompt string, maxToolTurns int, llmModel model.LLM, tools ...tool.Tool) (agent.Agent, error) {
	return BuildADKAgentWithConfig(agentID, renderedPrompt, maxToolTurns, nil, llmModel, tools...)
}

// ExtractTextFromEvent parses plain text output from an ADK session event,
// excluding reasoning/thinking parts - mirrors ContentText's behavior.
func ExtractTextFromEvent(event *session.Event) string {
	if event == nil || event.Content == nil {
		return ""
	}
	var text string
	for _, part := range event.Content.Parts {
		if part != nil && part.Text != "" && !part.Thought {
			text += part.Text
		}
	}
	return text
}

// hasDeferredScratchpadResponse inspects request contents to detect if the most recent turn
// includes a get_scratchpad function response flagged with deferred: true per D49.
func hasDeferredScratchpadResponse(contents []*genai.Content) (bool, []string) {
	if len(contents) == 0 {
		return false, nil
	}
	last := contents[len(contents)-1]
	if last == nil {
		return false, nil
	}
	var deferredIDs []string
	hasDeferred := false
	for _, p := range last.Parts {
		if p != nil && p.FunctionResponse != nil && p.FunctionResponse.Name == "get_scratchpad" {
			respMap := p.FunctionResponse.Response
			if respMap != nil {
				if def, ok := respMap["deferred"].(bool); ok && def {
					hasDeferred = true
					if spID, ok := respMap["scratchpad_id"].(string); ok && spID != "" {
						deferredIDs = append(deferredIDs, spID)
					}
				}
			}
		}
	}
	return hasDeferred, deferredIDs
}
