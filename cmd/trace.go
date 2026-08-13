package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	adkAgent "github.com/colinrgodsey/WackyPub/pkg/agent"
)

var (
	traceMaxSteps  int
	traceVerbosity int
)

var traceCmd = &cobra.Command{
	Use:   "trace [<agent_id> <commit> | <trace_id>]",
	Short: "Causal graph trace across multi-agent commit histories",
	Long: `Traces backward step-by-step through multi-agent commit history according to D36.

Usage Modes:
  wackypub trace <agent_id> <commit>  Trace backward starting from <commit> in <agent_id>'s repository.
  wackypub trace <trace_id>           Search across all agent repositories for <trace_id> and trace backward.

Flags:
  -n, --max-steps <int>   Maximum trace steps to traverse (default 20).
  -v, --verbosity <int>   Verbosity level 0..4 (default 1):
                            0: Minimal (event types, function call names, user prompt text)
                            1: Compact Default (event type, tool names, user text, assistant text)
                            2: Clean Full (complete text, stripped of thinking blocks & signatures)
                            3: Full with Thinking (includes thinking blocks, stripped of signatures)
                            4: Raw JSONL (dumps raw commit messages & A2A payloads as-is)`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		wsDir, err := GetWorkspaceDir()
		if err != nil {
			return err
		}

		sdk := adkAgent.NewSDK(wsDir)
		opts := adkAgent.TraceOptions{
			MaxSteps:  traceMaxSteps,
			Verbosity: traceVerbosity,
		}

		var res *adkAgent.TraceResult

		if len(args) == 1 {
			// Single argument: trace_id
			traceID := args[0]
			res, err = sdk.Trace("", "", traceID, opts)
			if err != nil {
				return err
			}
		} else {
			// Two arguments: agent_id, commit
			agentID := args[0]
			commitSpec := args[1]
			res, err = sdk.Trace(agentID, commitSpec, "", opts)
			if err != nil {
				return err
			}
		}

		output := adkAgent.FormatTraceResult(wsDir, res, opts)
		fmt.Print(output)
		return nil
	},
}

func init() {
	traceCmd.Flags().IntVarP(&traceMaxSteps, "max-steps", "n", 20, "Maximum trace steps to traverse")
	traceCmd.Flags().IntVarP(&traceVerbosity, "verbosity", "v", 1, "Verbosity level 0..4 (0: minimal, 1: compact, 2: clean full, 3: full+thinking, 4: raw)")
	RootCmd.AddCommand(traceCmd)
}
