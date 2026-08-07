package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAcquireAndReleaseSessionLock(t *testing.T) {
	tempDir := t.TempDir()

	lock, err := AcquireSessionLock(tempDir)
	if err != nil {
		t.Fatalf("failed to acquire session lock: %v", err)
	}

	lockFile := filepath.Join(tempDir, "session.lock")
	data, err := os.ReadFile(lockFile)
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid != os.Getpid() {
		t.Errorf("expected lock PID %d, got %s", os.Getpid(), pidStr)
	}

	lock.Release()
}
