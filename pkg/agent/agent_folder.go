package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

type SetScratchpadArgs struct {
	ID   int    `json:"id" jsonschema_description:"Integer ID of the scratchpad slot"`
	Text string `json:"text" jsonschema_description:"Text content to store in the scratchpad slot"`
}

type SetScratchpadResult struct {
	Output string `json:"output"`
}

type GetScratchpadArgs struct {
	ID int `json:"id" jsonschema_description:"Integer ID of the scratchpad slot to read"`
}

type GetScratchpadResult struct {
	Output string `json:"output"`
}

type ExecToolArgs struct {
	Args               []string          `json:"args,omitempty" jsonschema_description:"List of CLI command line arguments passed positionally to the tool"`
	Env                map[string]string `json:"env,omitempty" jsonschema_description:"Key-value object map of environment variables to set for the tool invocation"`
	StdinScratchpadID  *int              `json:"stdin_scratchpad_id,omitempty" jsonschema_description:"Optional scratchpad slot integer ID to pipe as stdin into the command. The slot must have been set by set_scratchpad in an EARLIER turn, not this same one - tool calls within one turn may run concurrently, so a set_scratchpad call earlier in this same response is not guaranteed to have finished yet."`
	StdoutScratchpadID *int              `json:"stdout_scratchpad_id,omitempty" jsonschema_description:"Optional scratchpad slot integer ID to redirect stdout output into. Do not read this slot with get_scratchpad in the same turn - wait until your next turn, since the write may not have finished yet."`
}

type ExecToolResult struct {
	Output string `json:"output"`
}

