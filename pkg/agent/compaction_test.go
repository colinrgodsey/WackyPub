package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

// TestMain seeds DefaultCompactMD from the real examples/COMPACT.md before any
// test runs. Production gets this from main.go's //go:embed (D45) - main.go
// never runs under `go test`, so tests read the same real file directly
// instead of a fabricated fixture, to keep exercising the actual shipped
// default content rather than a stand-in.
func TestMain(m *testing.M) {
	data, err := os.ReadFile("../../examples/COMPACT.md")
	if err != nil {
		panic("failed to read examples/COMPACT.md for tests: " + err.Error())
	}
	DefaultCompactMD = string(data)
	os.Exit(m.Run())
}

// mustBuildTestADKAgent builds a real ADK agent around llmModel the same way
// LoadFolderAgent does, for tests that need to call CheckAndCompactSession
// (D45 - it takes an agent.Agent, not a bare model.LLM, so its request goes
// through the real runner/llmagent pipeline like a normal turn would). agentDir
// must be the same directory passed to CheckAndCompactSession: the ADK
// agent's Name has to equal filepath.Base(agentDir), exactly like
// LoadFolderAgent always guarantees in production - otherwise seeded
// "model"-role turns get attributed to an agent the runner doesn't
// recognize as self and are re-wrapped as third-party "for context" text
// instead of landing as native assistant-role turns (confirmed live: this
// is a real, observable wire-shape difference, not cosmetic).
func mustBuildTestADKAgent(t *testing.T, agentDir string, systemPrompt string, runtimeCfg *RuntimeConfig, llmModel model.LLM, tools ...tool.Tool) agent.Agent {
	t.Helper()
	agentID := filepath.Base(agentDir)
	ag, err := BuildADKAgentWithConfig(agentID, systemPrompt, DefaultMaxToolTurns, runtimeCfg, llmModel, tools...)
	if err != nil {
		t.Fatalf("failed to build test ADK agent: %v", err)
	}
	return ag
}

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
	adkAgent := mustBuildTestADKAgent(t, tempDir, "System prompt", runtimeCfg, mockModel)

	// Check compaction with mock model (http request will fail gracefully, testing flow)
	ctx := context.Background()
	_, err := CheckAndCompactSession(ctx, tempDir, runtimeCfg, adkAgent, false)
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

	llmModel := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1} // ContextWindow=1 forces compaction regardless of content size
	adkAgent := mustBuildTestADKAgent(t, tempDir, "system prompt", runtimeCfg, llmModel)

	ctx := context.Background()
	compacted, err := CheckAndCompactSession(ctx, tempDir, runtimeCfg, adkAgent, false)
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
	// u2, m2 - plus the D46 compaction-notice turn prepended in front of them.
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining turns (compaction notice, u2, m2), got %d: %+v", len(remaining), remaining)
	}
	if len(remaining) > 0 && !strings.Contains(ContentText(remaining[0]), "<COMPACTION_NOTICE>") {
		t.Errorf("expected remaining[0] to be the D46 compaction notice turn, got: %+v", remaining[0])
	}
	if len(remaining) > 1 && ContentText(remaining[1]) != "u2" {
		t.Errorf("expected remaining[1] to be the surviving \"u2\" turn, got: %+v", remaining[1])
	}
}

