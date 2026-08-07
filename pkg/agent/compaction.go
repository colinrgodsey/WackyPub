package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const CompactionDirectivePrompt = `You are updating a persistent execution log for this session. Above is the existing <PERSISTENT_MEMORY> contents, followed by a sequence of early session turns that are about to be archived.
Task: Generate a concise, chronological ADDENDUM to be appended directly to <PERSISTENT_MEMORY> that captures new critical developments from the archived turns.
Guidelines:
Do NOT repeat facts or state already captured in <PERSISTENT_MEMORY>.
Focus on: decisions made, character actions, relationships, unresolved issues, emotions.
Use bullet points with no header.
Do not include conversational filler; output ONLY the raw markdown to append.`

// ReadMemoryFile reads the contents of <agent_dir>/MEMORY.md.
// If the file does not exist, returns empty string without error.
func ReadMemoryFile(agentDir string) (string, error) {
	memPath := filepath.Join(agentDir, "MEMORY.md")
	data, err := os.ReadFile(memPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read MEMORY.md at %s: %w", memPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

// WriteMemoryFile updates the contents of <agent_dir>/MEMORY.md.
func WriteMemoryFile(agentDir string, memoryContent string) error {
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}
	memPath := filepath.Join(agentDir, "MEMORY.md")
	return os.WriteFile(memPath, []byte(strings.TrimSpace(memoryContent)+"\n"), 0644)
}

// FormatPersistentMemoryTurn constructs User Turn 1 wrapping MEMORY.md in <PERSISTENT_MEMORY> tags.
func FormatPersistentMemoryTurn(memoryContent string) string {
	return fmt.Sprintf("<PERSISTENT_MEMORY>\n%s\n</PERSISTENT_MEMORY>", strings.TrimSpace(memoryContent))
}

// CheckAndCompactSession checks if the session exceeds contextWindow and performs compaction,
// preserving the exact session prefix to optimize prompt caching.
func CheckAndCompactSession(ctx context.Context, agentDir string, runtimeCfg *RuntimeConfig, systemPrompt string, llmModel model.LLM) (bool, error) {
	if runtimeCfg.ContextWindow <= 0 {
		return false, nil
	}

	turns, err := ReadSessionTurns(agentDir)
	if err != nil {
		return false, err
	}

	if len(turns) == 0 {
		return false, nil
	}

	estimatedTokens := EstimateTokens(turns, runtimeCfg.PreserveThinking)
	if estimatedTokens < runtimeCfg.ContextWindow {
		return false, nil
	}

	// Calculate turns to compact based on sessionCompactPct
	pct := runtimeCfg.SessionCompactPct
	if pct <= 0 || pct > 100 {
		pct = 50.0
	}

	numToCompact := int(float64(len(turns)) * (pct / 100.0))
	if numToCompact < 1 {
		numToCompact = 1
	}
	if numToCompact > len(turns) {
		numToCompact = len(turns)
	}

	compactTurns := turns[:numToCompact]
	remainingTurns := turns[numToCompact:]

	existingMemory, err := ReadMemoryFile(agentDir)
	if err != nil {
		return false, err
	}

	// Build contents payload matching the exact session prefix
	var contents []*genai.Content

	// 1. User Turn 1: system prompt + <PERSISTENT_MEMORY>, mirroring GenerateTurn's first
	// turn exactly so the request prefix matches for prompt caching.
	memTurnText := FormatPersistentMemoryTurn(existingMemory)
	firstTurnText := systemPrompt + "\n\n" + memTurnText
	contents = append(contents, genai.NewContentFromText(firstTurnText, "user"))

	// 2. First X% of session.jsonl turns (already genai.Content)
	contents = append(contents, compactTurns...)

	// 3. User Turn with Compaction Directive
	contents = append(contents, genai.NewContentFromText(CompactionDirectivePrompt, "user"))

	// Collapse consecutive user turns before sending — same rationale as
	// GenerateTurn (see MergeConsecutiveUserTurns doc comment).
	contents = MergeConsecutiveUserTurns(contents)

	req := &model.LLMRequest{
		Model:    llmModel.Name(),
		Contents: contents,
	}

	var addendum string
	for resp, err := range llmModel.GenerateContent(ctx, req, false) {
		if err != nil {
			return false, fmt.Errorf("LLM compaction generation failed: %w", err)
		}
		if resp != nil && resp.Content != nil {
			addendum += ContentText(resp.Content)
		}
	}

	addendum = strings.TrimSpace(addendum)
	if addendum != "" {
		newMemory := strings.TrimSpace(existingMemory)
		if newMemory != "" {
			newMemory = newMemory + "\n\n" + addendum
		} else {
			newMemory = addendum
		}

		if err := WriteMemoryFile(agentDir, newMemory); err != nil {
			return false, fmt.Errorf("failed to update MEMORY.md: %w", err)
		}
	}

	if err := WriteSessionTurns(agentDir, remainingTurns); err != nil {
		return false, fmt.Errorf("failed to update session.jsonl after compaction: %w", err)
	}

	return true, nil
}
