package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration for the WackyPubAI CLI.
type Config struct {
	DefaultModel string `yaml:"default_model"`
	APIKey       string `yaml:"api_key,omitempty"`
}

// DefaultConfig provides a fallback starting configuration.
func DefaultConfig() *Config {
	return &Config{
		DefaultModel: "gemini-2.5-flash",
	}
}

// LoadConfig reads a YAML configuration file from path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			cfg := DefaultConfig()
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config %s: %w", path, err)
	}

	// Environment variable fallback for Gemini API key
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("GEMINI_API_KEY")
	}

	return cfg, nil
}

// SaveConfig writes the configuration back to a YAML file.
func SaveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}

	return nil
}
