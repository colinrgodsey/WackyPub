package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRuntimeConfig(t *testing.T) {
	tempDir := t.TempDir()

	jsonContent := `{
		"endpoint": "http://localhost:11434/v1",
		"model": "llama3",
		"apiKey": "test-key",
		"sessionCompactPct": 40.0,
		"contextWindow": 4096
	}`

	runtimeFile := filepath.Join(tempDir, "runtime.json")
	if err := os.WriteFile(runtimeFile, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("failed to write runtime.json: %v", err)
	}

	cfg, err := LoadRuntimeConfig(tempDir)
	if err != nil {
		t.Fatalf("failed to load runtime config: %v", err)
	}

	if cfg.Endpoint != "http://localhost:11434/v1" {
		t.Errorf("expected endpoint http://localhost:11434/v1, got %s", cfg.Endpoint)
	}
	if cfg.Model != "llama3" {
		t.Errorf("expected model llama3, got %s", cfg.Model)
	}
	if cfg.SessionCompactPct != 40.0 {
		t.Errorf("expected compact pct 40.0, got %f", cfg.SessionCompactPct)
	}
	if cfg.ContextWindow != 4096 {
		t.Errorf("expected context window 4096, got %d", cfg.ContextWindow)
	}
}

func TestLoadRuntimeConfigSymlink(t *testing.T) {
	tempDir := t.TempDir()

	realJson := filepath.Join(tempDir, "global_runtime.json")
	symlinkJson := filepath.Join(tempDir, "runtime.json")

	content := `{
		"endpoint": "https://api.openai.com/v1",
		"model": "gpt-4o",
		"apiKey": "sk-test",
		"sessionCompactPct": 50.0,
		"contextWindow": 8192
	}`

	if err := os.WriteFile(realJson, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write global_runtime.json: %v", err)
	}

	if err := os.Symlink(realJson, symlinkJson); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	cfg, err := LoadRuntimeConfig(tempDir)
	if err != nil {
		t.Fatalf("failed to load runtime config via symlink: %v", err)
	}

	if cfg.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o via symlink, got %s", cfg.Model)
	}
}
