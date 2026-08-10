package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSDKAddUserTurnAndReadSession(t *testing.T) {
	tempDir := t.TempDir()
	sdk := NewSDK(tempDir)

	agentID := "test_hero"
	agentDir := sdk.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, AllowedAgentsFile), []byte("test_hero\n"), 0644); err != nil {
		t.Fatalf("failed to write allowed agents: %v", err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatalf("failed to chdir to agentDir: %v", err)
	}
	defer os.Chdir(origCwd)

	if err := sdk.AddUserTurn(agentID, "What is your quest?"); err != nil {
		t.Fatalf("failed to add user turn via SDK: %v", err)
	}

	turns, err := sdk.ReadSession(agentID)
	if err != nil {
		t.Fatalf("failed to read session via SDK: %v", err)
	}

	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}

	if turns[0].Role != "user" || ContentText(turns[0]) != "What is your quest?" {
		t.Errorf("turn contents mismatch: %+v", turns[0])
	}
}

func TestSDKReadMemory(t *testing.T) {
	tempDir := t.TempDir()
	sdk := NewSDK(tempDir)

	agentID := "test_wizard"
	agentDir := sdk.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, AllowedAgentsFile), []byte("test_wizard\n"), 0644); err != nil {
		t.Fatalf("failed to write allowed agents: %v", err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get cwd: %v", err)
	}
	if err := os.Chdir(agentDir); err != nil {
		t.Fatalf("failed to chdir to agentDir: %v", err)
	}
	defer os.Chdir(origCwd)

	memory, err := sdk.ReadMemory(agentID)
	if err != nil {
		t.Fatalf("unexpected error reading non-existent memory: %v", err)
	}
	if memory != "" {
		t.Errorf("expected empty memory, got %s", memory)
	}

	if err := WriteMemoryFile(agentDir, "Fact: Wizard knows fireball."); err != nil {
		t.Fatalf("failed writing memory: %v", err)
	}

	memory, err = sdk.ReadMemory(agentID)
	if err != nil {
		t.Fatalf("failed reading memory via SDK: %v", err)
	}
	if memory != "Fact: Wizard knows fireball." {
		t.Errorf("memory content mismatch: %s", memory)
	}
}