// BuildFolderAgentTools constructs ADK functiontool instances for built-in tools (set_scratchpad, get_scratchpad)
// and executable tools discovered under <agent_dir>/tools/.
func BuildFolderAgentTools(agentDir string) (map[string]tool.Tool, []*genai.FunctionDeclaration, error) {
	toolMap := make(map[string]tool.Tool)
	var decls []*genai.FunctionDeclaration

	addTool := func(t tool.Tool) {
		toolMap[t.Name()] = t
		if decler, ok := t.(interface {
			Declaration() *genai.FunctionDeclaration
		}); ok {
			decls = append(decls, decler.Declaration())
		}
	}

	// 1. set_scratchpad
	setTool, err := functiontool.New(functiontool.Config{
		Name:        "set_scratchpad",
		Description: "Save a text payload or intermediate command output into a persistent session scratchpad slot by integer ID. IMPORTANT: tool calls within the same turn may execute concurrently - do not set a slot and then reference it (via get_scratchpad or stdin_scratchpad_id) in that same turn. Set it now, use it on your next turn.",
	}, func(ctx agent.Context, args SetScratchpadArgs) (SetScratchpadResult, error) {
		out, err := SetScratchpad(agentDir, args.ID, args.Text)
		if err != nil {
			return SetScratchpadResult{}, fmt.Errorf("failed to set scratchpad %d: %w", args.ID, err)
		}
		return SetScratchpadResult{Output: out}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create set_scratchpad tool: %w", err)
	}
	addTool(setTool)

	// 2. get_scratchpad
	getTool, err := functiontool.New(functiontool.Config{
		Name:        "get_scratchpad",
		Description: "Retrieve stored text from a persistent session scratchpad slot by integer ID. IMPORTANT: do not read a slot in the same turn as a set_scratchpad call or a stdout_scratchpad_id write targeting it - tool calls within one turn may execute concurrently, so the write is not guaranteed to have finished yet. Wait until your next turn.",
	}, func(ctx agent.Context, args GetScratchpadArgs) (GetScratchpadResult, error) {
		out, err := GetScratchpad(agentDir, args.ID)
		if err != nil {
			return GetScratchpadResult{}, fmt.Errorf("failed to read scratchpad %d: %w", args.ID, err)
		}
		return GetScratchpadResult{Output: out}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create get_scratchpad tool: %w", err)
	}
	addTool(getTool)

	// 3. Discovered executable tools
	discoveredMap, discoveredNames, _, err := DiscoverAgentToolsMap(agentDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover agent tools: %w", err)
	}

	for _, name := range discoveredNames {
		toolName := name
		toolPath := discoveredMap[name]

		execT, err := functiontool.New(functiontool.Config{
			Name:        toolName,
			Description: fmt.Sprintf("Command %s", toolName),
		}, func(ctx agent.Context, args ExecToolArgs) (ExecToolResult, error) {
			out, err := executeTool(ctx, agentDir, toolName, toolPath, args)
			if err != nil {
				return ExecToolResult{}, err
			}
			return ExecToolResult{Output: out}, nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create tool %s: %w", toolName, err)
		}
		addTool(execT)
	}

	return toolMap, decls, nil
}

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
func LoadFolderAgent(wsDir string, agentID string, maxToolTurns int) (*FolderAgent, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentID cannot be empty")
	}

	agentDir := filepath.Join(wsDir, agentID)
	if !pathExists(agentDir) {
		return nil, fmt.Errorf("agent directory %s does not exist", agentDir)
	}

	// 1. Load runtime.json
	runtimeCfg, err := LoadRuntimeConfig(agentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read runtime config: %w", err)
	}

	// 2. Render AGENTS.md (expanding @<FILE_PATH> macros)
	expandedPrompt, err := RenderAgentSystemPrompt(wsDir, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to render system prompt for agent %s: %w", agentID, err)
	}

	// 3. Read MEMORY.md
	memoryContent, err := ReadMemoryFile(agentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read memory file for agent %s: %w", agentID, err)
	}

	// 4. Initialize LLM Model adapter
	var llmModel model.LLM
	if runtimeCfg.Endpoint != "" {
		llmModel = NewOpenAIModel(runtimeCfg)
	} else {
		geminiModel, err := CreateGeminiModel(context.Background(), runtimeCfg.Model, runtimeCfg.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create fallback model for %s: %w", agentID, err)
		}
		llmModel = geminiModel
	}

	// 5. Build ADK functiontools for agent
	adkToolsMap, _, err := BuildFolderAgentTools(agentDir)
	if err != nil {
		return nil, fmt.Errorf("failed to build agent tools: %w", err)
	}
	var toolsList []tool.Tool
	for _, t := range adkToolsMap {
		toolsList = append(toolsList, t)
	}

	if maxToolTurns <= 0 {
		maxToolTurns = 10
	}

	// 6. Construct ADK llmagent with agentID, expanded prompt instruction, maxToolTurns cap, model, and tools
	ag, err := BuildADKAgent(agentID, expandedPrompt, maxToolTurns, llmModel, toolsList...)
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
		MaxToolTurns:  maxToolTurns,
	}, nil
}

// GenerateTurn performs the agent generation turn for the current session using Google ADK runner.Runner.
// Uses FileSessionService to read and write session history directly to session.jsonl.
func (fa *FolderAgent) GenerateTurn(ctx context.Context) (string, error) {
	// 1. Check for context window compaction trigger before generating
	_, err := CheckAndCompactSession(ctx, fa.AgentDir, fa.RuntimeConfig, fa.SystemPrompt, fa.Model)
	if err != nil {
		// Log compaction warning, but continue execution if possible
		fmt.Fprintf(os.Stderr, "Warning: session compaction error: %v\n", err)
	}

	wsDir := filepath.Dir(fa.AgentDir)
	sessionSvc := NewFileSessionService(wsDir)

	r, err := runner.New(runner.Config{
		AppName:           "wackypub",
		Agent:             fa.ADKAgent,
		SessionService:    sessionSvc,
		AutoCreateSession: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create runner: %w", err)
	}

	var finalResponse string
	for event, err := range r.Run(ctx, "user", fa.AgentID, nil, agent.RunConfig{}) {
		if err != nil {
			return "", fmt.Errorf("runner execution error: %w", err)
		}
		if event != nil {
			text := ExtractTextFromEvent(event)
			if text != "" {
				finalResponse = text
			}
		}
	}

	if finalResponse == "" {
		return "", fmt.Errorf("received empty response from agent")
	}

	return finalResponse, nil
}

func executeTool(ctx context.Context, agentDir string, toolName string, toolPath string, args ExecToolArgs) (string, error) {
	cmdArgs := args.Args

	absToolPath, err := filepath.Abs(toolPath)
	if err != nil {
		absToolPath = toolPath
	}

	cmd := exec.CommandContext(ctx, absToolPath, cmdArgs...)
	cmd.Dir = agentDir
	cmd.Env = os.Environ()

	if len(args.Env) > 0 {
		for k, v := range args.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if args.StdinScratchpadID != nil {
		sp, err := ReadScratchpad(agentDir)
		if err != nil {
			return "", fmt.Errorf("failed to read scratchpad: %w", err)
		}
		stdinText, ok := sp[*args.StdinScratchpadID]
		if !ok {
			return "", fmt.Errorf("scratchpad %d has not been set - set_scratchpad must complete in an earlier turn before it can be used as stdin", *args.StdinScratchpadID)
		}
		cmd.Stdin = strings.NewReader(stdinText)
	} else if len(args.Args) > 0 || len(args.Env) > 0 {
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
		return "", fmt.Errorf("tool %s failed: %s", toolName, errStr)
	}

	out := stdout.String()
	if args.StdoutScratchpadID != nil {
		summary, err := SetScratchpad(agentDir, *args.StdoutScratchpadID, out)
		if err != nil {
			return "", fmt.Errorf("failed to write output to scratchpad %d: %w", *args.StdoutScratchpadID, err)
		}
		return summary, nil
	}

	out = strings.TrimSpace(out)
	if out == "" {
		out = "Command completed with no output."
	}
	return out, nil
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
