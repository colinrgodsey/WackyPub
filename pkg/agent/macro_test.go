package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandMacros(t *testing.T) {
	tempDir := t.TempDir()

	rulesContent := "Rule 1: Always be helpful.\nRule 2: Speak concisely."
	if err := os.WriteFile(filepath.Join(tempDir, "rules.md"), []byte(rulesContent), 0644); err != nil {
		t.Fatalf("failed to write rules.md: %v", err)
	}

	mainPrompt := "System Prompt:\n@rules.md\nEnd Prompt"
	expanded, err := ExpandMacros(mainPrompt, tempDir)
	if err != nil {
		t.Fatalf("unexpected macro expansion error: %v", err)
	}

	if !strings.Contains(expanded, rulesContent) {
		t.Errorf("expected expanded prompt to contain rulesContent, got:\n%s", expanded)
	}
}

func TestExpandMacrosCircular(t *testing.T) {
	tempDir := t.TempDir()

	fileA := "@fileB.md"
	fileB := "@fileA.md"
	if err := os.WriteFile(filepath.Join(tempDir, "fileA.md"), []byte(fileA), 0644); err != nil {
		t.Fatalf("failed to write fileA.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "fileB.md"), []byte(fileB), 0644); err != nil {
		t.Fatalf("failed to write fileB.md: %v", err)
	}

	expanded, err := ExpandMacros("@fileA.md", tempDir)
	if err != nil {
		t.Fatalf("unexpected error during circular macro expansion: %v", err)
	}

	if !strings.Contains(expanded, "Circular macro import omitted") {
		t.Errorf("expected circular import omission notice, got:\n%s", expanded)
	}
}
