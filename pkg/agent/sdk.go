package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/genai"
)

// AgentSDK provides a clean, programmatic Go API for orchestrating folder-based agents.
type AgentSDK struct {
	WorkspaceDir string
	MaxToolTurns int
}

// NewSDK creates an SDK instance bound to a workspace directory.
func NewSDK(workspaceDir string) *AgentSDK {
	if workspaceDir == "" {
		workspaceDir = "."
	}
	return &AgentSDK{
		WorkspaceDir: workspaceDir,
		MaxToolTurns: 10,
	}
}

// AgentDir returns the absolute or relative path for an agent folder (<ws_dir>/<agent_id>).
func (s *AgentSDK) AgentDir(agentID string) string {
	return filepath.Join(s.WorkspaceDir, agentID)
}

// AddUserTurn appends a user message to <ws_dir>/<agent_id>/session.jsonl.
// Creates the agent directory automatically if it does not exist yet.
func (s *AgentSDK) AddUserTurn(agentID string, message string) error {
	if agentID == "" {
		return fmt.Errorf("agentID cannot be empty")
	}
	if message == "" {
		return fmt.Errorf("message cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return err
	}
	defer cleanup()

	agentDir := s.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("failed to create agent directory %s: %w", agentDir, err)
	}

	lock, err := AcquireSessionLock(agentDir)
	if err != nil {
		return fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer lock.Release()

	return AppendSessionTurn(agentDir, "user", message)
}

// GenerateTurn loads the folder agent, checks for compaction, generates the next assistant turn,
// prints to output if configured, and appends the assistant turn to session.jsonl.
func (s *AgentSDK) GenerateTurn(ctx context.Context, agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	agentDir := s.AgentDir(agentID)
	lock, err := AcquireSessionLock(agentDir)
	if err != nil {
		return "", fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer lock.Release()

	fa, err := LoadFolderAgent(s.WorkspaceDir, agentID, s.MaxToolTurns)
	if err != nil {
		return "", fmt.Errorf("failed to load agent %q: %w", agentID, err)
	}

	resp, err := fa.GenerateTurn(ctx)
	if err != nil {
		return "", fmt.Errorf("failed during turn generation for agent %q: %w", agentID, err)
	}

	return resp, nil
}

// AddAndGenerateTurn atomically appends a user message and generates the assistant response under a single lock.
func (s *AgentSDK) AddAndGenerateTurn(ctx context.Context, agentID string, userMessage string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agentID cannot be empty")
	}
	if userMessage == "" {
		return "", fmt.Errorf("userMessage cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	agentDir := s.AgentDir(agentID)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create agent directory %s: %w", agentDir, err)
	}

	lock, err := AcquireSessionLock(agentDir)
	if err != nil {
		return "", fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer lock.Release()

	// 1. Append User Turn
	if err := AppendSessionTurn(agentDir, "user", userMessage); err != nil {
		return "", fmt.Errorf("failed to append user turn: %w", err)
	}

	// 2. Load Folder Agent & Generate Assistant Turn
	fa, err := LoadFolderAgent(s.WorkspaceDir, agentID, s.MaxToolTurns)
	if err != nil {
		return "", fmt.Errorf("failed to load agent %q: %w", agentID, err)
	}

	resp, err := fa.GenerateTurn(ctx)
	if err != nil {
		return "", fmt.Errorf("failed during turn generation for agent %q: %w", agentID, err)
	}

	return resp, nil
}

// GetAgent loads and returns the FolderAgent object for low-level ADK runner interactions.
func (s *AgentSDK) GetAgent(agentID string) (*FolderAgent, error) {
	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return LoadFolderAgent(s.WorkspaceDir, agentID, s.MaxToolTurns)
}

// ListAgents returns the IDs of agent directories found directly under the
// workspace directory (see ListAgentIDs for how a directory is recognized as
// an agent). Does not acquire any lock - it only reads directory names.
func (s *AgentSDK) ListAgents() ([]string, error) {
	return ListAgentIDs(s.WorkspaceDir)
}

// InspectAgent reports the on-disk state of <ws_dir>/<agent_id>: which
// expected files are present, whether runtime.json parses, and
// session/memory stats. Safe to call on an agent that doesn't exist yet or
// is only partially set up - see AgentInspection.
//
// Deliberately does not go through ValidateAgentTarget's WACKYPUB_ALLOWED_AGENTS
// check (D16): that authorization boundary exists to gate cross-agent tool
// invocation/generation, not read-only diagnostic visibility - InspectAgent
// has no side effects and can't cause another agent to do anything. Gating
// it the same way surfaces an "unauthorized" failure as a generic parse/
// config error in wackypub workspace's summary table, which is actively
// misleading (see D16).
//
// Does not create the agent directory as a side effect (unlike most other
// AgentSDK methods) - if it doesn't exist, returns an AgentInspection with
// AgentDirExists false and every other field zero-valued.
//
// Deliberately does not acquire the session lock. AcquireSessionLock blocks
// until the lock is free, and InspectAgent is exactly the kind of call an
// agent's own tool loop can make against itself mid-generation (directly, or
// via wackypub workspace's no-arg summary, which inspects every agent
// including the caller) - since GenerateTurn already holds that same lock
// for the whole call, that blocking acquire deadlocks forever. Reading
// without the lock is safe: ReadSessionTurns already tolerates a torn read
// gracefully (see AgentInspection.SessionCorruptLines), and the lock's real
// job is serializing concurrent writers, not protecting readers.
func (s *AgentSDK) InspectAgent(agentID string) (*AgentInspection, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentID cannot be empty")
	}

	agentDir := s.AgentDir(agentID)
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		return &AgentInspection{AgentID: agentID, AgentDir: agentDir}, nil
	}

	return InspectAgentDir(s.WorkspaceDir, agentID)
}

