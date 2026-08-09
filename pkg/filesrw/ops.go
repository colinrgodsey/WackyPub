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

// MaxReadSizeBytes is the maximum allowed byte size for a single read operation (200KB).
const MaxReadSizeBytes = 200 * 1024

// ReadFile returns canonPath's content, optionally restricted to the inclusive
// 1-indexed [start, end] line range. If numbered is true, output is cat -n
// formatted ("%6d\t%s\n"). If false, raw text is returned. If output exceeds
// MaxReadSizeBytes, an error is returned suggesting line-based pagination.
func ReadFile(canonPath string, start, end int, numbered bool) (string, error) {
	data, err := os.ReadFile(canonPath)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", canonPath, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("%s looks like a binary file - refusing to read it as text", canonPath)
	}

	if start == 0 && end == 0 && !numbered {
		if len(data) > MaxReadSizeBytes {
			return "", fmt.Errorf("%s size is %d bytes, which exceeds the %d byte read limit - use --start and --end for line pagination", canonPath, len(data), MaxReadSizeBytes)
		}
		return string(data), nil
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

	if !numbered {
		selected := lines[from-1 : to]
		totalBytes := 0
		for _, l := range selected {
			totalBytes += len(l) + 1
		}
		if totalBytes > MaxReadSizeBytes {
			return "", fmt.Errorf("%s selected range lines %d..%d is %d bytes, which exceeds the %d byte read limit - use --start and --end for line pagination", canonPath, from, to, totalBytes, MaxReadSizeBytes)
		}
		return strings.Join(selected, "\n") + "\n", nil
	}

	var b strings.Builder
	for i := from; i <= to; i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i, lines[i-1])
	}
	out := b.String()
	if len(out) > MaxReadSizeBytes {
		return "", fmt.Errorf("%s formatted output is %d bytes, which exceeds the %d byte read limit - use --start and --end for line pagination", canonPath, len(out), MaxReadSizeBytes)
	}
	return out, nil
}

// WriteFile atomically overwrites (or creates) canonPath with content,
// creating any missing parent directories first. Atomic via write-to-temp +
// rename, so a crash mid-write never leaves a corrupted/partial file behind.
//
// This also happens to be what defeats a hardlink-to-FILES_RW_ACCESS attack
// (confirmed live via swarm pen-test, see docs/files-rw-security-test.md): a
// hardlink planted inside a writable root shares FILES_RW_ACCESS's inode but
// passes Access.Resolve on its own (permitted) path, so an in-place write
// through it would silently rewrite the real FILES_RW_ACCESS. Rename instead
// replaces the writable root's directory entry with a new inode, severing
// the hardlink before any bytes reach the original file. That's incidental
// to why this function is atomic, not a deliberate hardlink defense - if
// this ever changes to an in-place write (or gains a fast path that skips
// the rename), that protection disappears with it. Don't remove the
// temp+rename pattern without re-verifying this.
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

// CopyFile reads raw bytes from canonSrc and writes them to canonDst.
func CopyFile(canonSrc, canonDst string) error {
	data, err := os.ReadFile(canonSrc)
	if err != nil {
		return fmt.Errorf("failed to read source file %s: %w", canonSrc, err)
	}
	return WriteFile(canonDst, string(data))
}

// MoveFile moves canonSrc to canonDst using os.Rename, falling back to copy + delete
// if cross-device move is required.
func MoveFile(canonSrc, canonDst string) error {
	dir := filepath.Dir(canonDst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory %s: %w", dir, err)
	}

	err := os.Rename(canonSrc, canonDst)
	if err == nil {
		return nil
	}

	// Fallback for cross-device renames or filesystem boundaries
	if err := CopyFile(canonSrc, canonDst); err != nil {
		return fmt.Errorf("failed to move %s to %s: %w", canonSrc, canonDst, err)
	}
	if err := os.Remove(canonSrc); err != nil {
		return fmt.Errorf("copied %s to %s but failed to remove original source: %w", canonSrc, canonDst, err)
	}
	return nil
}

// DeleteFile removes canonPath.
func DeleteFile(canonPath string) error {
	if err := os.Remove(canonPath); err != nil {
		return fmt.Errorf("failed to delete %s: %w", canonPath, err)
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

func isUnifiedDiff(diff string) bool {
	hasMinusHeader := false
	hasPlusHeader := false
	hasHunkHeader := false
	lines := strings.Split(diff, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--- ") || trimmed == "---" {
			hasMinusHeader = true
		}
		if strings.HasPrefix(trimmed, "+++ ") || trimmed == "+++" {
			hasPlusHeader = true
		}
		if strings.HasPrefix(trimmed, "@@") && strings.Contains(trimmed[2:], "@@") {
			hasHunkHeader = true
		}
	}
	return hasMinusHeader && hasPlusHeader && hasHunkHeader
}

// PatchFile applies a unified diff (read from diff) to canonPath by
// shelling out to the system `patch` binary, writing its output to a temp
// file first and renaming over canonPath only on success - so a malformed
// or partially-applying diff never leaves canonPath half-patched.
func PatchFile(canonPath string, diff string) error {
	if !isUnifiedDiff(diff) {
		return fmt.Errorf("patch rejected: input diff is not in unified diff format (must include \"---\", \"+++\", and \"@@\" hunk headers)")
	}

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
