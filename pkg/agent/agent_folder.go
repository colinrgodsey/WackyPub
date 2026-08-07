package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// FolderAgent encapsulates an agent loaded from a folder environment (<ws_dir>/<agent_id>).
type FolderAgent struct {
	AgentID       string
	AgentDir      string
	RuntimeConfig *RuntimeConfig
	SystemPrompt  string
	MemoryPrompt  string
	Model         model.LLM
	ADKAgent      agent.Agent
}

// LoadFolderAgent loads and initializes an agent from <wsDir>/<agentID>.
func LoadFolderAgent(wsDir string, agentID string) (*FolderAgent, error) {
	agentDir := filepath.Join(wsDir, agentID)

	// Check if agent directory exists
	st, err := os.Stat(agentDir)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("agent directory %s does not exist or is not a directory", agentDir)
	}

	// 1. Load runtime.json
	runtimeCfg, err := LoadRuntimeConfig(agentDir)
	if err != nil {
		return nil, err
	}

	// 2. Load AGENTS.md and expand macros (@<FILE_PATH>)
	expandedPrompt, err := RenderAgentSystemPrompt(wsDir, agentID)
	if err != nil {
		return nil, err
	}

	// 3. Load MEMORY.md
	memoryContent, err := ReadMemoryFile(agentDir)
	if err != nil {
		return nil, err
	}

	// 4. Instantiate Model (using OpenAIModel adapter or default Gemini if endpoint is missing)
	var llmModel model.LLM
	if runtimeCfg.Endpoint != "" {
		llmModel = NewOpenAIModel(runtimeCfg)
	} else {
		// Fallback to Gemini if no endpoint provided
		llmModel, err = CreateGeminiModel(context.Background(), runtimeCfg.Model, runtimeCfg.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create fallback model for %s: %w", agentID, err)
		}
	}

	// 5. Construct ADK llmagent with agentID and expanded prompt instruction
	ag, err := BuildADKAgent(agentID, expandedPrompt, llmModel)
	if err != nil {
		return nil, fmt.Errorf("failed to build ADK agent for folder agent %s: %w", agentID, err)
	}

	return &FolderAgent{
		AgentID:       agentID,
		AgentDir:      agentDir,
		RuntimeConfig: runtimeCfg,
		SystemPrompt:  expandedPrompt,
		MemoryPrompt:  memoryContent,
		Model:         llmModel,
		ADKAgent:      ag,
	}, nil
}

// GenerateTurn performs the agent generation turn for the current session.
// Injects MEMORY.md between <PERSISTENT_MEMORY> tags as user turn 1, followed by session.jsonl turns.
func (fa *FolderAgent) GenerateTurn(ctx context.Context) (string, error) {
	// 1. Check for context window compaction trigger before generating
	_, err := CheckAndCompactSession(ctx, fa.AgentDir, fa.RuntimeConfig, fa.SystemPrompt, fa.Model)
	if err != nil {
		// Log compaction warning, but continue execution if possible
		fmt.Fprintf(os.Stderr, "Warning: session compaction error: %v\n", err)
	}

	// Re-read memory in case compaction updated MEMORY.md
	memContent, _ := ReadMemoryFile(fa.AgentDir)
	turns, err := ReadSessionTurns(fa.AgentDir)
	if err != nil {
		return "", err
	}

	// 2. Build full contents array for LLM request
	var contents []*genai.Content

	// First user turn is ALWAYS the system prompt plus current contents of MEMORY.md in
	// <PERSISTENT_MEMORY> tags, sent as a standard user turn (not a "system" role message)
	// for broad compatibility across OpenAI-compatible backends.
	memTurnText := FormatPersistentMemoryTurn(memContent)
	firstTurnText := fa.SystemPrompt + "\n\n" + memTurnText
	contents = append(contents, genai.NewContentFromText(firstTurnText, "user"))

	// Append turns directly from session.jsonl (already genai.Content)
	contents = append(contents, turns...)

	// Collapse consecutive user turns (e.g. multiple `add` calls, or the
	// injected first turn landing before another user turn) into single
	// messages — many backends reject or mishandle non-alternating roles.
	// session.jsonl itself is left untouched; this only affects what's sent.
	contents = MergeConsecutiveUserTurns(contents)

	// 3. Issue LLM generation request
	req := &model.LLMRequest{
		Model:    fa.Model.Name(),
		Contents: contents,
	}

	var responseContent *genai.Content
	for resp, err := range fa.Model.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", fmt.Errorf("generation error: %w", err)
		}
		if resp != nil && resp.Content != nil {
			responseContent = resp.Content
		}
	}

	if responseContent == nil || len(responseContent.Parts) == 0 {
		return "", fmt.Errorf("received empty response from agent")
	}

	// Extract text for return value
	generatedResponse := ContentText(responseContent)
	if generatedResponse == "" {
		return "", fmt.Errorf("received empty text response from agent")
	}

	// 4. Append full assistant Content (preserves all parts: text, thinking, etc.)
	persistContent := responseContent
	if !fa.RuntimeConfig.SupportsReasoningDetails {
		persistContent = StripReasoningDetails(responseContent)
	}
	if err := AppendSessionContent(fa.AgentDir, persistContent); err != nil {
		return generatedResponse, fmt.Errorf("failed to append turn to session.jsonl: %w", err)
	}

	return generatedResponse, nil
}

// Helper to run ADK runner session for folder agent
func (fa *FolderAgent) RunWithRunner(ctx context.Context, sessionID string, prompt string) ([]*session.Event, error) {
	sessionService := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:           "wackypub",
		Agent:             fa.ADKAgent,
		SessionService:    sessionService,
		AutoCreateSession: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create runner: %w", err)
	}

	userMsg := genai.NewContentFromText(prompt, "user")
	var events []*session.Event
	for event, err := range r.Run(ctx, "user", sessionID, userMsg, agent.RunConfig{}) {
		if err != nil {
			return events, err
		}
		if event != nil {
			events = append(events, event)
		}
	}

	return events, nil
}
