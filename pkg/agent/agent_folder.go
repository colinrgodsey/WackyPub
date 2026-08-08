package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
	MaxToolTurns  int
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
		MaxToolTurns:  10,
	}, nil
}

// GenerateTurn performs the agent generation turn for the current session.
// Injects MEMORY.md between <PERSISTENT_MEMORY> tags as user turn 1, followed by session.jsonl turns.
// Automatically discovers and registers executable tools from <agent_dir>/tools/ and executes
// tool calls in a loop per D17.
func (fa *FolderAgent) GenerateTurn(ctx context.Context) (string, error) {
	// 1. Check for context window compaction trigger before generating
	_, err := CheckAndCompactSession(ctx, fa.AgentDir, fa.RuntimeConfig, fa.SystemPrompt, fa.Model)
	if err != nil {
		// Log compaction warning, but continue execution if possible
		fmt.Fprintf(os.Stderr, "Warning: session compaction error: %v\n", err)
	}

	// Discover tools for agent
	toolPathMap, discoveredTools, _, err := DiscoverAgentToolsMap(fa.AgentDir)
	if err != nil {
		return "", fmt.Errorf("failed to discover agent tools: %w", err)
	}

	var decls []*genai.FunctionDeclaration

	// Built-in tool 1: set_scratchpad
	decls = append(decls, &genai.FunctionDeclaration{
		Name:        "set_scratchpad",
		Description: "Save a text payload or intermediate command output into a persistent session scratchpad slot by integer ID.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"id": {
					Type:        genai.TypeInteger,
					Description: "Integer ID of the scratchpad slot",
				},
				"text": {
					Type:        genai.TypeString,
					Description: "Text content to store in the scratchpad slot",
				},
			},
			Required: []string{"id", "text"},
		},
	})

	// Built-in tool 2: get_scratchpad
	decls = append(decls, &genai.FunctionDeclaration{
		Name:        "get_scratchpad",
		Description: "Retrieve stored text from a persistent session scratchpad slot by integer ID.",
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"id": {
					Type:        genai.TypeInteger,
					Description: "Integer ID of the scratchpad slot to read",
				},
			},
			Required: []string{"id"},
		},
	})

	for _, name := range discoveredTools {
		decls = append(decls, &genai.FunctionDeclaration{
			Name:        name,
			Description: fmt.Sprintf("Command %s", name),
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"args": {
						Type:        genai.TypeArray,
						Description: "List of CLI command line arguments passed positionally to the tool",
						Items: &genai.Schema{
							Type: genai.TypeString,
						},
					},
					"env": {
						Type:        genai.TypeObject,
						Description: "Key-value object map of environment variables to set for the tool invocation",
					},
					"stdin_scratchpad_id": {
						Type:        genai.TypeInteger,
						Description: "Optional scratchpad slot integer ID to pipe as stdin into the command",
					},
					"stdout_scratchpad_id": {
						Type:        genai.TypeInteger,
						Description: "Optional scratchpad slot integer ID to redirect stdout output into",
					},
				},
			},
		})
	}

	reqConfig := &genai.GenerateContentConfig{
		Tools: []*genai.Tool{
			{
				FunctionDeclarations: decls,
			},
		},
	}

	maxTurns := fa.MaxToolTurns
	if maxTurns <= 0 {
		maxTurns = 10
	}

	for turnCount := 0; turnCount < maxTurns; turnCount++ {
		// Re-read memory and session turns on each iteration
		memContent, _ := ReadMemoryFile(fa.AgentDir)
		turns, err := ReadSessionTurns(fa.AgentDir)
		if err != nil {
			return "", err
		}

		// Build full contents array for LLM request
		var contents []*genai.Content

		// First user turn is ALWAYS system prompt + MEMORY.md
		memTurnText := FormatPersistentMemoryTurn(memContent)
		firstTurnText := fa.SystemPrompt + "\n\n" + memTurnText
		contents = append(contents, genai.NewContentFromText(firstTurnText, "user"))

		// Append turns directly from session.jsonl
		contents = append(contents, turns...)

		// Collapse consecutive user turns
		contents = MergeConsecutiveUserTurns(contents)

		// Issue LLM generation request
		req := &model.LLMRequest{
			Model:    fa.Model.Name(),
			Contents: contents,
			Config:   reqConfig,
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

		// Check for FunctionCall parts
		var funcCalls []*genai.FunctionCall
		for _, part := range responseContent.Parts {
			if part != nil && part.FunctionCall != nil {
				funcCalls = append(funcCalls, part.FunctionCall)
			}
		}

		if len(funcCalls) == 0 {
			// Final text response
			generatedResponse := ContentText(responseContent)
			if generatedResponse == "" {
				return "", fmt.Errorf("received empty text response from agent")
			}

			persistContent := responseContent
			if !fa.RuntimeConfig.SupportsReasoningDetails {
				persistContent = StripReasoningDetails(responseContent)
			}
			if err := AppendSessionContent(fa.AgentDir, persistContent); err != nil {
				return generatedResponse, fmt.Errorf("failed to append turn to session.jsonl: %w", err)
			}

			return generatedResponse, nil
		}

		// Model emitted tool call(s): append assistant response turn to session.jsonl
		persistContent := responseContent
		if !fa.RuntimeConfig.SupportsReasoningDetails {
			persistContent = StripReasoningDetails(responseContent)
		}
		if err := AppendSessionContent(fa.AgentDir, persistContent); err != nil {
			return "", fmt.Errorf("failed to append function call turn to session.jsonl: %w", err)
		}

		// Execute each requested tool and collect FunctionResponse parts
		var frParts []*genai.Part
		for _, fc := range funcCalls {
			var toolOutput string
			if fc.Name == "set_scratchpad" {
				id, hasID := parseIntArg(fc.Args["id"])
				rawText, hasText := fc.Args["text"]
				text, isString := rawText.(string)
				if !hasID {
					toolOutput = "Error: missing or invalid scratchpad id"
				} else if !hasText || !isString {
					toolOutput = "Error: missing or invalid scratchpad text"
				} else {
					out, err := SetScratchpad(fa.AgentDir, id, text)
					if err != nil {
						toolOutput = fmt.Sprintf("Error setting scratchpad %d: %v", id, err)
					} else {
						toolOutput = out
					}
				}
			} else if fc.Name == "get_scratchpad" {
				id, hasID := parseIntArg(fc.Args["id"])
				if !hasID {
					toolOutput = "Error: missing or invalid scratchpad id"
				} else {
					out, err := GetScratchpad(fa.AgentDir, id)
					if err != nil {
						toolOutput = fmt.Sprintf("Error reading scratchpad %d: %v", id, err)
					} else {
						toolOutput = out
					}
				}
			} else {
				toolPath, exists := toolPathMap[fc.Name]
				if !exists {
					toolOutput = fmt.Sprintf("Error: tool %q not found", fc.Name)
				} else {
					toolOutput = executeTool(ctx, fa.AgentDir, fc.Name, toolPath, fc.Args)
				}
			}

			frParts = append(frParts, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{
					Name: fc.Name,
					Response: map[string]any{
						"output": toolOutput,
					},
				},
			})
		}

		frContent := &genai.Content{
			Role:  "user",
			Parts: frParts,
		}
		if err := AppendSessionContent(fa.AgentDir, frContent); err != nil {
			return "", fmt.Errorf("failed to append function response turn to session.jsonl: %w", err)
		}
	}

	return "", fmt.Errorf("exceeded maximum tool turns limit (%d)", maxTurns)
}

