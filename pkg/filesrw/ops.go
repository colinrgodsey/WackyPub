package filesrw

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isBinary is a cheap heuristic (matches most agent-harness Read tools):
// a NUL byte anywhere in the sampled content means "not text".
func isBinary(data []byte) bool {
	sample := data
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	return bytes.IndexByte(sample, 0) != -1
}

// ReadFile returns canonPath's content as newline-joined "<line>\t<text>"
// entries (cat -n style), optionally restricted to the inclusive 1-indexed
// [start, end] line range. start/end of 0 means "unbounded" on that side.
func ReadFile(canonPath string, start, end int) (string, error) {
	data, err := os.ReadFile(canonPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", canonPath, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s looks like a binary file - refusing to read it as text", canonPath)
	}

	lines := strings.Split(string(data), "\n")
	// A trailing newline produces one spurious empty final element; drop it
	// so line numbers match what's actually in the file.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	from := 1
	to := len(lines)
	if start > 0 {
		from = start
	}
	if end > 0 {
		to = end
	}
	if from < 1 {
		from = 1
	}
	if to > len(lines) {
		to = len(lines)
	}
	if from > to {
		return "", nil
	}

	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%d\t%s\n", i, lines[i-1])
	}
	return b.String(), nil
}

// WriteFile atomically overwrites (or creates) canonPath with content,
// creating any missing parent directories first. Atomic via write-to-temp +
// rename, so a crash mid-write never leaves a corrupted/partial file behind.
func WriteFile(canonPath, content string) error {
	dir := filepath.Dir(canonPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".files-rw-write-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write %s: %w", canonPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write %s: %w", canonPath, err)
	}

	if err := os.Rename(tmpPath, canonPath); err != nil {
		return fmt.Errorf("failed to finalize write to %s: %w", canonPath, err)
	}
	return nil
}

// EditFile replaces oldStr with newStr in canonPath. Unless replaceAll is
// set, oldStr must appear exactly once - zero or multiple matches are
// rejected rather than guessed at, so the caller can supply more
// surrounding context instead of silently editing the wrong occurrence.
func EditFile(canonPath, oldStr, newStr string, replaceAll bool) error {
	if oldStr == "" {
		return fmt.Errorf("old string must not be empty")
	}
	data, err := os.ReadFile(canonPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", canonPath, err)
	}
	if isBinary(data) {
		return fmt.Errorf("%s looks like a binary file - refusing to edit it as text", canonPath)
	}
	content := string(data)

	count := strings.Count(content, oldStr)
	if count == 0 {
		return fmt.Errorf("old string not found in %s", canonPath)
	}
	if count > 1 && !replaceAll {
		return fmt.Errorf("old string appears %d times in %s - supply more surrounding context to make it unique, or pass --replace-all", count, canonPath)
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(content, oldStr, newStr)
	} else {
		updated = strings.Replace(content, oldStr, newStr, 1)
	}

	return WriteFile(canonPath, updated)
}

// PatchFile applies a unified diff (read from diff) to canonPath by
// shelling out to the system `patch` binary, writing its output to a temp
// file first and renaming over canonPath only on success - so a malformed
// or partially-applying diff never leaves canonPath half-patched.
func PatchFile(canonPath string, diff string) error {
	if _, err := exec.LookPath("patch"); err != nil {
		return fmt.Errorf("the \"patch\" command is not available on PATH: %w", err)
	}

	dir := filepath.Dir(canonPath)
	tmp, err := os.CreateTemp(dir, ".files-rw-patch-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("patch", "-o", tmpPath, canonPath)
	cmd.Stdin = strings.NewReader(diff)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("patch failed: %w: %s", err, stderr.String())
	}

	if err := os.Rename(tmpPath, canonPath); err != nil {
		return fmt.Errorf("failed to finalize patch to %s: %w", canonPath, err)
	}
	return nil
}

// ListDir shells out to the system `ls` for canonPath, passing through only
// a fixed set of recognized boolean flags (no raw argv passthrough, since
// that would let extra positional path arguments slip past access control).
func ListDir(canonPath string, long, all, recursive bool) (string, error) {
	if _, err := exec.LookPath("ls"); err != nil {
		return "", fmt.Errorf("the \"ls\" command is not available on PATH: %w", err)
	}

	var flags []string
	if long {
		flags = append(flags, "-l")
	}
	if all {
		flags = append(flags, "-a")
	}
	if recursive {
		flags = append(flags, "-R")
	}
	args := append(flags, "--", canonPath)

	cmd := exec.Command("ls", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ls failed: %w: %s", err, stderr.String())
	}
	return stdout.String(), nil
}
