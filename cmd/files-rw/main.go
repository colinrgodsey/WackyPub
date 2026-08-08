package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/colinrgodsey/WackyPubAI/pkg/filesrw"
)

var (
	readStart int
	readEnd   int

	editOld        string
	editNew        string
	editReplaceAll bool

	listLong      bool
	listAll       bool
	listRecursive bool
)

var rootCmd = &cobra.Command{
	Use:   "files-rw",
	Short: "Per-directory allowed file read/write/edit/patch/list tool for AI agents",
	Long: `files-rw provides an explicit, per-directory-scoped file manipulation tool suite
(read, write, edit, patch, list) for AI agents, gated by a FILES_RW_ACCESS allowlist file in the current working directory.`,
	SilenceUsage: true,
}

var readCmd = &cobra.Command{
	Use:   "read <path>",
	Short: "Read a text file with line numbers",
	Long:  "Read a text file with cat -n style line numbers, bounded by optional start/end 1-indexed line numbers.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		canon, err := access.Resolve(args[0], cwd, false)
		if err != nil {
			return err
		}
		out, err := filesrw.ReadFile(canon, readStart, readEnd)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	},
}

var writeCmd = &cobra.Command{
	Use:   "write <path>",
	Short: "Write content from standard input to a file atomically",
	Long:  "Write content provided on standard input atomically to the target path, creating any missing parent directories.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		canon, err := access.Resolve(args[0], cwd, true)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read content from stdin: %w", err)
		}
		return filesrw.WriteFile(canon, string(data))
	},
}

var editCmd = &cobra.Command{
	Use:   "edit <path>",
	Short: "Replace exact text in a file",
	Long:  "Replace an exact string in a text file with new content. Rejects zero or multiple matches unless --replace-all is specified.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("old") {
			return fmt.Errorf("--old string flag is required")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		canon, err := access.Resolve(args[0], cwd, true)
		if err != nil {
			return err
		}
		return filesrw.EditFile(canon, editOld, editNew, editReplaceAll)
	},
}

var patchCmd = &cobra.Command{
	Use:   "patch <path>",
	Short: "Apply a unified diff from standard input to a file",
	Long:  "Apply a unified diff passed on standard input to the specified target path.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		canon, err := access.Resolve(args[0], cwd, true)
		if err != nil {
			return err
		}
		diff, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read diff from stdin: %w", err)
		}
		return filesrw.PatchFile(canon, string(diff))
	},
}

var listCmd = &cobra.Command{
	Use:   "list [path]",
	Short: "List directory contents or file info",
	Long:  "List files in a directory or inspect a single path using ls-style output.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := "."
		if len(args) > 0 {
			target = args[0]
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		canon, err := access.Resolve(target, cwd, false)
		if err != nil {
			return err
		}
		out, err := filesrw.ListDir(canon, listLong, listAll, listRecursive)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	},
}

func init() {
	readCmd.Flags().IntVarP(&readStart, "start", "s", 0, "1-indexed starting line number")
	readCmd.Flags().IntVarP(&readEnd, "end", "e", 0, "1-indexed ending line number")

	editCmd.Flags().StringVar(&editOld, "old", "", "exact string to replace (required)")
	editCmd.Flags().StringVar(&editNew, "new", "", "replacement string")
	editCmd.Flags().BoolVar(&editReplaceAll, "replace-all", false, "replace all occurrences instead of requiring uniqueness")

	listCmd.Flags().BoolVarP(&listLong, "long", "l", false, "use long listing format")
	listCmd.Flags().BoolVarP(&listAll, "all", "a", false, "do not ignore entries starting with .")
	listCmd.Flags().BoolVarP(&listRecursive, "recursive", "R", false, "list subdirectories recursively")

	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(writeCmd)
	rootCmd.AddCommand(editCmd)
	rootCmd.AddCommand(patchCmd)
	rootCmd.AddCommand(listCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
