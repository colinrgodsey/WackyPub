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

type CreateScratchpadArgs struct {
	Text string `json:"text" jsonschema_description:"Text content to store in a persistent scratchpad entry"`
}

type CreateScratchpadResult struct {
	ID   string `json:"id"`
	Size int    `json:"size"`
}

type GetScratchpadArgs struct {
	ID        string `json:"id" jsonschema_description:"4-character ID of the scratchpad entry to read"`
	SkipLines *int   `json:"skip_lines,omitempty" jsonschema_description:"Optional number of lines to skip from the beginning"`
	NumLines  *int   `json:"num_lines,omitempty" jsonschema_description:"Optional maximum number of lines to retrieve"`
}

type GetScratchpadResult struct {
	Output string `json:"output"`
}

type ListScratchpadsArgs struct{}

type ListScratchpadsResult struct {
	Entries []ScratchpadItem `json:"entries"`
	Count   int              `json:"count"`
	Cap     int              `json:"cap"`
}

type SearchScratchpadArgs struct {
	ID            string `json:"id" jsonschema_description:"Required scratchpad entry ID to search"`
	Query         string `json:"query" jsonschema_description:"Search query string"`
	CaseSensitive *bool  `json:"case_sensitive,omitempty" jsonschema_description:"Whether search is case-sensitive (default: true)"`
	Regex         bool   `json:"regex,omitempty" jsonschema_description:"Opt-in to treat query as a regular expression (default: false)"`
	MaxResults    int    `json:"max_results,omitempty" jsonschema_description:"Maximum number of matching lines to return (default: 50)"`
}

type ExecToolArgs struct {
	Args  []string          `json:"args,omitempty" jsonschema_description:"List of CLI command line arguments passed positionally to the tool (supports inline <SCRATCHPAD_DATA id=\"X\" /> macros)"`
	Env   map[string]string `json:"env,omitempty" jsonschema_description:"Key-value object map of environment variables to set for the tool invocation (not macro-expanded)"`
	Stdin string            `json:"stdin,omitempty" jsonschema_description:"Optional stdin template string to pipe into the command (supports inline <SCRATCHPAD_DATA id=\"X\" /> macros)"`
}

type RunCommandArgs struct {
	Command string            `json:"command" jsonschema_description:"Name of the command executable to run from the discovered tools list"`
	Args    []string          `json:"args,omitempty" jsonschema_description:"List of CLI command line arguments passed positionally to the tool (supports inline <SCRATCHPAD_DATA id=\"X\" /> macros)"`
	Env     map[string]string `json:"env,omitempty" jsonschema_description:"Key-value object map of environment variables to set for the tool invocation (not macro-expanded)"`
	Stdin   string            `json:"stdin,omitempty" jsonschema_description:"Optional stdin template string to pipe into the command (supports inline <SCRATCHPAD_DATA id=\"X\" /> macros)"`
}

type RunCommandResult struct {
	Output string `json:"output"`
}

type LoadSkillArgs struct {
	Name string `json:"name" jsonschema_description:"Name of the skill to load into conversation context"`
}

type LoadSkillResult struct {
	Output string `json:"output"`
}