// TestCompactionNoticeOptOut verifies an explicit compaction-notice: "" in
// COMPACT.md suppresses the D46 notice turn entirely, rather than falling
// back to the built-in default the way an absent key does.
func TestCompactionNoticeOptOut(t *testing.T) {
	tempDir := t.TempDir()

	turns := []*genai.Content{
		genai.NewContentFromText("u0", "user"),
		genai.NewContentFromText("m0", "model"),
		genai.NewContentFromText("u1", "user"),
		genai.NewContentFromText("m1", "model"),
	}
	if err := WriteSessionTurns(tempDir, turns); err != nil {
		t.Fatalf("failed to write session turns: %v", err)
	}

	compactContent := "---\ncompaction-notice: \"\"\n---\nSummarize."
	if err := os.WriteFile(filepath.Join(tempDir, "COMPACT.md"), []byte(compactContent), 0644); err != nil {
		t.Fatalf("failed writing COMPACT.md: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"* addendum"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	llmModel := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1}
	adkAgent := mustBuildTestADKAgent(t, tempDir, "system prompt", runtimeCfg, llmModel)

	compacted, err := CheckAndCompactSession(context.Background(), tempDir, runtimeCfg, adkAgent, false)
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
	// 50% cut (index 2) already lands on a model turn (m0), leaving u1, m1 -
	// a non-empty remaining session, so the notice would normally be
	// prepended here if not explicitly opted out.
	if len(remaining) != 2 {
		t.Fatalf("expected 2 remaining turns (u1, m1) with no notice, got %d: %+v", len(remaining), remaining)
	}
	for _, c := range remaining {
		if strings.Contains(ContentText(c), "<COMPACTION_NOTICE>") {
			t.Errorf("expected compaction-notice: \"\" to suppress the notice turn entirely, got: %+v", remaining)
		}
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
	wantCfg, err := ParseCompactConfig(DefaultCompactMD)
	if err != nil {
		t.Fatalf("ParseCompactConfig(DefaultCompactMD) failed: %v", err)
	}
	if cfg.Prompt != wantCfg.Prompt {
		t.Errorf("expected default Prompt to equal the embedded DefaultCompactMD's body, got a mismatch")
	}
	if !strings.Contains(cfg.Prompt, "state compaction engine") {
		t.Errorf("expected default Prompt to contain the compaction directive text, got: %s", cfg.Prompt)
	}
	if !strings.Contains(cfg.Prompt, "SKILL LOADS") {
		t.Errorf("expected default Prompt to contain the D44 skill-loads guideline, got: %s", cfg.Prompt)
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

	llmModel := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1}
	adkAgent := mustBuildTestADKAgent(t, tempDir, "system prompt", runtimeCfg, llmModel)

	compacted, err := CheckAndCompactSession(context.Background(), tempDir, runtimeCfg, adkAgent, false)
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

type d45EchoArgs struct {
	Text string `json:"text"`
}

// mustBuildEchoTool is a minimal real tool.Tool, used only to prove tool
// declarations actually reach the wire request below.
func mustBuildEchoTool(t *testing.T) tool.Tool {
	t.Helper()
	tl, err := functiontool.New(functiontool.Config{
		Name:        "echo_tool",
		Description: "Echoes the given text back",
	}, func(ctx agent.Context, args d45EchoArgs) (string, error) {
		return args.Text, nil
	})
	if err != nil {
		t.Fatalf("failed to build echo tool: %v", err)
	}
	return tl
}

// TestCheckAndCompactSession_WirePayloadMatchesRealTurnShape is the
// httptest-based wire-payload test the "compaction bypasses the runner
// pipeline" TODO asked for once fixed (D45) - a test that would have failed
// against the old hand-built-request implementation, not one that just
// re-confirms whatever the current behavior happens to be. Verifies, in the
// literal JSON sent to the model: the system prompt lands in the dedicated
// "system" role message (not glued into turn 1's text), archived turns
// appear as native user/assistant-role messages (not text), and a real tool
// declaration is present - all three were absent or wrong before this
// decision.
func TestCheckAndCompactSession_WirePayloadMatchesRealTurnShape(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "test-agent")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		t.Fatalf("failed to create agent dir: %v", err)
	}

	turns := []*genai.Content{
		genai.NewContentFromText("u0-marker-abc", "user"),
		genai.NewContentFromText("m0-marker-def", "model"),
		genai.NewContentFromText("u1", "user"),
		genai.NewContentFromText("m1", "model"),
	}
	if err := WriteSessionTurns(tempDir, turns); err != nil {
		t.Fatalf("failed writing session turns: %v", err)
	}

	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"addendum text"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	llmModel := NewOpenAIModel(&RuntimeConfig{Model: "test-model", Endpoint: srv.URL})
	runtimeCfg := &RuntimeConfig{ContextWindow: 1}
	adkAgent := mustBuildTestADKAgent(t, tempDir, "system prompt with SENTINEL_SYS_PROMPT", runtimeCfg, llmModel, mustBuildEchoTool(t))

	compacted, err := CheckAndCompactSession(context.Background(), tempDir, runtimeCfg, adkAgent, false)
	if err != nil {
		t.Fatalf("CheckAndCompactSession failed: %v", err)
	}
	if !compacted {
		t.Fatalf("expected compaction to occur")
	}

	var wire struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(receivedBody), &wire); err != nil {
		t.Fatalf("failed to parse wire payload as JSON: %v\nbody: %s", err, receivedBody)
	}

	if len(wire.Messages) == 0 || wire.Messages[0].Role != "system" || !strings.Contains(wire.Messages[0].Content, "SENTINEL_SYS_PROMPT") {
		t.Errorf("expected message[0] to be a dedicated system-role message containing the system prompt, got: %+v", wire.Messages)
	}

	var sawArchivedUserTurn, sawArchivedAssistantTurn bool
	for _, m := range wire.Messages {
		if m.Role == "user" && strings.Contains(m.Content, "u0-marker-abc") {
			sawArchivedUserTurn = true
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "m0-marker-def") {
			sawArchivedAssistantTurn = true
		}
	}
	if !sawArchivedUserTurn {
		t.Errorf("expected archived user turn to appear as a native user-role message, got: %+v", wire.Messages)
	}
	if !sawArchivedAssistantTurn {
		t.Errorf("expected archived model turn to appear as a native assistant-role message (not re-wrapped as third-party text), got: %+v", wire.Messages)
	}

	var sawTool bool
	for _, tl := range wire.Tools {
		if tl.Function.Name == "echo_tool" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("expected tool declaration %q in wire payload - this is exactly what the pre-D45 direct GenerateContent call never sent, got tools: %+v", "echo_tool", wire.Tools)
	}
}
