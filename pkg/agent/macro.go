package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MacroRegex matches @<FILE_PATH> patterns where FILE_PATH is a relative path.
var macroRegex = regexp.MustCompile(`@([a-zA-Z0-9_\-./]+)`)

// ExpandMacros processes text content and replaces any @<FILE_PATH> directives
// with the content of the referenced file relative to agentDir.
func ExpandMacros(content string, agentDir string) (string, error) {
	visited := make(map[string]bool)
	return expandMacrosRecursive(content, agentDir, visited, 0)
}

// RenderAgentSystemPrompt reads <wsDir>/<agentID>/AGENTS.md (falling back to
// a generic "You are agent <id>." prompt if it doesn't exist, matching
// LoadFolderAgent) and expands @<FILE_PATH> macros. Unlike LoadFolderAgent,
// it does not touch runtime.json and does not construct a model - useful for
// validating AGENTS.md/macro output independently of backend configuration.
func RenderAgentSystemPrompt(wsDir, agentID string) (string, error) {
	agentDir := filepath.Join(wsDir, agentID)
	agentsPath := filepath.Join(agentDir, "AGENTS.md")

	agentsData, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			agentsData = []byte(fmt.Sprintf("You are agent %s.", agentID))
		} else {
			return "", fmt.Errorf("failed to read AGENTS.md at %s: %w", agentsPath, err)
		}
	}

	expanded, err := ExpandMacros(string(agentsData), agentDir)
	if err != nil {
		return "", fmt.Errorf("macro expansion failed for %s: %w", agentsPath, err)
	}

	autoloadBlock, err := RenderAutoloadedSkills(agentDir)
	if err != nil {
		return "", fmt.Errorf("failed to render autoloaded skills for %s: %w", agentID, err)
	}
	if autoloadBlock != "" {
		expanded = expanded + "\n\n" + autoloadBlock
	}

	return expanded, nil
}

func expandMacrosRecursive(content string, agentDir string, visited map[string]bool, depth int) (string, error) {
	if depth > 10 {
		return content, fmt.Errorf("macro expansion depth exceeded limit of 10")
	}

	result := macroRegex.ReplaceAllStringFunc(content, func(match string) string {
		relPath := strings.TrimPrefix(match, "@")
		absPath := filepath.Join(agentDir, relPath)

		// Prevent circular imports
		if visited[absPath] {
			return fmt.Sprintf("<!-- Circular macro import omitted: %s -->", relPath)
		}
		visited[absPath] = true

		data, err := os.ReadFile(absPath)
		if err != nil {
			// If referenced file is missing, leave macro unexpanded or comment error
			return fmt.Sprintf("<!-- Error reading macro file %s: %v -->", relPath, err)
		}

		// Recursively expand macros in the included content
		expanded, err := expandMacrosRecursive(string(data), agentDir, visited, depth+1)
		if err != nil {
			return string(data)
		}
		return expanded
	})

	return result, nil
}
