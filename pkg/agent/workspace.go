package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	RootMarkerFile    = "WACKYPUB_ROOT"
	AllowedAgentsFile = "WACKYPUB_ALLOWED_AGENTS"
	CallChainEnvVar   = "WACKYPUB_CALL_CHAIN"
	ToolsDirName      = "tools"
)

// agentDirSignals are the files whose presence (directly inside a directory)
// marks that directory as an agent directory rather than something else
// living under the workspace (e.g. a shared runtimes/ folder holding
// runtime.json variants to symlink from - see .agents/LOCAL_TESTING.md).
var agentDirSignals = []string{"AGENTS.md", "runtime.json", "session.jsonl"}

// ListAgentIDs returns the names of subdirectories of wsDir that look like
// agent directories - see agentDirSignals. Returned in sorted order. Returns
// an empty (nil) slice without error if wsDir does not exist.
func ListAgentIDs(wsDir string) ([]string, error) {
	entries, err := os.ReadDir(wsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if looksLikeAgentDir(filepath.Join(wsDir, e.Name())) {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func looksLikeAgentDir(agentDir string) bool {
	for _, name := range agentDirSignals {
		if pathExists(filepath.Join(agentDir, name)) {
			return true
		}
	}
	return false
}

// pathExists reports whether path exists, without following a symlink (a
// broken symlink still counts as present for signaling purposes).
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// AgentInspection reports the on-disk state of a single agent directory:
// which expected files are present, whether runtime.json parses, and basic
// session/memory stats. Intended for diagnosing what a workspace still
// needs without requiring prior knowledge of the file layout - see the
// `workspace` CLI command and AgentSDK.InspectAgent.
type AgentInspection struct {
	AgentID  string
	AgentDir string

	// AgentDirExists is false when AgentDir doesn't exist yet - every other
	// field is zero-valued in that case.
	AgentDirExists bool

	AgentsMDExists bool
	MemoryMDExists bool
	DotEnvExists   bool

	RuntimeJSONExists    bool
	RuntimeJSONIsSymlink bool
	// RuntimeJSONResolved is the symlink target's real path, only set when
	// RuntimeJSONIsSymlink is true and it resolves.
	RuntimeJSONResolved string
	RuntimeJSONValid    bool
	// RuntimeJSONError holds LoadRuntimeConfig's error message when
	// RuntimeJSONExists is true but RuntimeJSONValid is false.
	RuntimeJSONError string
	// RuntimeConfig is non-nil only when RuntimeJSONValid is true.
	RuntimeConfig *RuntimeConfig

	SessionJSONLExists bool
	// SessionTurnCount is the number of turns ReadSessionTurns successfully
	// parsed.
	SessionTurnCount int
	// SessionCorruptLines is the number of non-empty lines in session.jsonl
	// that ReadSessionTurns silently skipped because they didn't parse as a
	// genai.Content - see .agents/AGENTS.md's session.jsonl corruption
	// gotcha. Zero in the common case.
	SessionCorruptLines int
	AllowedAgentsExists bool
	AllowedAgents       []string

	ToolsDirExists  bool
	DiscoveredTools []string
	ShadowedTools   []string

	SkillsDirExists  bool
	DiscoveredSkills []string
	ShadowedSkills   []string
}

// ResolveWorkspaceDir resolves the workspace directory according to D15:
// - If isExplicit is true (--ws was explicitly specified), wsFlag must contain RootMarkerFile directly.
// - If isExplicit is false (default), walk up from CWD looking for RootMarkerFile. Error if not found.
func ResolveWorkspaceDir(wsFlag string, isExplicit bool) (string, error) {
	if isExplicit {
		clean := filepath.Clean(wsFlag)
		if !pathExists(filepath.Join(clean, RootMarkerFile)) {
			return "", fmt.Errorf("workspace directory %q does not contain %s marker file", wsFlag, RootMarkerFile)
		}
		return clean, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	dir := cwd
	for {
		if pathExists(filepath.Join(dir, RootMarkerFile)) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s marker file found in current directory (%s) or any parent directory", RootMarkerFile, cwd)
		}
		dir = parent
	}
}

// ValidateAgentTarget performs cross-agent authorization (AllowedAgentsFile against CWD)
// and deadlock prevention (CallChainEnvVar) according to D16.
// Returns a cleanup function that restores CallChainEnvVar to its previous state.
func ValidateAgentTarget(targetAgentID string) (func(), error) {
	noopCleanup := func() {}
	if targetAgentID == "" {
		return noopCleanup, nil
	}

	// 1. Authorization check: AllowedAgentsFile against CWD (before resolving workspace root)
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	dir := cwd
	for {
		if pathExists(filepath.Join(dir, RootMarkerFile)) {
			break
		}

		allowedPath := filepath.Join(dir, AllowedAgentsFile)
		if pathExists(allowedPath) {
			allowed, err := readAllowedAgents(allowedPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read %s: %w", allowedPath, err)
			}
			authorized := false
			for _, id := range allowed {
				if id == targetAgentID {
					authorized = true
					break
				}
			}
			if !authorized {
				return nil, fmt.Errorf("agent %q is not in %s allowlist for current agent directory %q", targetAgentID, AllowedAgentsFile, dir)
			}
			break
		}

		if looksLikeAgentDir(dir) {
			return nil, fmt.Errorf("access to agent %q denied: current agent directory %q has no %s allowlist", targetAgentID, dir, AllowedAgentsFile)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// 2. Deadlock cycle check & A2A Metadata parsing (D16, D33)
	origA2AEnv := os.Getenv(Agent2AgentEnvVar)
	origChainEnv := os.Getenv(CallChainEnvVar)

	meta, err := ParseA2AMetadata()
	if err != nil {
		return nil, fmt.Errorf("failed to parse A2A metadata: %w", err)
	}

	for _, id := range meta.CallChain {
		if id == targetAgentID {
			return nil, fmt.Errorf("agent %q is already in call chain (%s); operation rejected to prevent deadlock cycle", targetAgentID, strings.Join(meta.CallChain, ","))
		}
	}

	// 3. Update AGENT2AGENT & legacy WACKYPUB_CALL_CHAIN in environment
	callerID := ""
	if len(meta.CallChain) > 0 {
		callerID = meta.CallChain[len(meta.CallChain)-1]
	}

	newChain := append(append([]string{}, meta.CallChain...), targetAgentID)
	traceID := meta.TraceID
	if traceID == "" {
		traceID = GenerateTraceID()
	}

	newMeta := &A2AMetadata{
		CallerID:  callerID,
		CallChain: newChain,
		TraceID:   traceID,
		Metadata:  meta.Metadata,
	}

	denseJSON, err := newMeta.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode %s: %w", Agent2AgentEnvVar, err)
	}

	os.Setenv(Agent2AgentEnvVar, denseJSON)
	os.Setenv(CallChainEnvVar, strings.Join(newChain, ","))

	cleanup := func() {
		os.Setenv(Agent2AgentEnvVar, origA2AEnv)
		os.Setenv(CallChainEnvVar, origChainEnv)
	}

	return cleanup, nil
}

func readAllowedAgents(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var result []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result = append(result, line)
	}
	return result, scanner.Err()
}

// DiscoverAgentToolsMap walks <agentDir>/tools/ recursively for executable files according to D14.
// Resolves directory and file symlinks and follows them, preventing infinite symlink cycles.
// Returns a map of tool name -> file path, discovered unique tool names, shadowing warning messages, and error.
func DiscoverAgentToolsMap(agentDir string) (map[string]string, []string, []string, error) {
	toolsDir := filepath.Join(agentDir, ToolsDirName)
	if !pathExists(toolsDir) {
		return nil, nil, nil, nil
	}

	toolMap := make(map[string]string) // tool name -> file path
	var discovered []string
	var shadowed []string
	visitedDirs := make(map[string]bool)

	var walk func(dir string) error
	walk = func(dir string) error {
		realDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return nil // Skip unresolvable directory symlinks
		}
		if visitedDirs[realDir] {
			return nil // Prevent cycle
		}
		visitedDirs[realDir] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			info, err := os.Stat(path) // os.Stat follows symlinks!
			if err != nil {
				continue // Skip broken symlinks or unreadable files
			}

			if info.IsDir() {
				if err := walk(path); err != nil {
					return err
				}
			} else if info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				name := entry.Name()
				if existingPath, exists := toolMap[name]; exists {
					shadowed = append(shadowed, fmt.Sprintf("tool %q at %s is shadowed by %s", name, existingPath, path))
				} else {
					discovered = append(discovered, name)
				}
				toolMap[name] = path
			}
		}
		return nil
	}

	if err := walk(toolsDir); err != nil {
		return nil, nil, nil, err
	}

	sort.Strings(discovered)
	return toolMap, discovered, shadowed, nil
}

