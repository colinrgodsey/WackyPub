package agent

import (
	"testing"
)

func TestSDKAddUserTurnAndReadSession(t *testing.T) {
	tempDir := t.TempDir()
	sdk := NewSDK(tempDir)

	agentID := "test_hero"

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