func parseIntArg(val any) (int, bool) {
	if val == nil {
		return 0, false
	}
	switch v := val.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		i, err := strconv.Atoi(v)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func executeTool(ctx context.Context, agentDir string, toolName string, toolPath string, args map[string]any) string {
	var cmdArgs []string
	if rawArgs, ok := args["args"]; ok {
		if slice, ok := rawArgs.([]any); ok {
			for _, item := range slice {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%v", item))
			}
		} else if slice, ok := rawArgs.([]string); ok {
			cmdArgs = append(cmdArgs, slice...)
		}
	}

	absToolPath, err := filepath.Abs(toolPath)
	if err != nil {
		absToolPath = toolPath
	}

	cmd := exec.CommandContext(ctx, absToolPath, cmdArgs...)
	cmd.Dir = agentDir
	cmd.Env = os.Environ()

	if rawEnv, ok := args["env"]; ok {
		if envMap, ok := rawEnv.(map[string]any); ok {
			for k, v := range envMap {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", k, v))
			}
		}
	}

	if stdinID, ok := parseIntArg(args["stdin_scratchpad_id"]); ok {
		stdinText, err := GetScratchpad(agentDir, stdinID)
		if err == nil {
			cmd.Stdin = strings.NewReader(stdinText)
		}
	} else if len(args) > 0 {
		argsJSON, err := json.Marshal(args)
		if err == nil {
			cmd.Stdin = bytes.NewReader(argsJSON)
			cmd.Env = append(cmd.Env, "WACKYPUB_TOOL_ARGS="+string(argsJSON))
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		errStr := stderr.String()
		if errStr == "" {
			errStr = err.Error()
		}
		return fmt.Sprintf("Error executing tool %s: %s", toolName, errStr)
	}

	out := stdout.String()
	if stdoutID, ok := parseIntArg(args["stdout_scratchpad_id"]); ok {
		summary, err := SetScratchpad(agentDir, stdoutID, out)
		if err != nil {
			return fmt.Sprintf("Error writing output to scratchpad %d: %v", stdoutID, err)
		}
		return summary
	}

	out = strings.TrimSpace(out)
	if out == "" {
		out = "Command completed with no output."
	}
	return out
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
