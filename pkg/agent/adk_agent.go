package agent

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/session"
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
// Name is agentID (unique within workspace), Instruction is the fully rendered system prompt (e.g. from AGENTS.md).
func BuildADKAgent(agentID string, renderedPrompt string, llmModel model.LLM) (agent.Agent, error) {
	cfg := llmagent.Config{
		Name:        agentID,
		Description: fmt.Sprintf("Agent %s", agentID),
		Instruction: renderedPrompt,
		Model:       llmModel,
	}

	ag, err := llmagent.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to build ADK agent %q: %w", agentID, err)
	}

	return ag, nil
}

// ExtractTextFromEvent parses plain text output from an ADK session event.
func ExtractTextFromEvent(event *session.Event) string {
	if event == nil || event.Content == nil {
		return ""
	}
	var text string
	for _, part := range event.Content.Parts {
		if part != nil && part.Text != "" {
			text += part.Text
		}
	}
	return text
}
