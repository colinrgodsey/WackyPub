package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchpadPersistence(t *testing.T) {
	agentDir := t.TempDir()

	msg, err := SetScratchpad(agentDir, 1, "hello scratchpad")
	if err != nil {
		t.Fatalf("SetScratchpad failed: %v", err)
	}
	if !strings.Contains(msg, "Scratchpad 1 updated") {
		t.Fatalf("unexpected summary: %q", msg)
	}

	val, err := GetScratchpad(agentDir, 1)
	if err != nil {
		t.Fatalf("GetScratchpad failed: %v", err)
	}
	if val != "hello scratchpad" {
		t.Fatalf("expected 'hello scratchpad', got %q", val)
	}

	empty, err := GetScratchpad(agentDir, 99)
	if err != nil {
		t.Fatalf("GetScratchpad for missing id failed: %v", err)
	}
	if !strings.Contains(empty, "empty") {
		t.Fatalf("expected empty slot message, got %q", empty)
	}
}

func TestExecuteTool_ScratchpadRedirection(t *testing.T) {
	agentDir := t.TempDir()

	// Set initial scratchpad entry 10
	if _, err := SetScratchpad(agentDir, 10, "input-from-scratchpad"); err != nil {
		t.Fatalf("failed to seed scratchpad: %v", err)
	}

	// Tool script reads stdin and prints output
	toolPath := filepath.Join(agentDir, "pipe_tool.sh")
	script := "#!/bin/sh\nread input\necho \"Processed: $input\"\n"
	if err := os.WriteFile(toolPath, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write tool script: %v", err)
	}

	// Execute tool with stdin_scratchpad_id=10 and stdout_scratchpad_id=20
	stdinID := 10
	stdoutID := 20
	args := ExecToolArgs{
		StdinScratchpadID:  &stdinID,
		StdoutScratchpadID: &stdoutID,
	}

	summary := executeTool(context.Background(), agentDir, "pipe_tool.sh", toolPath, args)
	if !strings.Contains(summary, "Scratchpad 20 updated") {
		t.Fatalf("unexpected summary: %q", summary)
	}

	// Verify scratchpad 20 output
	outputVal, err := GetScratchpad(agentDir, 20)
	if err != nil {
		t.Fatalf("GetScratchpad 20 failed: %v", err)
	}
	if !strings.Contains(outputVal, "Processed: input-from-scratchpad") {
		t.Fatalf("unexpected scratchpad 20 content: %q", outputVal)
	}
}
