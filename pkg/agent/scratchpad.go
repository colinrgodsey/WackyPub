package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

const ScratchpadFileName = "scratchpad.json"

// ReadScratchpad reads the persistent scratchpad map from <agentDir>/scratchpad.json.
// If the file does not exist, returns an empty map.
func ReadScratchpad(agentDir string) (map[int]string, error) {
	spPath := filepath.Join(agentDir, ScratchpadFileName)
	data, err := os.ReadFile(spPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[int]string), nil
		}
		return nil, fmt.Errorf("failed to read scratchpad at %s: %w", spPath, err)
	}

	var stringMap map[string]string
	if err := json.Unmarshal(data, &stringMap); err != nil {
		return nil, fmt.Errorf("failed to parse scratchpad JSON: %w", err)
	}

	result := make(map[int]string, len(stringMap))
	for kStr, val := range stringMap {
		id, err := strconv.Atoi(kStr)
		if err == nil {
			result[id] = val
		}
	}
	return result, nil
}

// WriteScratchpad persists the scratchpad map to <agentDir>/scratchpad.json.
func WriteScratchpad(agentDir string, sp map[int]string) error {
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory: %w", err)
	}

	stringMap := make(map[string]string, len(sp))
	for id, val := range sp {
		stringMap[strconv.Itoa(id)] = val
	}

	data, err := json.MarshalIndent(stringMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal scratchpad JSON: %w", err)
	}

	spPath := filepath.Join(agentDir, ScratchpadFileName)
	return os.WriteFile(spPath, data, 0644)
}

// SetScratchpad updates scratchpad entry for id and persists it.
func SetScratchpad(agentDir string, id int, text string) (string, error) {
	sp, err := ReadScratchpad(agentDir)
	if err != nil {
		return "", err
	}

	sp[id] = text
	if err := WriteScratchpad(agentDir, sp); err != nil {
		return "", err
	}
	return fmt.Sprintf("Scratchpad %d updated (%d bytes)", id, len(text)), nil
}

// GetScratchpad retrieves scratchpad entry for id.
func GetScratchpad(agentDir string, id int) (string, error) {
	sp, err := ReadScratchpad(agentDir)
	if err != nil {
		return "", err
	}

	val, exists := sp[id]
	if !exists {
		return fmt.Sprintf("Scratchpad %d is empty", id), nil
	}
	return val, nil
}
