package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/genai"
)

const SessionFileName = "session.jsonl"

// ReadSessionTurns reads all turns from <agent_dir>/session.jsonl as genai.Content objects.
// If the file does not exist, returns an empty list without error.
func ReadSessionTurns(agentDir string) ([]*genai.Content, error) {
	sessionPath := filepath.Join(agentDir, "session.jsonl")

	file, err := os.Open(sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to open session file at %s: %w", sessionPath, err)
	}
	defer file.Close()

	var turns []*genai.Content
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line size for large parts
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var turn genai.Content
		if err := json.Unmarshal(line, &turn); err != nil {
			// Skip corrupted lines gracefully
			continue
		}
		turns = append(turns, &turn)
	}

	if err := scanner.Err(); err != nil {
		return turns, fmt.Errorf("error reading session file at %s: %w", sessionPath, err)
	}

	return turns, nil
}

// AppendSessionContent appends a genai.Content turn to <agent_dir>/session.jsonl.
func AppendSessionContent(agentDir string, content *genai.Content) error {
	sessionPath := filepath.Join(agentDir, "session.jsonl")

	data, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to marshal content: %w", err)
	}
	data = append(data, '\n')

	file, err := os.OpenFile(sessionPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open session.jsonl for writing: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write content to session.jsonl: %w", err)
	}

	return nil
}

// AppendSessionTurn is a convenience wrapper that appends a simple text turn.
func AppendSessionTurn(agentDir string, role string, text string) error {
	return AppendSessionContent(agentDir, genai.NewContentFromText(text, genai.Role(role)))
}

// WriteSessionTurns overwrites <agent_dir>/session.jsonl with a new list of turns.
func WriteSessionTurns(agentDir string, turns []*genai.Content) error {
	sessionPath := filepath.Join(agentDir, "session.jsonl")

	file, err := os.Create(sessionPath)
	if err != nil {
		return fmt.Errorf("failed to create session.jsonl: %w", err)
	}
	defer file.Close()

	writer := bufio.NewWriter(file)
	for _, t := range turns {
		data, err := json.Marshal(t)
		if err != nil {
			continue
		}
		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("failed writing turn to session.jsonl: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return fmt.Errorf("failed writing newline to session.jsonl: %w", err)
		}
	}

	return writer.Flush()
}

// ContentText extracts the concatenated final-answer text from a genai.Content's parts,
// excluding any parts marked as Thought (reasoning/thinking output).
func ContentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var text string
	for _, p := range c.Parts {
		if p != nil && p.Text != "" && !p.Thought {
			text += p.Text
		}
	}
	return text
}

// EstimateTokens calculates an approximate token count for session turns.
// includeThinking should match RuntimeConfig.PreserveThinking: when true,
// Thought-marked part text is counted too, since it's actually replayed to
// the model on every subsequent request for backends that preserve thinking.
func EstimateTokens(turns []*genai.Content, includeThinking bool) int {
	totalChars := 0
	imageTokens := 0
	for _, t := range turns {
		if t == nil {
			continue
		}
		if includeThinking {
			totalChars += len(contentTextAll(t))
		} else {
			totalChars += len(ContentText(t))
		}
		for _, p := range t.Parts {
			if p != nil && p.InlineData != nil && len(p.InlineData.Data) > 0 {
				rawLen := len(p.InlineData.Data)
				b64Len := (rawLen + 2) / 3 * 4
				imageTokens += b64Len / 150
			}
		}
	}
	// Heuristic: ~4 characters per token + per-image tokens
	return (totalChars / 4) + imageTokens
}

// contentTextAll extracts the concatenated text from all of a genai.Content's
// parts, including Thought-marked ones.
func contentTextAll(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var text string
	for _, p := range c.Parts {
		if p != nil && p.Text != "" {
			text += p.Text
		}
	}
	return text
}

// MergeConsecutiveUserTurns collapses runs of consecutive "user"-role Contents
// into a single Content per run, concatenating their Parts in order.
//
// session.jsonl intentionally allows consecutive user turns to accumulate —
// multiple `add` calls without an intervening `generate`, and, on every
// generation, the injected system-prompt+memory turn landing immediately
// before whatever the first real turn happens to be (itself usually "user").
// That's fine for storage, but many OpenAI-compatible chat templates reject
// or silently mishandle non-alternating roles. This normalizes the sequence
// right before it's sent to a model, without touching what's stored on disk;
// callers should apply it to the Contents slice built for a model.LLMRequest,
// not to what gets persisted via AppendSessionContent/WriteSessionTurns.
//
// Only "user" runs are merged. "model" turns are never produced back-to-back
// under normal operation (each GenerateTurn call reads history, then appends
// exactly one model turn), so there's nothing to collapse there.
func MergeConsecutiveUserTurns(contents []*genai.Content) []*genai.Content {
	merged := make([]*genai.Content, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		if n := len(merged); n > 0 && merged[n-1].Role == "user" && c.Role == "user" {
			combinedParts := make([]*genai.Part, 0, len(merged[n-1].Parts)+len(c.Parts))
			combinedParts = append(combinedParts, merged[n-1].Parts...)
			combinedParts = append(combinedParts, c.Parts...)
			merged[n-1] = &genai.Content{Role: "user", Parts: combinedParts}
			continue
		}
		merged = append(merged, c)
	}
	return merged
}
