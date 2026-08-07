package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

// TestCompactionEndsOnModelTurn verifies the compaction boundary always
// lands after a model turn, so the surviving session never opens with a
// dangling assistant response whose prompting user turn was just archived
// into MEMORY.md. With sessionCompactPct=50 over 6 turns, a raw percentage
// cut would land mid-exchange (index 3, "user1") - this checks it gets
// extended forward to the next model turn instead.
func TestCompactionEndsOnModelTurn(t *testing.T) {
	tempDir := t.TempDir()

	turns := []*genai.Content{
		genai.NewContentFromText("u0", "user"),
		genai.NewContentFromText("m0", "model"),
		genai.NewContentFromText("u1", "user"),
		genai.NewContentFromText("m1", "model"),
		genai.NewContentFromText("u2", "user"),
		genai.NewContentFromText("m2", "model"),
	}
	if err := WriteSessionTurns(tempDir, turns); err != nil {
		t.Fatalf("failed to write session turns: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"* addendum"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	model := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1, SessionCompactPct: 50.0} // ContextWindow=1 forces compaction regardless of content size

	ctx := context.Background()
	compacted, err := CheckAndCompactSession(ctx, tempDir, runtimeCfg, "system prompt", model)
	if err != nil {
		t.Fatalf("CheckAndCompactSession failed: %v", err)
	}
	if !compacted {
		t.Fatalf("expected compaction to occur")
	}

	remaining, err := ReadSessionTurns(tempDir)
	if err != nil {
		t.Fatalf("failed to read remaining session turns: %v", err)
	}
	if len(remaining) == 0 {
		t.Fatalf("expected some turns to remain after compaction")
	}
	if remaining[0].Role != "user" {
		t.Errorf("expected remaining session to start with a user turn, got a dangling %q turn: %+v", remaining[0].Role, remaining[0])
	}
	// With the fix, the 50% cut (index 3) should extend forward to index 4
	// (through "m1"), leaving exactly 2 turns: u2, m2.
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining turns (u2, m2), got %d: %+v", len(remaining), remaining)
	}
}
