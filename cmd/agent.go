package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	adkAgent "github.com/colinrgodsey/WackyPubAI/pkg/agent"
)

var (
	messageFlag string
)

var agentCmd = &cobra.Command{
	Use:   "agent <agent_id>",
	Short: "Manage folder-based agent sessions (<ws_dir>/<agent_id>)",
	Long: `Manage agent sessions located in workspace folders (<ws_dir>/<agent_id>).
Supports adding user turns to session.jsonl and generating assistant responses powered by Google ADK.`,
}

// wackypub agent <agent_id> add [message] OR wackypub agent add <agent_id> [message]
var agentAddCmd = &cobra.Command{
	Use:   "add [agent_id] [message]",
	Short: "Add a user message turn to the agent session (<ws_dir>/<agent_id>/session.jsonl)",
	Long: `Appends a single user-role turn to <ws_dir>/<agent_id>/session.jsonl. Does not generate a
response - use "generate" afterward, or use "prompt" to do both atomically.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).
  message    The text to append as a user turn. Can also be supplied via the --message flag,
             or piped in on stdin (e.g. "echo hello | wackypub agent <agent_id> add"). Exactly
             one of these three must be provided.

Acquires the session lock for the duration of the append. Creates the agent directory if it
does not already exist.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		var userMsg string

		if len(args) >= 2 {
			agentID = args[0]
			userMsg = args[1]
		} else if len(args) == 1 {
			agentID = args[0]
			userMsg = messageFlag
		} else {
			userMsg = messageFlag
		}

		// If userMsg is empty, check stdin (piped input)
		if userMsg == "" {
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				reader := bufio.NewReader(os.Stdin)
				bytesInput, err := io.ReadAll(reader)
				if err == nil {
					userMsg = string(bytesInput)
				}
			}
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> add [message]")
		}
		if userMsg == "" {
			return fmt.Errorf("user message is required. Provide via argument, --message flag, or stdin pipe")
		}

		if err := sdk.AddUserTurn(agentID, userMsg); err != nil {
			return err
		}

		fmt.Printf("Added user message to agent %q session (%s/session.jsonl).\n", agentID, sdk.AgentDir(agentID))
		return nil
	},
}

// wackypub agent <agent_id> generate OR wackypub agent generate <agent_id>
var agentGenerateCmd = &cobra.Command{
	Use:   "generate [agent_id]",
	Short: "Generate the agent's turn from current session.jsonl using Google ADK",
	Long: `Loads the agent from <ws_dir>/<agent_id>, evaluates whether session compaction is needed
(based on runtime.json's contextWindow/sessionCompactPct - see docs/agents.md), then calls the
configured model with the system prompt, MEMORY.md, and current session.jsonl history, and
appends the resulting turn (including any reasoning/thinking part) to session.jsonl.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Prints the generated final-answer text to stdout (reasoning/thinking text is excluded from
what's printed, though it is still persisted to session.jsonl). Does not append a user turn
first - the session must already end on a user turn, or generation will be based on whatever
history currently exists. Use "prompt" to append a user turn and generate in one call.

Acquires the session lock for the duration of the operation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> generate")
		}

		ctx := context.Background()
		respText, err := sdk.GenerateTurn(ctx, agentID)
		if err != nil {
			return err
		}

		// Print generated assistant response turn to stdout
		fmt.Println(respText)
		return nil
	},
}