// ReadSession returns all conversation turns logged in <ws_dir>/<agent_id>/session.jsonl.
func (s *AgentSDK) ReadSession(agentID string) ([]*genai.Content, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	// No session lock: ReadSessionTurns already tolerates a torn read
	// gracefully (skipped lines surface via SessionCorruptLines elsewhere),
	// and the blocking acquire here is exactly what can deadlock against an
	// agent's own already-held lock during live generation.
	agentDir := s.AgentDir(agentID)
	return ReadSessionTurns(agentDir)
}

// ReadMemory returns the current contents of <ws_dir>/<agent_id>/MEMORY.md.
func (s *AgentSDK) ReadMemory(agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// No session lock needed: MEMORY.md isn't session.jsonl, and this read
	// doesn't need protecting against the same writers that file's lock
	// serializes.
	agentDir := s.AgentDir(agentID)
	return ReadMemoryFile(agentDir)
}

// RenderSystemPrompt returns the fully rendered system prompt for an agent -
// AGENTS.md (or the generic fallback if it doesn't exist) after
// @<FILE_PATH> macro expansion. Does not construct a model and does not
// require runtime.json to exist or be valid - useful for validating
// AGENTS.md/macro output independently of backend configuration.
func (s *AgentSDK) RenderSystemPrompt(agentID string) (string, error) {
	if agentID == "" {
		return "", fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return "", err
	}
	defer cleanup()

	// No session lock needed: RenderAgentSystemPrompt reads AGENTS.md and
	// skills/, never session.jsonl - acquiring the lock here only added an
	// unnecessary blocking dependency on whatever else might be holding it.
	return RenderAgentSystemPrompt(s.WorkspaceDir, agentID)
}

// StripReasoningDetails permanently removes OpenRouter reasoning_details block
// metadata (e.g. encrypted/signed reasoning tied to a specific backend
// endpoint) from every turn in <ws_dir>/<agent_id>/session.jsonl, rewriting
// the file in place. Readable plain-text reasoning is left untouched. Useful
// when switching an agent from a model/endpoint that emits encrypted
// reasoning to a different one. Returns the number of turns that were
// modified.
func (s *AgentSDK) StripReasoningDetails(agentID string) (int, error) {
	if agentID == "" {
		return 0, fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	agentDir := s.AgentDir(agentID)
	lock, err := AcquireSessionLock(agentDir)
	if err != nil {
		return 0, fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer lock.Release()

	return StripSessionReasoningDetails(agentDir)
}

// CompactSession manually triggers session compaction evaluation for an agent.
func (s *AgentSDK) CompactSession(ctx context.Context, agentID string) (bool, error) {
	if agentID == "" {
		return false, fmt.Errorf("agentID cannot be empty")
	}

	cleanup, err := ValidateAgentTarget(agentID)
	if err != nil {
		return false, err
	}
	defer cleanup()

	agentDir := s.AgentDir(agentID)
	lock, err := AcquireSessionLock(agentDir)
	if err != nil {
		return false, fmt.Errorf("failed to acquire session lock: %w", err)
	}
	defer lock.Release()

	fa, err := LoadFolderAgent(s.WorkspaceDir, agentID, s.MaxToolTurns)
	if err != nil {
		return false, err
	}
	return CheckAndCompactSession(ctx, fa.AgentDir, fa.RuntimeConfig, fa.SystemPrompt, fa.Model)
}
