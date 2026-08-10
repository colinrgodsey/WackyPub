package cmd

import (
	"fmt"
	"os"

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
	showSkill    bool
	cfg          *config.Config

	// BundledSkill holds the embedded skills/wackypub/SKILL.md text passed from main.go (D34).
	BundledSkill string
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Print the bundled wackypub skill (SKILL.md) to stdout and exit",
	Long:  "Prints the embedded wackypub skill guidance (skills/wackypub/SKILL.md) directly to stdout.",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Print(BundledSkill)
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
		if showSkill {
			fmt.Print(BundledSkill)
			return nil
		}
		return cmd.Help()
	},
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if showSkill {
			fmt.Print(BundledSkill)
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
	RootCmd.PersistentFlags().BoolVar(&showSkill, "skill", false, "Print the bundled wackypub skill (SKILL.md) to stdout and exit")
	RootCmd.AddCommand(skillCmd)
}