// wackypub agent <agent_id> strip-reasoning OR wackypub agent strip-reasoning <agent_id>
var agentStripReasoningCmd = &cobra.Command{
	Use:   "strip-reasoning [agent_id]",
	Short: "Permanently remove OpenRouter reasoning_details block metadata from an agent's session.jsonl",
	Long: `Permanently removes OpenRouter's structured reasoning_details block metadata (including
encrypted/signed reasoning tied to a specific backend endpoint) from every turn in
<ws_dir>/<agent_id>/session.jsonl, rewriting the file in place. Readable plain-text reasoning
is left untouched.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Useful when switching an agent from a model/endpoint that emits encrypted reasoning (e.g.
OpenRouter routing to an OpenAI model) to a different one - old encrypted blocks would
otherwise be rejected if ever replayed to a backend that didn't produce them.

Prints the number of turns that were modified. Acquires the session lock for the duration of
the rewrite.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> strip-reasoning")
		}

		modified, err := sdk.StripReasoningDetails(agentID)
		if err != nil {
			return err
		}

		fmt.Printf("Stripped reasoning_details metadata from %d turn(s) in agent %q session (%s/session.jsonl).\n", modified, agentID, sdk.AgentDir(agentID))
		return nil
	},
}

// wackypub agent <agent_id> read-session OR wackypub agent read-session <agent_id>
var agentReadSessionCmd = &cobra.Command{
	Use:   "read-session [agent_id]",
	Short: "Print the agent's session.jsonl turn history as JSON",
	Long: `Prints every turn currently stored in <ws_dir>/<agent_id>/session.jsonl to stdout, one
JSON-encoded genai.Content object per line (the same shape used in session.jsonl itself -
{"role": "user"|"model", "parts": [...]}).

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Read-only: does not modify session.jsonl. Acquires the session lock for the duration of the
read.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> read-session")
		}

		turns, err := sdk.ReadSession(agentID)
		if err != nil {
			return err
		}

		enc := json.NewEncoder(os.Stdout)
		for _, t := range turns {
			if err := enc.Encode(t); err != nil {
				return fmt.Errorf("failed to encode turn: %w", err)
			}
		}
		return nil
	},
}

// wackypub agent <agent_id> read-memory OR wackypub agent read-memory <agent_id>
var agentReadMemoryCmd = &cobra.Command{
	Use:   "read-memory [agent_id]",
	Short: "Print the agent's MEMORY.md contents",
	Long: `Prints the current contents of <ws_dir>/<agent_id>/MEMORY.md to stdout.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Prints nothing (empty output, no error) if the agent has no MEMORY.md yet. Read-only: does not
modify anything. Acquires the session lock for the duration of the read.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> read-memory")
		}

		mem, err := sdk.ReadMemory(agentID)
		if err != nil {
			return err
		}

		fmt.Println(mem)
		return nil
	},
}

// wackypub agent <agent_id> render-prompt OR wackypub agent render-prompt <agent_id>
var agentRenderPromptCmd = &cobra.Command{
	Use:   "render-prompt [agent_id]",
	Short: "Print the agent's fully rendered system prompt (AGENTS.md after macro expansion)",
	Long: `Reads <ws_dir>/<agent_id>/AGENTS.md (falling back to a generic "You are agent <id>."
prompt if it doesn't exist) and expands @<FILE_PATH> macros, then prints the fully rendered
result to stdout - exactly the text that gets folded into the first turn of every generation
request (see docs/agents.md §3 MEMORY.md).

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Useful for validating AGENTS.md/macro output on its own: this command does not construct a
model and does not require runtime.json to exist or be valid, so it works even for an agent
whose backend isn't configured yet.

Read-only: does not modify anything.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> render-prompt")
		}

		prompt, err := sdk.RenderSystemPrompt(agentID)
		if err != nil {
			return err
		}

		fmt.Println(prompt)
		return nil
	},
}

