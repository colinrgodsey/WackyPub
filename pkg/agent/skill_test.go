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

func TestParseSkillFile(t *testing.T) {
	tmpDir := t.TempDir()
	skillPath := filepath.Join(tmpDir, "SKILL.md")

	content := `---
name: testing-guide
description: How to write Go unit tests
always_load: true
---
# Testing Guide
Always use t.Fatalf for fatal assertions.
`
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write SKILL.md: %v", err)
	}

	sk, err := ParseSkillFile(skillPath)
	if err != nil {
		t.Fatalf("ParseSkillFile failed: %v", err)
	}

	if sk.Name != "testing-guide" {
		t.Errorf("expected Name 'testing-guide', got %q", sk.Name)
	}
	if sk.Description != "How to write Go unit tests" {
		t.Errorf("expected Description 'How to write Go unit tests', got %q", sk.Description)
	}
	if !sk.AlwaysLoad {
		t.Errorf("expected AlwaysLoad true, got false")
	}
	if !strings.Contains(sk.Body, "# Testing Guide") {
		t.Errorf("expected Body to contain '# Testing Guide', got: %q", sk.Body)
	}
}

func TestDiscoverAgentSkills_AndAutoload(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, "bob")
	skillsDir := filepath.Join(agentDir, "skills")
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		t.Fatalf("failed to create skills dir: %v", err)
	}

	// 1. On-demand skill (always_load: false)
	sk1Dir := filepath.Join(skillsDir, "git-workflow")
	if err := os.MkdirAll(sk1Dir, 0755); err != nil {
		t.Fatalf("failed to create git-workflow dir: %v", err)
	}
	sk1Content := `---
name: git-workflow
description: Best practices for git commits
---
Use clear commit messages.
`
	if err := os.WriteFile(filepath.Join(sk1Dir, "SKILL.md"), []byte(sk1Content), 0644); err != nil {
		t.Fatalf("failed to write git-workflow SKILL.md: %v", err)
	}

	// 2. Always-loaded skill (always_load: true)
	sk2Dir := filepath.Join(skillsDir, "safety-rules")
	if err := os.MkdirAll(sk2Dir, 0755); err != nil {
		t.Fatalf("failed to create safety-rules dir: %v", err)
	}
	sk2Content := `---
name: safety-rules
description: Critical safety directives
always_load: true
---
Never run rm -rf /
`
	if err := os.WriteFile(filepath.Join(sk2Dir, "SKILL.md"), []byte(sk2Content), 0644); err != nil {
		t.Fatalf("failed to write safety-rules SKILL.md: %v", err)
	}

	skillsMap, onDemand, alwaysLoaded, err := DiscoverAgentSkills(agentDir)
	if err != nil {
		t.Fatalf("DiscoverAgentSkills failed: %v", err)
	}

	if len(skillsMap) != 2 {
		t.Fatalf("expected 2 total skills, got %d", len(skillsMap))
	}
	if len(onDemand) != 1 || onDemand[0].Name != "git-workflow" {
		t.Errorf("expected 1 on-demand skill 'git-workflow', got %v", onDemand)
	}
	if len(alwaysLoaded) != 1 || alwaysLoaded[0].Name != "safety-rules" {
		t.Errorf("expected 1 always-loaded skill 'safety-rules', got %v", alwaysLoaded)
	}

	// Test RenderAutoloadedSkills
	autoloadBlock, err := RenderAutoloadedSkills(agentDir)
	if err != nil {
		t.Fatalf("RenderAutoloadedSkills failed: %v", err)
	}

	if !strings.Contains(autoloadBlock, "<AUTOLOADED_SKILLS>") {
		t.Errorf("expected autoload block to contain <AUTOLOADED_SKILLS>, got: %s", autoloadBlock)
	}
	if !strings.Contains(autoloadBlock, `<SKILL name="safety-rules">`) {
		t.Errorf("expected autoload block to contain <SKILL name=\"safety-rules\">, got: %s", autoloadBlock)
	}
	if !strings.Contains(autoloadBlock, "Never run rm -rf /") {
		t.Errorf("expected autoload block to contain body, got: %s", autoloadBlock)
	}

	// Test RenderAgentSystemPrompt integrates autoload block
	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("System prompt Bob"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}

	prompt, err := RenderAgentSystemPrompt(wsDir, "bob")
	if err != nil {
		t.Fatalf("RenderAgentSystemPrompt failed: %v", err)
	}
	if !strings.Contains(prompt, "System prompt Bob") || !strings.Contains(prompt, "<AUTOLOADED_SKILLS>") {
		t.Errorf("RenderAgentSystemPrompt missing expected content: %s", prompt)
	}
}

type loadSkillTestModel struct {
	skillName string
}

func (m *loadSkillTestModel) Name() string { return "load-skill-test-model" }

func (m *loadSkillTestModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		res := &model.LLMResponse{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{
					{
						FunctionCall: &genai.FunctionCall{
							Name: "load_skill",
							Args: map[string]any{
								"name": m.skillName,
							},
						},
					},
				},
			},
		}
		yield(res, nil)
	}
}

