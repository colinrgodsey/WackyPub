package agent

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

func TestExecuteTool(t *testing.T) {
	agentDir := t.TempDir()
	toolPath := filepath.Join(agentDir, "echo_tool.sh")
	script := "#!/bin/sh\necho \"Arg1: $1, Env: $TEST_VAR\"\n"
	if err := os.WriteFile(toolPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write tool script: %v", err)
	}

	args := ExecToolArgs{
		Args: []string{"hello"},
		Env:  map[string]string{"TEST_VAR": "world"},
	}
	output, err := executeTool(context.Background(), agentDir, "echo_tool.sh", toolPath, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(output, "Arg1: hello, Env: world") {
		t.Fatalf("unexpected tool output: %q", output)
	}
}

func TestExecuteTool_SymlinkResolution(t *testing.T) {
	tmpDir := t.TempDir()
	targetDir := filepath.Join(tmpDir, "real_target")
	agentDir := filepath.Join(tmpDir, "agent")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(agentDir, "tools"), 0755); err != nil {
		t.Fatalf("failed to create agent tools dir: %v", err)
	}

	// Script uses dirname $0 to echo where it thinks it is located
	realScriptPath := filepath.Join(targetDir, "script.sh")
	scriptContent := "#!/bin/sh\nDIR=$(dirname \"$0\")\necho \"DIR=$DIR\"\n"
	if err := os.WriteFile(realScriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	// Create symlink inside agent tools directory pointing to real script
	symlinkPath := filepath.Join(agentDir, "tools", "script_link.sh")
	if err := os.Symlink(realScriptPath, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	out, err := executeTool(context.Background(), agentDir, "script_link.sh", symlinkPath, ExecToolArgs{})
	if err != nil {
		t.Fatalf("executeTool failed: %v", err)
	}

	realDir, _ := filepath.EvalSymlinks(targetDir)
	if !strings.Contains(out, "DIR="+realDir) {
		t.Fatalf("expected DIR=%s in output (evaluating symlink), got: %s", realDir, out)
	}
}

func TestExecuteTool_Failure(t *testing.T) {
	agentDir := t.TempDir()
	toolPath := filepath.Join(agentDir, "fail_tool.sh")
	script := "#!/bin/sh\necho \"something broke\" >&2\nexit 1\n"
	if err := os.WriteFile(toolPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fail tool script: %v", err)
	}

	_, err := executeTool(context.Background(), agentDir, "fail_tool.sh", toolPath, ExecToolArgs{})

	if err == nil {
		t.Fatalf("expected an error, got nil")
	}
	if !strings.Contains(err.Error(), "tool fail_tool.sh failed: something broke") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestBuildFolderAgentTools(t *testing.T) {
	agentDir := t.TempDir()
	toolsDir := filepath.Join(agentDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}
	shPath := filepath.Join(toolsDir, "custom.sh")
	if err := os.WriteFile(shPath, []byte("#!/bin/sh\necho custom_out"), 0755); err != nil {
		t.Fatalf("failed to write custom.sh: %v", err)
	}

	toolMap, decls, err := BuildFolderAgentTools(agentDir)
	if err != nil {
		t.Fatalf("BuildFolderAgentTools failed: %v", err)
	}

	// Should contain create_scratchpad, get_scratchpad, list_scratchpads, search_scratchpad, delete_scratchpad, run_command, load_skill (7 tools)
	if len(toolMap) != 7 {
		t.Errorf("expected 7 tools, got %d", len(toolMap))
	}
	if len(decls) != 7 {
		t.Errorf("expected 7 decls, got %d", len(decls))
	}
	if _, ok := toolMap["create_scratchpad"]; !ok {
		t.Errorf("missing create_scratchpad in toolMap")
	}
	if _, ok := toolMap["get_scratchpad"]; !ok {
		t.Errorf("missing get_scratchpad in toolMap")
	}
	runCmd, ok := toolMap["run_command"]
	if !ok {
		t.Fatalf("missing run_command in toolMap")
	}

	decler, ok := runCmd.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	if !ok {
		t.Fatalf("run_command does not implement Declaration()")
	}

	decl := decler.Declaration()
	if !strings.Contains(decl.Description, "Available commands: custom.sh.") {
		t.Errorf("expected description to list custom.sh, got: %s", decl.Description)
	}
	if !strings.Contains(decl.Description, "Usage Guidance:") {
		t.Errorf("expected description to contain Usage Guidance, got: %s", decl.Description)
	}
}

func TestDiscoverAgentTools_SymlinkToolpack(t *testing.T) {
	tmpDir := t.TempDir()

	// External toolpack directory with 3 executable files
	toolpackDir := filepath.Join(tmpDir, "external_toolpack")
	if err := os.MkdirAll(toolpackDir, 0755); err != nil {
		t.Fatalf("failed to create external toolpack: %v", err)
	}
	for _, name := range []string{"cat", "ls", "man"} {
		path := filepath.Join(toolpackDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+name), 0755); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	// Agent directory with tools/ folder containing a symlink to external_toolpack
	agentDir := filepath.Join(tmpDir, "agent_bob")
	toolsDir := filepath.Join(agentDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create agent tools dir: %v", err)
	}

	symlinkPath := filepath.Join(toolsDir, "read-only-fs")
	if err := os.Symlink(toolpackDir, symlinkPath); err != nil {
		t.Fatalf("failed to create toolpack symlink: %v", err)
	}

	toolMap, discovered, shadowed, err := DiscoverAgentToolsMap(agentDir)
	if err != nil {
		t.Fatalf("DiscoverAgentToolsMap failed: %v", err)
	}

	if len(shadowed) > 0 {
		t.Errorf("expected 0 shadowed warnings, got %v", shadowed)
	}

	// Must discover cat, ls, man (3 tools), NOT "read-only-fs"
	expected := []string{"cat", "ls", "man"}
	if len(discovered) != len(expected) {
		t.Fatalf("expected discovered tools %v, got %v", expected, discovered)
	}
	for i, name := range expected {
		if discovered[i] != name {
			t.Errorf("discovered[%d] = %q, expected %q", i, discovered[i], name)
		}
		if _, exists := toolMap[name]; !exists {
			t.Errorf("toolMap missing entry for %q", name)
		}
	}

	// Verify "read-only-fs" directory symlink itself is NOT registered as a tool
	if _, exists := toolMap["read-only-fs"]; exists {
		t.Errorf("directory symlink 'read-only-fs' was wrongly registered as an executable tool")
	}
}

func TestExecuteTool_RelativePath(t *testing.T) {
	// Setup subdirectories inside current CWD to test relative paths
	relAgentDir := filepath.Join("test_scratch", "agent_rel")
	if err := os.MkdirAll(relAgentDir, 0755); err != nil {
		t.Fatalf("failed to create rel agent dir: %v", err)
	}
	defer os.RemoveAll("test_scratch")

	toolPath := filepath.Join(relAgentDir, "tool.sh")
	if err := os.WriteFile(toolPath, []byte("#!/bin/sh\necho relative_ok"), 0755); err != nil {
		t.Fatalf("failed to write tool.sh: %v", err)
	}

	out, err := executeTool(context.Background(), relAgentDir, "tool.sh", toolPath, ExecToolArgs{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "relative_ok") {
		t.Fatalf("expected 'relative_ok', got: %q", out)
	}
}

type runCmdTestModel struct {
	command string
	args    []string
}

func (m *runCmdTestModel) Name() string { return "run-cmd-test-model" }

func (m *runCmdTestModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		res := &model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{
						FunctionCall: &genai.FunctionCall{
							Name: "run_command",
							Args: map[string]any{
								"command": m.command,
								"args":    m.args,
							},
						},
					},
				},
			},
		}
		yield(res, nil)
	}
}