// DiscoverAgentTools walks <agentDir>/tools/ recursively for executable files according to D14.
// Returns discovered unique tool names and shadowing warning messages.
func DiscoverAgentTools(agentDir string) ([]string, []string, error) {
	_, discovered, shadowed, err := DiscoverAgentToolsMap(agentDir)
	return discovered, shadowed, err
}

// InspectAgentDir builds an AgentInspection for <wsDir>/<agentID> without
// acquiring the session lock - callers that need a consistent snapshot
// alongside concurrent writers should hold the lock themselves (see
// AgentSDK.InspectAgent, which does). Safe to call even if the agent
// directory or any of its expected files don't exist.
func InspectAgentDir(wsDir, agentID string) (*AgentInspection, error) {
	agentDir := filepath.Join(wsDir, agentID)
	insp := &AgentInspection{AgentID: agentID, AgentDir: agentDir}

	if _, err := os.Stat(agentDir); err != nil {
		if os.IsNotExist(err) {
			return insp, nil
		}
		return nil, err
	}
	insp.AgentDirExists = true

	insp.AgentsMDExists = pathExists(filepath.Join(agentDir, "AGENTS.md"))
	insp.MemoryMDExists = pathExists(filepath.Join(agentDir, "MEMORY.md"))
	insp.DotEnvExists = pathExists(filepath.Join(agentDir, ".env"))

	allowedPath := filepath.Join(agentDir, AllowedAgentsFile)
	if pathExists(allowedPath) {
		insp.AllowedAgentsExists = true
		allowed, err := readAllowedAgents(allowedPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", allowedPath, err)
		}
		insp.AllowedAgents = allowed
	}

	toolsDir := filepath.Join(agentDir, ToolsDirName)
	if pathExists(toolsDir) {
		insp.ToolsDirExists = true
		discovered, shadowed, err := DiscoverAgentTools(agentDir)
		if err != nil {
			return nil, fmt.Errorf("failed to discover tools in %s: %w", toolsDir, err)
		}
		insp.DiscoveredTools = discovered
		insp.ShadowedTools = shadowed
	}

	skillsDir := filepath.Join(agentDir, SkillsDirName)
	if pathExists(skillsDir) {
		insp.SkillsDirExists = true
		_, onDemand, alwaysLoaded, shadowed, err := DiscoverAgentSkillsMap(agentDir)
		if err != nil {
			return nil, fmt.Errorf("failed to discover skills in %s: %w", skillsDir, err)
		}
		var skillNames []string
		for _, sk := range onDemand {
			skillNames = append(skillNames, sk.Name)
		}
		for _, sk := range alwaysLoaded {
			skillNames = append(skillNames, sk.Name+" (always_load)")
		}
		insp.DiscoveredSkills = skillNames
		insp.ShadowedSkills = shadowed
	}

	runtimePath := filepath.Join(agentDir, "runtime.json")
	if fi, err := os.Lstat(runtimePath); err == nil {
		insp.RuntimeJSONExists = true
		insp.RuntimeJSONIsSymlink = fi.Mode()&os.ModeSymlink != 0
		if insp.RuntimeJSONIsSymlink {
			if resolved, err := filepath.EvalSymlinks(runtimePath); err == nil {
				insp.RuntimeJSONResolved = resolved
			}
		}
		if cfg, err := LoadRuntimeConfig(agentDir); err != nil {
			insp.RuntimeJSONError = err.Error()
		} else {
			insp.RuntimeJSONValid = true
			insp.RuntimeConfig = cfg
		}
	}

	sessionPath := filepath.Join(agentDir, "session.jsonl")
	if pathExists(sessionPath) {
		insp.SessionJSONLExists = true

		turns, err := ReadSessionTurns(agentDir)
		if err != nil {
			return nil, err
		}
		insp.SessionTurnCount = len(turns)

		totalLines, err := countNonEmptyLines(sessionPath)
		if err != nil {
			return nil, err
		}
		insp.SessionCorruptLines = totalLines - len(turns)
	}

	return insp, nil
}

func countNonEmptyLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	count := 0
	for scanner.Scan() {
		if len(scanner.Bytes()) > 0 {
			count++
		}
	}
	return count, scanner.Err()
}
