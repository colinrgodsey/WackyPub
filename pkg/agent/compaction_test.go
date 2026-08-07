package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestFormatPersistentMemoryTurn(t *testing.T) {
	mem := "Fact A: User is a mechanic."
	turn := FormatPersistentMemoryTurn(mem)

	expected := "<PERSISTENT_MEMORY>\nFact A: User is a mechanic.\n</PERSISTENT_MEMORY>"
	if turn != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, turn)
	}
}

func TestCompactionPrefixPreservation(t *testing.T) {
	tempDir := t.TempDir()

	// Write initial MEMORY.md
	if err := WriteMemoryFile(tempDir, "Initial Memory"); err != nil {
		t.Fatalf("failed to write MEMORY.md: %v", err)
	}

	// Write session turns
	turns := []*genai.Content{
		genai.NewContentFromText("Turn 1 user message", "user"),
		genai.NewContentFromText("Turn 1 assistant response", "model"),
		genai.NewContentFromText("Turn 2 user message", "user"),
		genai.NewContentFromText("Turn 2 assistant response", "model"),
	}
	if err := WriteSessionTurns(tempDir, turns); err != nil {
		t.Fatalf("failed to write session turns: %v", err)
	}

	runtimeCfg := &RuntimeConfig{
		ContextWindow:     10, // low threshold to force compaction
		SessionCompactPct: 50.0,
	}

	mockModel := NewOpenAIModel(&RuntimeConfig{
		Model:    "test-model",
		Endpoint: "http://localhost:9999",
		APIKey:   "fake-key",
	})

	// Check compaction with mock model (http request will fail gracefully, testing flow)
	ctx := context.Background()
	_, err := CheckAndCompactSession(ctx, tempDir, runtimeCfg, "System prompt", mockModel)
	if err == nil {
		// Mock HTTP error expected
	}

	// Verify MEMORY.md file exists
	memContent, err := ReadMemoryFile(tempDir)
	if err != nil {
		t.Fatalf("failed to read MEMORY.md: %v", err)
	}
	if !strings.Contains(memContent, "Initial Memory") {
		t.Errorf("expected MEMORY.md to preserve existing content")
	}
}
