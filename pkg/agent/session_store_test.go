package agent

import (
	"testing"
)

func TestReadWriteAppendSessionTurns(t *testing.T) {
	tempDir := t.TempDir()

	turns, err := ReadSessionTurns(tempDir)
	if err != nil {
		t.Fatalf("unexpected error reading non-existent session file: %v", err)
	}
	if len(turns) != 0 {
		t.Errorf("expected 0 turns, got %d", len(turns))
	}

	if err := AppendSessionTurn(tempDir, "user", "Hello agent"); err != nil {
		t.Fatalf("failed to append user turn: %v", err)
	}
	if err := AppendSessionTurn(tempDir, "assistant", "Hello user"); err != nil {
		t.Fatalf("failed to append assistant turn: %v", err)
	}

	turns, err = ReadSessionTurns(tempDir)
	if err != nil {
		t.Fatalf("failed to read appended session turns: %v", err)
	}

	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}

	if turns[0].Role != "user" || ContentText(turns[0]) != "Hello agent" {
		t.Errorf("turn 0 mismatch: %+v", turns[0])
	}
	if turns[1].Role != "assistant" || ContentText(turns[1]) != "Hello user" {
		t.Errorf("turn 1 mismatch: %+v", turns[1])
	}
}
