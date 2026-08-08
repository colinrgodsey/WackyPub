package agent

import (
	"context"
	"fmt"
	"os"

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

// BuildADKAgent constructs a Google ADK LLMAgent for an agent directory.
// Name is agentID (unique within workspace), renderedPrompt is AGENTS.md system prompt, maxToolTurns caps tool executions.
func BuildADKAgent(agentID string, renderedPrompt string, maxToolTurns int, llmModel model.LLM, tools ...tool.Tool) (agent.Agent, error) {
	if maxToolTurns <= 0 {
		maxToolTurns = DefaultMaxToolTurns
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
