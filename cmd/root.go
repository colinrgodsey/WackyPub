package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	adkAgent "github.com/colinrgodsey/WackyPubAI/pkg/agent"
	"github.com/colinrgodsey/WackyPubAI/pkg/config"
)

var (
	cfgFile      string
	workspaceDir string
	modelName    string
	apiKey       string
	maxToolTurns int
	skillFlagVal string
	cfg          *config.Config

	// BundledA2ASkill holds embedded skills/wackypub-a2a/SKILL.md text passed from main.go (D34).
	BundledA2ASkill string
	// BundledWSSkill holds embedded skills/wackypub-ws/SKILL.md text passed from main.go (D34).
	BundledWSSkill string
)

// GetSkillContent resolves and returns skill guidance by name (a2a, ws).
func GetSkillContent(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	switch name {
	case "a2a", "wackypub-a2a":
		return BundledA2ASkill, nil
	case "ws", "workspace", "wackypub-ws":
		return BundledWSSkill, nil
	case "", "all":
		return BundledA2ASkill, nil
	default:
		return "", fmt.Errorf("unknown skill %q. Available skills: a2a, ws", name)
	}
}

var skillCmd = &cobra.Command{
	Use:   "skill [a2a|ws]",
	Short: "Print bundled WackyPub skill guidance (a2a, ws) to stdout and exit",
	Long:  "Prints embedded skill guidance (skills/wackypub-a2a/SKILL.md or skills/wackypub-ws/SKILL.md) directly to stdout.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		content, err := GetSkillContent(name)
		if err != nil {
			return err
		}
		fmt.Print(content)
		return nil
	},
}

// RootCmd represents the base command when called without any subcommands.
var RootCmd = &cobra.Command{
	Use:   "wackypub",
	Short: "WackyPubAI - Folder-based Agent Management CLI powered by Google ADK",
	Long: `WackyPubAI is a command-line tool and Go SDK for managing folder-based AI agents.

Built on top of Google Agent Development Kit (ADK) in Go, WackyPubAI supports workspace agent directories,
OpenAI-compatible model adapters, macro prompt inclusion (@<FILE_PATH>), and auto-compaction.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("skill") {
			content, err := GetSkillContent(skillFlagVal)
			if err != nil {
				return err
			}
			fmt.Print(content)
			return nil
		}
		return cmd.Help()
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Flags().Changed("skill") {
			content, err := GetSkillContent(skillFlagVal)
			if err != nil {
				return err
			}
			fmt.Print(content)
			os.Exit(0)
		}

		var err error
		cfgPath := cfgFile
		if cfgPath == "" {
			cfgPath = "wackypub.yaml"
		}

		cfg, err = config.LoadConfig(cfgPath)
		if err != nil {
			// Ignore if wackypub.yaml doesn't exist for agent CLI operations
			cfg = config.DefaultConfig()
		}

		if modelName != "" {
			cfg.DefaultModel = modelName
		}
		if apiKey != "" {
			cfg.APIKey = apiKey
		}

		return nil
	},
}

// GetWorkspaceDir returns the resolved workspace directory according to D15.
func GetWorkspaceDir() (string, error) {
	isExplicit := RootCmd.PersistentFlags().Changed("ws")
	return adkAgent.ResolveWorkspaceDir(workspaceDir, isExplicit)
}

// GetMaxToolTurns returns the configured max tool turns limit.
func GetMaxToolTurns() int {
	return maxToolTurns
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "wackypub.yaml", "config file path")
	RootCmd.PersistentFlags().StringVar(&workspaceDir, "ws", ".", "workspace directory path (defaults to current working directory)")
	RootCmd.PersistentFlags().StringVarP(&modelName, "model", "m", "", "Gemini model override (e.g. gemini-2.5-flash)")
	RootCmd.PersistentFlags().StringVar(&apiKey, "api-key", "", "Gemini API key override (or GEMINI_API_KEY env var)")
	RootCmd.PersistentFlags().IntVar(&maxToolTurns, "max-tool-turns", adkAgent.DefaultMaxToolTurns, "Maximum consecutive tool-call turns allowed per generation")
	RootCmd.PersistentFlags().StringVar(&skillFlagVal, "skill", "", "Print bundled WackyPub skill guidance (a2a|ws) to stdout and exit")
	RootCmd.PersistentFlags().Lookup("skill").NoOptDefVal = "a2a"
	RootCmd.AddCommand(skillCmd)
}
