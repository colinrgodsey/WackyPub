package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/colinrgodsey/WackyPubAI/pkg/filesrw"
)

var (
	readStart   int
	readEnd     int
	readNumbers bool

	editOld        string
	editNew        string
	editReplaceAll bool

	listLong      bool
	listAll       bool
	listRecursive bool
)

var rootCmd = &cobra.Command{
	Use:   "files-rw",
	Short: "Per-directory allowed file read/write/edit/patch/copy/move/delete/list tool for AI agents",
	Long: `files-rw provides an explicit, per-directory-scoped file manipulation tool suite
(read, write, edit, patch, copy, move, delete, list) for AI agents, gated by a FILES_RW_ACCESS allowlist file in the current working directory.`,
	SilenceUsage: true,
}

var readCmd = &cobra.Command{
	Use:   "read <path>",
	Short: "Read a text file",
	Long:  "Read a text file. Output defaults to raw unnumbered bytes. Pass -n/--numbers for cat -n style line numbers (useful to reference line numbers before constructing edit/patch calls). Subject to a hard read size limit (200KB); use --start and --end for line-based pagination.",
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
		out, err := filesrw.ReadFile(access, args[0], cwd, readStart, readEnd, readNumbers)
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
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read content from stdin: %w", err)
		}
		return filesrw.WriteFile(access, args[0], cwd, string(data))
	},
}

var copyCmd = &cobra.Command{
	Use:   "copy <src> <dst>",
	Short: "Copy a file from src to dst",
	Long:  "Copy a file from src to dst. Requires read access on src and write access on dst.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		return filesrw.CopyFile(access, args[0], args[1], cwd)
	},
}

var moveCmd = &cobra.Command{
	Use:   "move <src> <dst>",
	Short: "Move or rename a file from src to dst",
	Long:  "Move or rename a file from src to dst. Requires write access on both src and dst.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get working directory: %w", err)
		}
		access, err := filesrw.LoadAccess(cwd)
		if err != nil {
			return err
		}
		return filesrw.MoveFile(access, args[0], args[1], cwd)
	},
}

var deleteCmd = &cobra.Command{
	Use:   "delete <path>",
	Short: "Delete a file",
	Long:  "Delete a file at path. Requires write access on path.",
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
		return filesrw.DeleteFile(access, args[0], cwd)
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
		return filesrw.EditFile(access, args[0], cwd, editOld, editNew, editReplaceAll)
	},
}

var patchCmd = &cobra.Command{
	Use:   "patch <path>",
	Short: "Apply a unified diff from standard input to a file",
	Long:  "Apply a unified diff passed on standard input to the specified target path. Only unified diffs (containing '---', '+++', and '@@' headers) are accepted. Read the target file with read -n first to obtain accurate line numbers.",
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
		diff, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read diff from stdin: %w", err)
		}
		return filesrw.PatchFile(access, args[0], cwd, string(diff))
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
		out, err := filesrw.ListDir(access, target, cwd, listLong, listAll, listRecursive)
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
	readCmd.Flags().BoolVarP(&readNumbers, "numbers", "n", false, "format output with cat -n style line numbers (useful before construct edit/patch)")

	editCmd.Flags().StringVar(&editOld, "old", "", "exact string to replace (required)")
	editCmd.Flags().StringVar(&editNew, "new", "", "replacement string")
	editCmd.Flags().BoolVar(&editReplaceAll, "replace-all", false, "replace all occurrences instead of requiring uniqueness")

	listCmd.Flags().BoolVarP(&listLong, "long", "l", false, "use long listing format")
	listCmd.Flags().BoolVarP(&listAll, "all", "a", false, "do not ignore entries starting with .")
	listCmd.Flags().BoolVarP(&listRecursive, "recursive", "R", false, "list subdirectories recursively")

	rootCmd.AddCommand(readCmd)
	rootCmd.AddCommand(writeCmd)
	rootCmd.AddCommand(copyCmd)
	rootCmd.AddCommand(moveCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(editCmd)
	rootCmd.AddCommand(patchCmd)
	rootCmd.AddCommand(listCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
