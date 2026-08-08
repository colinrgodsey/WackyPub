package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	output := executeTool(context.Background(), agentDir, "echo_tool.sh", toolPath, args)

	if !strings.Contains(output, "Arg1: hello, Env: world") {
		t.Fatalf("unexpected tool output: %q", output)
	}
}

func TestExecuteTool_Failure(t *testing.T) {
	agentDir := t.TempDir()
	toolPath := filepath.Join(agentDir, "fail_tool.sh")
	script := "#!/bin/sh\necho \"something broke\" >&2\nexit 1\n"
	if err := os.WriteFile(toolPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fail tool script: %v", err)
	}

	output := executeTool(context.Background(), agentDir, "fail_tool.sh", toolPath, ExecToolArgs{})

	if !strings.Contains(output, "Error executing tool fail_tool.sh: something broke") {
		t.Fatalf("unexpected fail output: %q", output)
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

	// Should contain set_scratchpad, get_scratchpad, custom.sh
	if len(toolMap) != 3 {
		t.Errorf("expected 3 tools, got %d", len(toolMap))
	}
	if len(decls) != 3 {
		t.Errorf("expected 3 decls, got %d", len(decls))
	}
	if _, ok := toolMap["set_scratchpad"]; !ok {
		t.Errorf("missing set_scratchpad in toolMap")
	}
	if _, ok := toolMap["get_scratchpad"]; !ok {
		t.Errorf("missing get_scratchpad in toolMap")
	}
	if _, ok := toolMap["custom.sh"]; !ok {
		t.Errorf("missing custom.sh in toolMap")
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

	out := executeTool(context.Background(), relAgentDir, "tool.sh", toolPath, ExecToolArgs{})
	if !strings.Contains(out, "relative_ok") {
		t.Fatalf("expected 'relative_ok', got: %q", out)
	}
}
