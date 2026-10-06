package store

import (
	"fmt"
	"os"
	"syscall"
)

// fileLock is an advisory, process-scoped exclusive lock backed by flock(2).
// It prevents two processes from opening the same index directory in a
// read/write mode simultaneously.
type fileLock struct {
	f *os.File
}

func acquireLock(dir string) (*fileLock, error) {
	path := lockPath(dir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("index directory %s is locked by another process: %w", dir, err)
	}
	return &fileLock{f: f}, nil
}

func (l *fileLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	return l.f.Close()
}