func TestRunCommandToolValidationAndExecution(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, "bob")
	toolsDir := filepath.Join(agentDir, "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatalf("failed to create tools dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("Prompt Bob"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(`{"model":"dummy-model","endpoint":"http://localhost:1234/v1"}`), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	// Create 2 discovered tools: echo.sh and greet.sh
	if err := os.WriteFile(filepath.Join(toolsDir, "echo.sh"), []byte("#!/bin/sh\necho \"echo: $1\""), 0755); err != nil {
		t.Fatalf("failed to write echo.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(toolsDir, "greet.sh"), []byte("#!/bin/sh\necho \"greet: $1\""), 0755); err != nil {
		t.Fatalf("failed to write greet.sh: %v", err)
	}

	toolMap, decls, err := BuildFolderAgentTools(agentDir)
	if err != nil {
		t.Fatalf("BuildFolderAgentTools failed: %v", err)
	}

	// 7 tools in toolMap: create_scratchpad, get_scratchpad, list_scratchpads, search_scratchpad, delete_scratchpad, run_command, load_skill
	if len(toolMap) != 7 {
		t.Fatalf("expected 7 tools in toolMap, got %d", len(toolMap))
	}
	if len(decls) != 7 {
		t.Fatalf("expected 7 decls, got %d", len(decls))
	}

	runCmdTool, ok := toolMap["run_command"]
	if !ok {
		t.Fatalf("missing run_command tool in toolMap")
	}

	// Verify command list in description is alphabetically sorted: echo.sh, greet.sh
	decler := runCmdTool.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	desc := decler.Declaration().Description
	if !strings.Contains(desc, "Available commands: echo.sh, greet.sh.") {
		t.Errorf("expected sorted commands list in description, got: %s", desc)
	}

	// Test executing valid command 'echo.sh' via FolderAgent and ADK runner
	fa, err := LoadFolderAgent(wsDir, "bob", 1)
	if err != nil {
		t.Fatalf("LoadFolderAgent failed: %v", err)
	}
	fa.Model = &runCmdTestModel{command: "echo.sh", args: []string{"world"}}

	toolsList := []tool.Tool{toolMap["create_scratchpad"], toolMap["get_scratchpad"], toolMap["list_scratchpads"], runCmdTool}
	fa.ADKAgent, err = BuildADKAgent("bob", fa.SystemPrompt, 1, fa.Model, toolsList...)
	if err != nil {
		t.Fatalf("BuildADKAgent failed: %v", err)
	}

	// Add user turn to session.jsonl
	uMsg := genai.NewContentFromText("run echo", "user")
	if err := AppendSessionContent(agentDir, uMsg); err != nil {
		t.Fatalf("AppendSessionContent failed: %v", err)
	}

	// Run turn (expect max tool turns limit error because test model constantly returns FunctionCall)
	_, _ = fa.GenerateTurn(context.Background())

	turns, err := ReadSessionTurns(agentDir)
	if err != nil {
		t.Fatalf("ReadSessionTurns failed: %v", err)
	}

	// session.jsonl should contain FunctionResponse with output "echo: world"
	foundOutput := false
	for _, turn := range turns {
		if turn.Role == "user" {
			for _, part := range turn.Parts {
				if part.FunctionResponse != nil {
					respJSON, _ := json.Marshal(part.FunctionResponse.Response)
					if strings.Contains(string(respJSON), "echo: world") {
						foundOutput = true
					}
				}
			}
		}
	}
	if !foundOutput {
		t.Errorf("expected to find FunctionResponse containing 'echo: world' in session.jsonl")
	}
}

func TestSearchScratchpad(t *testing.T) {
	agentDir := t.TempDir()

	text := "Line 1: Alpha\nLine 2: Beta\nLine 3: ALPHA\nLine 4: Gamma\nLine 5: alpha delta\n"
	entry, err := CreateScratchpad(agentDir, text, "test")
	if err != nil {
		t.Fatalf("CreateScratchpad failed: %v", err)
	}

	// 1. Literal case-sensitive search for "Alpha"
	resCase, err := SearchScratchpad(agentDir, entry.ID, "Alpha", nil, false, 50)
	if err != nil {
		t.Fatalf("SearchScratchpad case-sensitive failed: %v", err)
	}
	if resCase.TotalMatches != 1 {
		t.Errorf("expected 1 match for 'Alpha', got %d", resCase.TotalMatches)
	}
	if len(resCase.Matches) > 0 {
		if resCase.Matches[0].Line != 1 || resCase.Matches[0].SkipLines != 0 {
			t.Errorf("expected match line 1, skip_lines 0, got line %d, skip_lines %d", resCase.Matches[0].Line, resCase.Matches[0].SkipLines)
		}
	}

	// 2. Case-insensitive search for "alpha"
	caseSensFalse := false
	resNoCase, err := SearchScratchpad(agentDir, entry.ID, "alpha", &caseSensFalse, false, 50)
	if err != nil {
		t.Fatalf("SearchScratchpad case-insensitive failed: %v", err)
	}
	if resNoCase.TotalMatches != 3 {
		t.Errorf("expected 3 matches for case-insensitive 'alpha', got %d", resNoCase.TotalMatches)
	}

	// 3. Regex search for "Alpha|Beta"
	resRegex, err := SearchScratchpad(agentDir, entry.ID, "Alpha|Beta", nil, true, 50)
	if err != nil {
		t.Fatalf("SearchScratchpad regex failed: %v", err)
	}
	if resRegex.TotalMatches != 2 {
		t.Errorf("expected 2 matches for regex 'Alpha|Beta', got %d", resRegex.TotalMatches)
	}

	// 4. Max results capping
	resCap, err := SearchScratchpad(agentDir, entry.ID, "alpha", &caseSensFalse, false, 2)
	if err != nil {
		t.Fatalf("SearchScratchpad capped failed: %v", err)
	}
	if resCap.TotalMatches != 3 {
		t.Errorf("expected total_matches 3, got %d", resCap.TotalMatches)
	}
	if len(resCap.Matches) != 2 {
		t.Errorf("expected len(Matches) 2 due to cap, got %d", len(resCap.Matches))
	}
}
