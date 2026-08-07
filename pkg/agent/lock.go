package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SessionLock provides process-level exclusive locking for an agent session.
type SessionLock struct {
	file *os.File
}

// AcquireSessionLock acquires an exclusive POSIX lock (flock) on <agent_dir>/session.lock.
// It writes the current process PID to the lock file for diagnostic visibility.
func AcquireSessionLock(agentDir string) (*SessionLock, error) {
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create agent directory for lock: %w", err)
	}

	lockPath := filepath.Join(agentDir, "session.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", lockPath, err)
	}

	// Acquire exclusive file lock using syscall.Flock
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to acquire flock on %s: %w", lockPath, err)
	}

	// Write current PID to lock file
	_ = file.Truncate(0)
	_, _ = file.Seek(0, 0)
	_, _ = file.WriteString(fmt.Sprintf("%d\n", os.Getpid()))
	_ = file.Sync()

	return &SessionLock{file: file}, nil
}

// Release unlocks and closes the session lock file.
func (l *SessionLock) Release() {
	if l != nil && l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		_ = l.file.Close()
		l.file = nil
	}
}
