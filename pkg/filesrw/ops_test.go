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

	// 1. Default unnumbered raw read
	rawOut, err := ReadFile(filePath, 0, 0, false)
	if err != nil {
		t.Fatalf("ReadFile raw failed: %v", err)
	}
	if rawOut != content {
		t.Errorf("got %q, expected %q", rawOut, content)
	}

	// 2. Numbered read (cat -n style: %6d\t%s)
	numberedOut, err := ReadFile(filePath, 0, 0, true)
	if err != nil {
		t.Fatalf("ReadFile numbered failed: %v", err)
	}
	expectedNumbered := "     1\tline 1\n     2\tline 2\n     3\tline 3\n     4\tline 4\n"
	if numberedOut != expectedNumbered {
		t.Errorf("got %q, expected %q", numberedOut, expectedNumbered)
	}

	// 3. Line range [2, 3] unnumbered
	outRange, err := ReadFile(filePath, 2, 3, false)
	if err != nil {
		t.Fatalf("ReadFile range failed: %v", err)
	}
	expectedRange := "line 2\nline 3\n"
	if outRange != expectedRange {
		t.Errorf("got %q, expected %q", outRange, expectedRange)
	}

	// 4. Hard read size limit (> 200KB) error
	largePath := filepath.Join(tempDir, "large.txt")
	largeData := make([]byte, MaxReadSizeBytes+1024)
	for i := range largeData {
		largeData[i] = 'a'
	}
	if err := os.WriteFile(largePath, largeData, 0o600); err != nil {
		t.Fatalf("failed to write large file: %v", err)
	}
	_, err = ReadFile(largePath, 0, 0, false)
	if err == nil || !strings.Contains(err.Error(), "exceeds the 204800 byte read limit") {
		t.Errorf("expected read size cap error, got %v", err)
	}

	// 5. Binary file read rejection
	binPath := filepath.Join(tempDir, "sample.bin")
	binContent := []byte{'h', 'e', 'l', 'l', 'o', 0, 'w', 'o', 'r', 'l', 'd'}
	if err := os.WriteFile(binPath, binContent, 0o600); err != nil {
		t.Fatalf("failed to write binary file: %v", err)
	}
	_, err = ReadFile(binPath, 0, 0, false)
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

func TestCopyFile(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "src.txt")
	dst := filepath.Join(tempDir, "sub", "dst.txt")
	content := "copy test bytes\n"

	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write src: %v", err)
	}

	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile failed: %v", err)
	}

	readBack, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(readBack) != content {
		t.Errorf("got %q, expected %q", string(readBack), content)
	}
}

func TestMoveFile(t *testing.T) {
	tempDir := t.TempDir()
	src := filepath.Join(tempDir, "move_src.txt")
	dst := filepath.Join(tempDir, "sub", "move_dst.txt")
	content := "move test bytes\n"

	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write src: %v", err)
	}

	if err := MoveFile(src, dst); err != nil {
		t.Fatalf("MoveFile failed: %v", err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("expected source file to be removed after move")
	}

	readBack, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read dst: %v", err)
	}
	if string(readBack) != content {
		t.Errorf("got %q, expected %q", string(readBack), content)
	}
}

func TestDeleteFile(t *testing.T) {
	tempDir := t.TempDir()
	target := filepath.Join(tempDir, "delete_me.txt")
	if err := os.WriteFile(target, []byte("temp"), 0o600); err != nil {
		t.Fatalf("failed to write target: %v", err)
	}

	if err := DeleteFile(target); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected file to be deleted")
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

	// Non-unified diff rejected
	if err := PatchFile(filePath, "invalid diff format"); err == nil {
		t.Errorf("expected non-unified diff to be rejected, got nil")
	}

	// Valid unified diff
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
