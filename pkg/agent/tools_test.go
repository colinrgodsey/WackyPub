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

	args := map[string]any{
		"args": []any{"hello"},
		"env":  map[string]any{"TEST_VAR": "world"},
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

	output := executeTool(context.Background(), agentDir, "fail_tool.sh", toolPath, nil)

	if !strings.Contains(output, "Error executing tool fail_tool.sh: something broke") {
		t.Fatalf("unexpected fail output: %q", output)
	}
}
