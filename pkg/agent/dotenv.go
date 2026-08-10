package agent

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ParseDotEnv reads a .env file from the specified path and returns a key-value map.
// If the file does not exist, it returns an empty map and a nil error.
func ParseDotEnv(dotenvPath string) (map[string]string, error) {
	data, err := os.ReadFile(dotenvPath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}

	result := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle optional export keyword
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[7:])
		}

		idx := strings.Index(line, "=")
		if idx == -1 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])

		if key == "" {
			continue
		}

		val = parseDotEnvValue(val)
		result[key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func parseDotEnvValue(val string) string {
	if len(val) >= 2 && strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") {
		unquoted, err := strconv.Unquote(val)
		if err == nil {
			return unquoted
		}
		return val[1 : len(val)-1]
	}

	if len(val) >= 2 && strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'") {
		return val[1 : len(val)-1]
	}

	// Strip inline comment for unquoted values if space precedes '#'
	if commentIdx := strings.Index(val, " #"); commentIdx != -1 {
		val = strings.TrimSpace(val[:commentIdx])
	} else if commentIdx := strings.Index(val, "\t#"); commentIdx != -1 {
		val = strings.TrimSpace(val[:commentIdx])
	}

	return val
}

// LoadAgentDotEnv loads the .env file for an agent located at <agentDir>/.env.
func LoadAgentDotEnv(agentDir string) (map[string]string, error) {
	dotenvPath := filepath.Join(agentDir, ".env")
	return ParseDotEnv(dotenvPath)
}