// BuildFolderAgentTools constructs ADK functiontool instances for built-in tools (create_scratchpad, get_scratchpad, list_scratchpads)
// and a single generic run_command tool covering executables discovered under <agent_dir>/tools/.
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

	// 1. create_scratchpad
	createTool, err := functiontool.New(functiontool.Config{
		Name:        "create_scratchpad",
		Description: "Store a text payload in a persistent, session-level scratchpad entry. Returns a freshly generated 4-character ID.",
	}, func(ctx agent.Context, args CreateScratchpadArgs) (CreateScratchpadResult, error) {
		entry, err := CreateScratchpad(agentDir, args.Text, "create_scratchpad")
		if err != nil {
			return CreateScratchpadResult{}, fmt.Errorf("failed to create scratchpad entry: %w", err)
		}
		return CreateScratchpadResult{
			ID:   entry.ID,
			Size: entry.Size,
		}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create create_scratchpad tool: %w", err)
	}
	addTool(createTool)

	// 2. get_scratchpad
	getTool, err := functiontool.New(functiontool.Config{
		Name:        "get_scratchpad",
		Description: "Retrieve stored text from a scratchpad entry by ID, optionally paginated by line range.",
	}, func(ctx agent.Context, args GetScratchpadArgs) (GetScratchpadResult, error) {
		out, err := GetScratchpad(agentDir, args.ID, args.SkipLines, args.NumLines)
		if err != nil {
			return GetScratchpadResult{}, err
		}
		return GetScratchpadResult{Output: out}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create get_scratchpad tool: %w", err)
	}
	addTool(getTool)

	// 3. list_scratchpads
	listTool, err := functiontool.New(functiontool.Config{
		Name:        "list_scratchpads",
		Description: "List metadata for all currently-live scratchpad entries (ID, size, created_by), ordered oldest-first, and current capacity usage.",
	}, func(ctx agent.Context, args ListScratchpadsArgs) (ListScratchpadsResult, error) {
		items, count, capVal, err := ListScratchpads(agentDir)
		if err != nil {
			return ListScratchpadsResult{}, fmt.Errorf("failed to list scratchpads: %w", err)
		}
		return ListScratchpadsResult{
			Entries: items,
			Count:   count,
			Cap:     capVal,
		}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create list_scratchpads tool: %w", err)
	}
	addTool(listTool)

	// 4. search_scratchpad
	searchTool, err := functiontool.New(functiontool.Config{
		Name:        "search_scratchpad",
		Description: "Search a specific scratchpad entry by ID for matching lines. Returns 1-indexed line numbers and precomputed skip_lines for get_scratchpad pagination.",
	}, func(ctx agent.Context, args SearchScratchpadArgs) (*SearchScratchpadResult, error) {
		return SearchScratchpad(agentDir, args.ID, args.Query, args.CaseSensitive, args.Regex, args.MaxResults)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create search_scratchpad tool: %w", err)
	}
	addTool(searchTool)

	// 3. Single generic run_command tool covering all discovered executables
	discoveredMap, discoveredNames, _, err := DiscoverAgentToolsMap(agentDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover agent tools: %w", err)
	}

	var cmdListStr string
	if len(discoveredNames) > 0 {
		cmdListStr = strings.Join(discoveredNames, ", ")
	} else {
		cmdListStr = "none"
	}

	runCmdDesc := fmt.Sprintf(
		"Execute a command binary from tools/. Available commands: %s.\n\n"+
			"Usage Guidance:\n"+
			"- The working directory is always the agent's own directory - there's no way to cd elsewhere, since commands don't chain.\n"+
			"- args entries are passed as literal argv elements, not shell-parsed - no quoting or escaping needed for spaces/special characters.\n"+
			"- The agent's scratchpad may already contain the data it needs - check before running a command to regenerate something already available.\n"+
			"- Running a command with no arguments or --help is a legitimate way to learn what it is, how to use it, and what arguments it takes.\n"+
			"- args entries and the stdin field both support inline <SCRATCHPAD_DATA id=\"X\" skip_lines=\"N\" num_lines=\"M\" /> macros (skip_lines/num_lines optional) - this substitutes the referenced scratchpad entry's content directly, without you ever having to read or repaste it yourself. Large stdout/stderr from this same tool is automatically captured into a fresh scratchpad entry and returned the same way, so it can be piped straight into another command's args/stdin this way.",
		cmdListStr,
	)

	runCmdTool, err := functiontool.New(functiontool.Config{
		Name:        "run_command",
		Description: runCmdDesc,
	}, func(ctx agent.Context, args RunCommandArgs) (RunCommandResult, error) {
		toolPath, ok := discoveredMap[args.Command]
		if !ok {
			return RunCommandResult{}, fmt.Errorf("unknown command %q. Available commands: %s", args.Command, cmdListStr)
		}

		execArgs := ExecToolArgs{
			Args:  args.Args,
			Env:   args.Env,
			Stdin: args.Stdin,
		}
		out, err := executeTool(ctx, agentDir, args.Command, toolPath, execArgs)
		if err != nil {
			return RunCommandResult{}, err
		}
		return RunCommandResult{Output: out}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create run_command tool: %w", err)
	}
	addTool(runCmdTool)

	// 4. load_skill tool for on-demand skills
	skillsMap, onDemandSkills, _, err := DiscoverAgentSkills(agentDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to discover agent skills: %w", err)
	}

	var skillLines []string
	for _, sk := range onDemandSkills {
		skillLines = append(skillLines, fmt.Sprintf("- %s: %s", sk.Name, sk.Description))
	}

	var skillListStr string
	if len(skillLines) > 0 {
		skillListStr = strings.Join(skillLines, "\n")
	} else {
		skillListStr = "none"
	}

	loadSkillDesc := fmt.Sprintf(
		"Load pre-written distilled guidance and instructions for a specific skill into conversation context.\n\n"+
			"Available skills:\n%s",
		skillListStr,
	)

	loadSkillTool, err := functiontool.New(functiontool.Config{
		Name:        "load_skill",
		Description: loadSkillDesc,
	}, func(ctx agent.Context, args LoadSkillArgs) (LoadSkillResult, error) {
		sk, ok := skillsMap[args.Name]
		if !ok || sk.AlwaysLoad {
			var availStr string
			if len(skillLines) > 0 {
				availStr = strings.Join(skillLines, "\n")
			} else {
				availStr = "none"
			}
			return LoadSkillResult{}, fmt.Errorf("unknown skill %q. Available skills:\n%s", args.Name, availStr)
		}
		return LoadSkillResult{Output: sk.Body}, nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create load_skill tool: %w", err)
	}
	addTool(loadSkillTool)

	return toolMap, decls, nil
}

func executeTool(ctx context.Context, agentDir string, toolName string, toolPath string, args ExecToolArgs) (string, error) {
	cmdArgs := make([]string, len(args.Args))
	for i, rawArg := range args.Args {
		expanded, err := ExpandScratchpadMacros(agentDir, rawArg)
		if err != nil {
			return "", err
		}
		if len(expanded) > MaxExpandedArgBytes {
			return "", fmt.Errorf("expanded argument exceeds 500000 bytes (was %d) - use stdin/stdout scratchpad redirection instead", len(expanded))
		}
		cmdArgs[i] = expanded
	}

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

	if args.Stdin != "" {
		expandedStdin, err := ExpandScratchpadMacros(agentDir, args.Stdin)
		if err != nil {
			return "", err
		}
		cmd.Stdin = strings.NewReader(expandedStdin)
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

	stdoutBytes := stdout.Bytes()
	stderrBytes := stderr.Bytes()

	var stdoutBlock string
	if len(stdoutBytes) > ScratchpadOutputThreshold {
		entry, err := CreateScratchpad(agentDir, string(stdoutBytes), "run_command")
		if err != nil {
			return "", fmt.Errorf("failed to create stdout scratchpad entry: %w", err)
		}
		stdoutBlock = fmt.Sprintf("<STDOUT><SCRATCHPAD_DATA id=%q /></STDOUT>", entry.ID)
	} else if len(stdoutBytes) > 0 {
		stdoutBlock = fmt.Sprintf("<STDOUT>%s</STDOUT>", string(stdoutBytes))
	} else {
		stdoutBlock = "<STDOUT></STDOUT>"
	}

	var stderrBlock string
	if len(stderrBytes) > ScratchpadOutputThreshold {
		entry, err := CreateScratchpad(agentDir, string(stderrBytes), "run_command")
		if err != nil {
			return "", fmt.Errorf("failed to create stderr scratchpad entry: %w", err)
		}
		stderrBlock = fmt.Sprintf("<STDERR><SCRATCHPAD_DATA id=%q /></STDERR>", entry.ID)
	} else if len(stderrBytes) > 0 {
		stderrBlock = fmt.Sprintf("<STDERR>%s</STDERR>", string(stderrBytes))
	}

	output := stdoutBlock + stderrBlock
	return output, nil
} // FolderAgent encapsulates an agent loaded from a folder environment (<ws_dir>/<agent_id>).
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
	switch runtimeCfg.Provider {
	case "anthropic":
		llmModel = NewAnthropicModel(runtimeCfg)
	case "openai", "openai-compatible":
		llmModel = NewOpenAIModel(runtimeCfg)
	case "gemini":
		geminiModel, err := CreateGeminiModel(context.Background(), runtimeCfg.Model, runtimeCfg.APIKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create Gemini model for %s: %w", agentID, err)
		}
		llmModel = geminiModel
	default:
		return nil, fmt.Errorf("unsupported provider %q in runtime.json for agent %s (supported: openai, gemini, anthropic)", runtimeCfg.Provider, agentID)
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
		maxToolTurns = DefaultMaxToolTurns
	}

	// 6. Construct ADK llmagent with agentID, expanded prompt instruction, maxToolTurns cap, runtimeCfg, model, and tools
	ag, err := BuildADKAgentWithConfig(agentID, expandedPrompt, maxToolTurns, runtimeCfg, llmModel, toolsList...)
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
	// 0. The session must already end on a user turn - generating against a
	// session that doesn't (empty, or already ends on a model turn) hands
	// the model no new input to react to, which just produces a confused
	// response. AddAndGenerateTurn (the "prompt" command) always satisfies
	// this itself by appending a user turn first.
	turns, err := ReadSessionTurns(fa.AgentDir)
	if err != nil {
		return "", fmt.Errorf("failed to read session turns: %w", err)
	}
	if len(turns) == 0 || turns[len(turns)-1].Role != "user" {
		return "", fmt.Errorf("cannot generate: session for agent %q does not end on a user turn - add one first (\"wackypub agent add\") or use \"wackypub agent prompt\" to do both in one call", fa.AgentID)
	}

	// 1. Check for context window compaction trigger before generating
	_, err = CheckAndCompactSession(ctx, fa.AgentDir, fa.RuntimeConfig, fa.SystemPrompt, fa.Model)
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
