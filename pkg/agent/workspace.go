package agent

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
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
