package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		ContextWindow: 10, // low threshold to force compaction
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
	runtimeCfg := &RuntimeConfig{ContextWindow: 1} // ContextWindow=1 forces compaction regardless of content size

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
	// 50% cut (index 3) extends forward to index 4 (through "m1"), leaving
	// exactly 2 turns: u2, m2.
	if len(remaining) != 2 {
		t.Errorf("expected 2 remaining turns (u2, m2), got %d: %+v", len(remaining), remaining)
	}
}

func TestLoadCompactConfig_Defaults(t *testing.T) {
	tempDir := t.TempDir()

	cfg, err := LoadCompactConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadCompactConfig failed: %v", err)
	}

	if !cfg.AppendOnly {
		t.Errorf("expected default AppendOnly to be true")
	}
	if cfg.CompactPct != 50.0 {
		t.Errorf("expected default CompactPct to be 50.0, got %f", cfg.CompactPct)
	}
	if cfg.Prompt != CompactionDirectivePrompt {
		t.Errorf("expected default Prompt to equal CompactionDirectivePrompt")
	}
}

func TestLoadCompactConfig_CustomFrontmatterAndBody(t *testing.T) {
	tempDir := t.TempDir()

	content := "---\nappend-only: false\ncompact-pct: 25\n---\nCustom Compaction Directive Prompt Body"
	compactPath := filepath.Join(tempDir, "COMPACT.md")
	if err := os.WriteFile(compactPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write COMPACT.md: %v", err)
	}

	cfg, err := LoadCompactConfig(tempDir)
	if err != nil {
		t.Fatalf("LoadCompactConfig failed: %v", err)
	}

	if cfg.AppendOnly {
		t.Errorf("expected AppendOnly to be false")
	}
	if cfg.CompactPct != 25.0 {
		t.Errorf("expected CompactPct to be 25.0, got %f", cfg.CompactPct)
	}
	if cfg.Prompt != "Custom Compaction Directive Prompt Body" {
		t.Errorf("expected custom prompt body, got %q", cfg.Prompt)
	}
}

func TestCompactionWithCustomCompactMD(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Write existing MEMORY.md that should be REPLACED (because append-only: false)
	if err := WriteMemoryFile(tempDir, "Old Memory Content To Be Overwritten"); err != nil {
		t.Fatalf("failed to write MEMORY.md: %v", err)
	}

	// 2. Write custom COMPACT.md
	compactContent := "---\nappend-only: false\ncompact-pct: 50\n---\nSummarize state into clean new memory."
	if err := os.WriteFile(filepath.Join(tempDir, "COMPACT.md"), []byte(compactContent), 0644); err != nil {
		t.Fatalf("failed writing COMPACT.md: %v", err)
	}

	// 3. Session turns (4 turns: u0, m0, u1, m1)
	turns := []*genai.Content{
		genai.NewContentFromText("u0", "user"),
		genai.NewContentFromText("m0", "model"),
		genai.NewContentFromText("u1", "user"),
		genai.NewContentFromText("m1", "model"),
	}
	if err := WriteSessionTurns(tempDir, turns); err != nil {
		t.Fatalf("failed writing session turns: %v", err)
	}

	var receivedPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedPrompt = string(body)

		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Brand New Wholesale Memory"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	model := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1}

	compacted, err := CheckAndCompactSession(context.Background(), tempDir, runtimeCfg, "system prompt", model)
	if err != nil {
		t.Fatalf("CheckAndCompactSession failed: %v", err)
	}
	if !compacted {
		t.Fatalf("expected compaction to execute")
	}

	// Verify custom prompt reached the model payload
	if !strings.Contains(receivedPrompt, "Summarize state into clean new memory.") {
		t.Errorf("expected custom prompt in HTTP request payload, got: %s", receivedPrompt)
	}

	// Verify MEMORY.md was replaced wholesale (not appended)
	memContent, err := ReadMemoryFile(tempDir)
	if err != nil {
		t.Fatalf("failed to read MEMORY.md: %v", err)
	}
	if strings.Contains(memContent, "Old Memory Content To Be Overwritten") {
		t.Errorf("expected old memory to be replaced wholesale when append-only is false, got: %s", memContent)
	}
	if !strings.Contains(memContent, "Brand New Wholesale Memory") {
		t.Errorf("expected new wholesale memory content, got: %s", memContent)
	}
}