func TestLoadSkillToolExecution(t *testing.T) {
	wsDir := t.TempDir()
	agentDir := filepath.Join(wsDir, "bob")
	skillsDir := filepath.Join(agentDir, "skills")
	skDir := filepath.Join(skillsDir, "debugging")
	if err := os.MkdirAll(skDir, 0755); err != nil {
		t.Fatalf("failed to create debugging skill dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("Prompt Bob"), 0644); err != nil {
		t.Fatalf("failed to write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "runtime.json"), []byte(`{"model":"dummy-model","endpoint":"http://localhost:1234/v1"}`), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	skContent := `---
name: debugging
description: Debugging steps
---
Step 1: Check logs.
`
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(skContent), 0644); err != nil {
		t.Fatalf("failed to write debugging SKILL.md: %v", err)
	}

	toolMap, decls, err := BuildFolderAgentTools(agentDir)
	if err != nil {
		t.Fatalf("BuildFolderAgentTools failed: %v", err)
	}

	// 6 tools: create_scratchpad, get_scratchpad, list_scratchpads, search_scratchpad, run_command, load_skill
	if len(toolMap) != 6 {
		t.Errorf("expected 6 tools, got %d", len(toolMap))
	}
	if len(decls) != 6 {
		t.Errorf("expected 6 decls, got %d", len(decls))
	}

	loadSkillTool, ok := toolMap["load_skill"]
	if !ok {
		t.Fatalf("missing load_skill tool in toolMap")
	}

	decler := loadSkillTool.(interface {
		Declaration() *genai.FunctionDeclaration
	})
	desc := decler.Declaration().Description
	if !strings.Contains(desc, "- debugging: Debugging steps") {
		t.Errorf("expected description to contain '- debugging: Debugging steps', got: %s", desc)
	}

	// Test loading valid skill via GenerateTurn
	fa, err := LoadFolderAgent(wsDir, "bob", 1)
	if err != nil {
		t.Fatalf("LoadFolderAgent failed: %v", err)
	}
	fa.Model = &loadSkillTestModel{skillName: "debugging"}

	toolsList := []tool.Tool{toolMap["create_scratchpad"], toolMap["get_scratchpad"], toolMap["list_scratchpads"], toolMap["run_command"], loadSkillTool}
	fa.ADKAgent, err = BuildADKAgent("bob", fa.SystemPrompt, 1, fa.Model, toolsList...)
	if err != nil {
		t.Fatalf("BuildADKAgent failed: %v", err)
	}

	uMsg := genai.NewContentFromText("load debugging skill", "user")
	if err := AppendSessionContent(agentDir, uMsg); err != nil {
		t.Fatalf("AppendSessionContent failed: %v", err)
	}

	_, _ = fa.GenerateTurn(context.Background())

	turns, err := ReadSessionTurns(agentDir)
	if err != nil {
		t.Fatalf("ReadSessionTurns failed: %v", err)
	}

	foundOutput := false
	for _, turn := range turns {
		if turn.Role == "user" {
			for _, part := range turn.Parts {
				if part.FunctionResponse != nil {
					respJSON, _ := json.Marshal(part.FunctionResponse.Response)
					if strings.Contains(string(respJSON), "Step 1: Check logs.") {
						foundOutput = true
					}
				}
			}
		}
	}
	if !foundOutput {
		t.Errorf("expected to find FunctionResponse containing 'Step 1: Check logs.' in session.jsonl")
	}
}

func TestParseSkillFile_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	skillPath := filepath.Join(tmpDir, "SKILL.md")

	content := `---
name: [invalid yaml syntax
description: {{bad syntax
---
# Invalid Frontmatter
`
	if err := os.WriteFile(skillPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write invalid SKILL.md: %v", err)
	}

	_, err := ParseSkillFile(skillPath)
	if err == nil {
		t.Fatalf("expected error parsing invalid YAML frontmatter, got nil")
	}
	if !strings.Contains(err.Error(), "failed to parse YAML frontmatter") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDiscoverAgentSkills_Shadowing(t *testing.T) {
	agentDir := t.TempDir()
	skillsDir := filepath.Join(agentDir, "skills")

	dir1 := filepath.Join(skillsDir, "a", "my-skill")
	dir2 := filepath.Join(skillsDir, "b", "my-skill")
	if err := os.MkdirAll(dir1, 0755); err != nil {
		t.Fatalf("failed to create dir1: %v", err)
	}
	if err := os.MkdirAll(dir2, 0755); err != nil {
		t.Fatalf("failed to create dir2: %v", err)
	}

	sk1 := `---
name: my-skill
description: Skill A
---
Body A
`
	sk2 := `---
name: my-skill
description: Skill B
---
Body B
`
	if err := os.WriteFile(filepath.Join(dir1, "SKILL.md"), []byte(sk1), 0644); err != nil {
		t.Fatalf("failed to write sk1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "SKILL.md"), []byte(sk2), 0644); err != nil {
		t.Fatalf("failed to write sk2: %v", err)
	}

	_, _, _, shadowed, err := DiscoverAgentSkillsMap(agentDir)
	if err != nil {
		t.Fatalf("DiscoverAgentSkillsMap failed: %v", err)
	}

	if len(shadowed) != 1 {
		t.Fatalf("expected 1 shadowed warning, got %d: %v", len(shadowed), shadowed)
	}
	if !strings.Contains(shadowed[0], "skill \"my-skill\"") || !strings.Contains(shadowed[0], "is shadowed by") {
		t.Errorf("unexpected shadowed warning message: %s", shadowed[0])
	}
}
