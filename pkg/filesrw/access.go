// Package filesrw implements a standalone read/write/edit/patch/list file
// tool for AI agents, gated by a per-directory FILES_RW_ACCESS allowlist.
package filesrw

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// AccessFileName is the exact filename (no upward search) that grants file
	// access for the current directory. Its own path is always denied, even to
	// a rule that would otherwise cover it.
	AccessFileName = "FILES_RW_ACCESS"

	rulePrefixWrite = "w:"
	rulePrefixRead  = "r:"
	tildeChar       = "~"
)

// Access is the parsed, resolved set of access rules from one
// FILES_RW_ACCESS file. All roots are canonical absolute paths (symlinks
// resolved) so containment checks are exact.
type Access struct {
	writableRoots []string
	readableRoots []string // superset of writableRoots
	denyPath      string   // FILES_RW_ACCESS's own canonical path
}

// LoadAccess reads and parses <cwd>/FILES_RW_ACCESS. Returns an error
// (access must be denied entirely) if the file is missing, unreadable, or
// contains an invalid rule - there is no partial-trust fallback.
func LoadAccess(cwd string) (*Access, error) {
	accessFilePath := filepath.Join(cwd, AccessFileName)

	f, err := os.Open(accessFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no %s file in %s - all file access is denied by default", AccessFileName, cwd)
		}
		return nil, fmt.Errorf("failed to read %s: %w", AccessFileName, err)
	}
	defer f.Close()

	denyPath, err := filepath.EvalSymlinks(accessFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve %s's own path: %w", AccessFileName, err)
	}

	acc := &Access{denyPath: denyPath}

	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var writable bool
		var rest string
		switch {
		case strings.HasPrefix(line, rulePrefixWrite):
			writable = true
			rest = strings.TrimSpace(line[len(rulePrefixWrite):])
		case strings.HasPrefix(line, rulePrefixRead):
			writable = false
			rest = strings.TrimSpace(line[len(rulePrefixRead):])
		default:
			return nil, fmt.Errorf("%s line %d: invalid rule %q - must start with %q or %q", AccessFileName, lineNo, line, rulePrefixWrite, rulePrefixRead)
		}
		if rest == "" {
			return nil, fmt.Errorf("%s line %d: rule has no path", AccessFileName, lineNo)
		}

		// A w: root is allowed to not exist yet - write auto-creates missing
		// parent directories within it, so requiring the root to pre-exist
		// would make that feature unreachable for a brand-new output
		// directory. r: has no such use case, so it stays strict: a
		// read-only grant pointing at nothing is almost always a typo worth
		// catching immediately.
		root, err := canonicalizeRoot(rest, cwd, !writable)
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", AccessFileName, lineNo, err)
		}

		acc.readableRoots = append(acc.readableRoots, root)
		if writable {
			acc.writableRoots = append(acc.writableRoots, root)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", AccessFileName, err)
	}

	return acc, nil
}

// canonicalizeRoot resolves a FILES_RW_ACCESS rule's path to a canonical
// absolute path. When mustExist is true, the root must already exist on
// disk - a rule pointing at nothing is a config mistake worth failing on
// immediately rather than silently granting access to nothing. When false
// (used for w: rules), resolution falls back to canonicalizeTarget's
// nearest-existing-ancestor logic, so a writable root can point at a
// directory that doesn't exist yet - required for write's auto-mkdir to
// ever be reachable for a brand-new output directory.
func canonicalizeRoot(path, cwd string, mustExist bool) (string, error) {
	if !mustExist {
		return canonicalizeTarget(path, cwd)
	}
	if strings.Contains(path, tildeChar) {
		return "", fmt.Errorf("path %q contains %q - not supported, use an absolute path", path, tildeChar)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %q: %w", path, err)
	}
	return resolved, nil
}

// canonicalizeTarget resolves a request path (possibly relative to cwd) to
// a canonical absolute path, for use in an access-control decision. Symlinks
// are resolved for as much of the path as actually exists on disk - so a
// not-yet-existing write target still has its real (symlink-resolved)
// ancestor directory checked, while the not-yet-existing tail is trusted as
// given relative to that resolved ancestor.
func canonicalizeTarget(path, cwd string) (string, error) {
	if strings.Contains(path, tildeChar) {
		return "", fmt.Errorf("path %q contains %q - not supported, use an absolute path", path, tildeChar)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.Clean(abs)

	dir := abs
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			full := resolved
			for i := len(tail) - 1; i >= 0; i-- {
				full = filepath.Join(full, tail[i])
			}
			return full, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to resolve %q: %w", path, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("failed to resolve %q: no existing ancestor directory found", path)
		}
		tail = append(tail, filepath.Base(dir))
		dir = parent
	}
}

// withinRoot reports whether path is root itself or a descendant of it,
// using a path-separator-aware boundary check (a naive string prefix would
// wrongly match "/home/bob/Downloads-secret" against root "/home/bob/Downloads").
func withinRoot(path, root string) bool {
	if path == root {
		return true
	}
	rootWithSep := root
	if !strings.HasSuffix(rootWithSep, string(os.PathSeparator)) {
		rootWithSep += string(os.PathSeparator)
	}
	return strings.HasPrefix(path, rootWithSep)
}

// Resolve validates path (relative to cwd) against a's rules and returns its
// canonical form on success. needWrite selects which rule set (w: vs r:/w:)
// must cover it. FILES_RW_ACCESS's own path is always denied, regardless of
// needWrite or any rule that would otherwise cover it.
func (a *Access) Resolve(path, cwd string, needWrite bool) (string, error) {
	canon, err := canonicalizeTarget(path, cwd)
	if err != nil {
		return "", err
	}

	if canon == a.denyPath {
		return "", fmt.Errorf("access to %s itself is always denied", AccessFileName)
	}

	roots := a.readableRoots
	verb, rule := "read", "r:"
	if needWrite {
		roots = a.writableRoots
		verb, rule = "write", "w:"
	}
	for _, root := range roots {
		if withinRoot(canon, root) {
			return canon, nil
		}
	}
	return "", fmt.Errorf("%s access denied for %q - not covered by any %q rule in %s", verb, path, rule, AccessFileName)
}
