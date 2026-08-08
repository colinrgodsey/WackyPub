package filesrw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "sample.txt")
	content := "line 1\nline 2\nline 3\nline 4\n"
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write sample file: %v", err)
	}

	// Full file read
	out, err := ReadFile(filePath, 0, 0)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	expectedFull := "1\tline 1\n2\tline 2\n3\tline 3\n4\tline 4\n"
	if out != expectedFull {
		t.Errorf("got %q, expected %q", out, expectedFull)
	}

	// Line range [2, 3]
	outRange, err := ReadFile(filePath, 2, 3)
	if err != nil {
		t.Fatalf("ReadFile range failed: %v", err)
	}
	expectedRange := "2\tline 2\n3\tline 3\n"
	if outRange != expectedRange {
		t.Errorf("got %q, expected %q", outRange, expectedRange)
	}

	// Binary file read rejection
	binPath := filepath.Join(tempDir, "sample.bin")
	binContent := []byte{'h', 'e', 'l', 'l', 'o', 0, 'w', 'o', 'r', 'l', 'd'}
	if err := os.WriteFile(binPath, binContent, 0o600); err != nil {
		t.Fatalf("failed to write binary file: %v", err)
	}
	_, err = ReadFile(binPath, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Errorf("expected binary file error, got %v", err)
	}
}

func TestWriteFile(t *testing.T) {
	tempDir := t.TempDir()
	nestedPath := filepath.Join(tempDir, "sub", "dir", "output.txt")

	content := "hello world\n"
	if err := WriteFile(nestedPath, content); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	readBack, err := os.ReadFile(nestedPath)
	if err != nil {
		t.Fatalf("failed to read back written file: %v", err)
	}
	if string(readBack) != content {
		t.Errorf("got %q, expected %q", string(readBack), content)
	}
}

func TestEditFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "edit.txt")

	initial := "foo bar foo baz\n"
	if err := os.WriteFile(filePath, []byte(initial), 0o600); err != nil {
		t.Fatalf("failed to write edit file: %v", err)
	}

	// Empty old string
	if err := EditFile(filePath, "", "new", false); err == nil {
		t.Errorf("expected error for empty old string, got nil")
	}

	// Zero match
	if err := EditFile(filePath, "nonexistent", "new", false); err == nil {
		t.Errorf("expected error for zero matches, got nil")
	}

	// Multiple matches without replaceAll
	if err := EditFile(filePath, "foo", "new", false); err == nil {
		t.Errorf("expected error for multiple matches without replaceAll, got nil")
	}

	// Multiple matches with replaceAll
	if err := EditFile(filePath, "foo", "QUX", true); err != nil {
		t.Fatalf("EditFile with replaceAll failed: %v", err)
	}
	readBack, _ := os.ReadFile(filePath)
	if string(readBack) != "QUX bar QUX baz\n" {
		t.Errorf("got %q, expected QUX bar QUX baz", string(readBack))
	}

	// Single match replacement
	if err := EditFile(filePath, "bar", "FOO", false); err != nil {
		t.Fatalf("EditFile single match failed: %v", err)
	}
	readBackSingle, _ := os.ReadFile(filePath)
	if string(readBackSingle) != "QUX FOO QUX baz\n" {
		t.Errorf("got %q, expected QUX FOO QUX baz", string(readBackSingle))
	}
}

func TestPatchFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "patch_target.txt")
	if err := os.WriteFile(filePath, []byte("line 1\nline 2\nline 3\n"), 0o600); err != nil {
		t.Fatalf("failed to write patch target: %v", err)
	}

	diff := strings.Join([]string{
		"--- patch_target.txt",
		"+++ patch_target.txt",
		"@@ -1,3 +1,3 @@",
		" line 1",
		"-line 2",
		"+line TWO",
		" line 3",
	}, "\n") + "\n"

	if err := PatchFile(filePath, diff); err != nil {
		t.Fatalf("PatchFile failed: %v", err)
	}

	readBack, _ := os.ReadFile(filePath)
	expected := "line 1\nline TWO\nline 3\n"
	if string(readBack) != expected {
		t.Errorf("got %q, expected %q", string(readBack), expected)
	}
}

func TestListDir(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "alpha.txt")
	if err := os.WriteFile(file1, []byte("content"), 0o600); err != nil {
		t.Fatalf("failed to write alpha: %v", err)
	}

	out, err := ListDir(tempDir, false, false, false)
	if err != nil {
		t.Fatalf("ListDir failed: %v", err)
	}
	if !strings.Contains(out, "alpha.txt") {
		t.Errorf("expected output to contain alpha.txt, got %q", out)
	}
}
