package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultModel == "" {
		t.Errorf("expected non-empty default model")
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.yaml")

	cfg := &Config{
		DefaultModel: "gemini-2.5-pro",
		APIKey:       "test-key",
	}

	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if loaded.DefaultModel != "gemini-2.5-pro" {
		t.Errorf("expected model gemini-2.5-pro, got %s", loaded.DefaultModel)
	}
	if loaded.APIKey != "test-key" {
		t.Errorf("expected api key test-key, got %s", loaded.APIKey)
	}
}

func TestLoadConfigNonExistentFallback(t *testing.T) {
	cfg, err := LoadConfig(filepath.Join(os.TempDir(), "non_existent_wackypub.yaml"))
	if err != nil {
		t.Fatalf("unexpected error when loading non-existent config: %v", err)
	}
	if cfg == nil || cfg.DefaultModel == "" {
		t.Errorf("expected fallback default config when file does not exist")
	}
}
