package agent

import (
	"testing"

	"google.golang.org/genai"
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

func TestMergeConsecutiveUserTurns(t *testing.T) {
	text := func(role, s string) *genai.Content {
		return genai.NewContentFromText(s, genai.Role(role))
	}

	t.Run("empty input", func(t *testing.T) {
		got := MergeConsecutiveUserTurns(nil)
		if len(got) != 0 {
			t.Errorf("expected 0 turns, got %d", len(got))
		}
	})

	t.Run("no merge needed (already alternating)", func(t *testing.T) {
		in := []*genai.Content{text("user", "a"), text("model", "b"), text("user", "c")}
		got := MergeConsecutiveUserTurns(in)
		if len(got) != 3 {
			t.Fatalf("expected 3 turns, got %d", len(got))
		}
		for i, c := range got {
			if c != in[i] {
				t.Errorf("turn %d: expected untouched original pointer", i)
			}
		}
	})

	t.Run("merges a run of consecutive user turns", func(t *testing.T) {
		in := []*genai.Content{
			text("user", "system+memory turn"),
			text("user", "first real message"),
			text("model", "assistant reply"),
		}
		got := MergeConsecutiveUserTurns(in)
		if len(got) != 2 {
			t.Fatalf("expected 2 turns, got %d", len(got))
		}
		if got[0].Role != "user" || len(got[0].Parts) != 2 {
			t.Fatalf("expected merged user turn with 2 parts, got %+v", got[0])
		}
		if got[0].Parts[0].Text != "system+memory turn" || got[0].Parts[1].Text != "first real message" {
			t.Errorf("merged parts out of order or wrong: %+v", got[0].Parts)
		}
		if got[1] != in[2] {
			t.Errorf("expected trailing model turn untouched")
		}
	})

	t.Run("merges multiple separate runs and skips nil", func(t *testing.T) {
		in := []*genai.Content{
			text("user", "u1"),
			text("user", "u2"),
			text("model", "m1"),
			nil,
			text("user", "u3"),
			text("user", "u4"),
			text("user", "u5"),
		}
		got := MergeConsecutiveUserTurns(in)
		if len(got) != 3 {
			t.Fatalf("expected 3 turns (merged, model, merged), got %d", len(got))
		}
		if len(got[0].Parts) != 2 {
			t.Errorf("expected first merged run to have 2 parts, got %d", len(got[0].Parts))
		}
		if got[1].Role != "model" {
			t.Errorf("expected middle turn to be model, got %s", got[1].Role)
		}
		if len(got[2].Parts) != 3 {
			t.Errorf("expected second merged run to have 3 parts, got %d", len(got[2].Parts))
		}
	})
}