// wackypub agent <agent_id> compact OR wackypub agent compact <agent_id>
var agentCompactCmd = &cobra.Command{
	Use:   "compact [agent_id]",
	Short: "Manually evaluate and, if needed, perform session compaction",
	Long: `Evaluates whether the agent's session exceeds the contextWindow token threshold configured
in runtime.json and, if so, performs the same compaction that "generate"/"prompt" would trigger
automatically: summarizes the oldest sessionCompactPct of turns into MEMORY.md and removes them
from session.jsonl. If the session is under the threshold, or contextWindow is 0/unset, this is
a no-op - it never errors just because compaction wasn't needed.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).

Prints whether compaction actually ran. Acquires the session lock for the duration of the
operation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		if len(args) >= 1 {
			agentID = args[0]
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> compact")
		}

		ctx := context.Background()
		compacted, err := sdk.CompactSession(ctx, agentID)
		if err != nil {
			return err
		}

		if compacted {
			fmt.Printf("Compacted agent %q session (%s/session.jsonl); MEMORY.md updated.\n", agentID, sdk.AgentDir(agentID))
		} else {
			fmt.Printf("No compaction needed for agent %q (session is under the contextWindow threshold, or contextWindow is unset).\n", agentID)
		}
		return nil
	},
}

// wackypub agent <agent_id> prompt [message] OR wackypub agent prompt <agent_id> [message]
var agentPromptCmd = &cobra.Command{
	Use:   "prompt [agent_id] [message]",
	Short: "Atomically append user message and generate agent response under a single lock",
	Long: `Appends a user-role turn and generates the assistant response in one call, holding the
session lock for both steps - the recommended way to drive an agent turn, since it can't race
with another process appending a turn in between the two steps the way separate "add" +
"generate" calls could.

Arguments:
  agent_id   Required. Identifies the agent directory (<ws_dir>/<agent_id>).
  message    The user turn's text. Can also be supplied via the --message flag, or piped in on
             stdin. Exactly one of these three must be provided.

Prints the generated final-answer text to stdout (reasoning/thinking text is excluded from
what's printed, though it is still persisted to session.jsonl).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir := GetWorkspaceDir()
		sdk := adkAgent.NewSDK(wsDir)

		var agentID string
		var userMsg string

		if len(args) >= 2 {
			agentID = args[0]
			userMsg = args[1]
		} else if len(args) == 1 {
			agentID = args[0]
			userMsg = messageFlag
		} else {
			userMsg = messageFlag
		}

		if userMsg == "" {
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) == 0 {
				reader := bufio.NewReader(os.Stdin)
				bytesInput, err := io.ReadAll(reader)
				if err == nil {
					userMsg = string(bytesInput)
				}
			}
		}

		if agentID == "" {
			return fmt.Errorf("agent_id is required. Usage: wackypub agent <agent_id> prompt [message]")
		}
		if userMsg == "" {
			return fmt.Errorf("user message is required. Provide via argument, --message flag, or stdin pipe")
		}

		ctx := context.Background()
		respText, err := sdk.AddAndGenerateTurn(ctx, agentID, userMsg)
		if err != nil {
			return err
		}

		fmt.Println(respText)
		return nil
	},
}

// ExecuteAgentDispatcher handles positional "wackypub agent <agent_id> <add|generate|prompt|...>" syntax.
func executeAgentDispatcher(cmd *cobra.Command, args []string) error {
	if len(args) >= 2 {
		agentID := args[0]
		subCmd := args[1]

		if subCmd == "add" {
			remainingArgs := []string{agentID}
			if len(args) > 2 {
				remainingArgs = append(remainingArgs, args[2:]...)
			}
			return agentAddCmd.RunE(cmd, remainingArgs)
		} else if subCmd == "generate" {
			return agentGenerateCmd.RunE(cmd, []string{agentID})
		} else if subCmd == "prompt" || subCmd == "turn" {
			remainingArgs := []string{agentID}
			if len(args) > 2 {
				remainingArgs = append(remainingArgs, args[2:]...)
			}
			return agentPromptCmd.RunE(cmd, remainingArgs)
		} else if subCmd == "strip-reasoning" {
			return agentStripReasoningCmd.RunE(cmd, []string{agentID})
		} else if subCmd == "read-session" {
			return agentReadSessionCmd.RunE(cmd, []string{agentID})
		} else if subCmd == "read-memory" {
			return agentReadMemoryCmd.RunE(cmd, []string{agentID})
		} else if subCmd == "render-prompt" {
			return agentRenderPromptCmd.RunE(cmd, []string{agentID})
		} else if subCmd == "compact" {
			return agentCompactCmd.RunE(cmd, []string{agentID})
		}
	}

	return cmd.Help()
}

func init() {
	// -m is intentionally not used here: RootCmd already binds it to --model (see cmd/root.go).
	// A local -m shorthand on this flagset would collide with that persistent flag and cobra
	// panics on the collision as soon as --help (or completion) merges the two flag sets.
	agentAddCmd.Flags().StringVar(&messageFlag, "message", "", "User message content")
	agentPromptCmd.Flags().StringVar(&messageFlag, "message", "", "User message content")

	agentCmd.RunE = executeAgentDispatcher

	agentCmd.AddCommand(agentAddCmd)
	agentCmd.AddCommand(agentGenerateCmd)
	agentCmd.AddCommand(agentPromptCmd)
	agentCmd.AddCommand(agentStripReasoningCmd)
	agentCmd.AddCommand(agentReadSessionCmd)
	agentCmd.AddCommand(agentReadMemoryCmd)
	agentCmd.AddCommand(agentRenderPromptCmd)
	agentCmd.AddCommand(agentCompactCmd)

	RootCmd.AddCommand(agentCmd)
}
