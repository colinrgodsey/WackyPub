package agent

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

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

// BuildADKAgent constructs a Google ADK LLMAgent for an agent directory.
// Name is agentID (unique within workspace), renderedPrompt is AGENTS.md system prompt, maxToolTurns caps tool executions.
func BuildADKAgent(agentID string, renderedPrompt string, maxToolTurns int, llmModel model.LLM, tools ...tool.Tool) (agent.Agent, error) {
	if maxToolTurns <= 0 {
		maxToolTurns = 10
	}
	var modelCalls int

	cfg := llmagent.Config{
		Name:        agentID,
		Description: fmt.Sprintf("Agent %s", agentID),
		Instruction: renderedPrompt,
		Model:       llmModel,
		Tools:       tools,
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{
			func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
				modelCalls++
				// First model call is initial prompt; subsequent model calls are tool loop turns
				if modelCalls > maxToolTurns+1 {
					return nil, fmt.Errorf("exceeded maximum tool turns limit (%d)", maxToolTurns)
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
