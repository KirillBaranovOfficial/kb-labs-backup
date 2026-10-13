// Package operationlock owns the per-installation cross-process mutation lock.
package operationlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var ErrInProgress = errors.New("installation operation already in progress")

type Lease struct {
	path string
	pid  int
	mu   sync.Mutex
	once sync.Once
}

var held = struct {
	sync.Mutex
	byPath map[string]*Lease
}{byPath: map[string]*Lease{}}

func Acquire(path, operation string) (*Lease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "%d %s\n", os.Getpid(), operation)
			if closeErr := file.Close(); errors.Join(writeErr, closeErr) != nil {
				_ = os.Remove(path)
				return nil, errors.Join(writeErr, closeErr)
			}
			lease := &Lease{path: path, pid: os.Getpid()}
			held.Lock()
			held.byPath[path] = lease
			held.Unlock()
			return lease, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		pid, alive := lockHolder(path)
		if alive {
			return nil, fmt.Errorf("%w: pid=%d path=%s", ErrInProgress, pid, path)
		}
		_ = os.Remove(path)
	}
	return nil, ErrInProgress
}

func Held(path string) *Lease {
	held.Lock()
	defer held.Unlock()
	lease := held.byPath[path]
	if lease == nil || lease.pid != os.Getpid() {
		return nil
	}
	return lease
}

func Holder(path string) (int, bool) { return lockHolder(path) }

func (lease *Lease) With(fn func() error) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.pid != os.Getpid() {
		return ErrInProgress
	}
	return fn()
}

func (lease *Lease) Release() {
	lease.once.Do(func() {
		held.Lock()
		delete(held.byPath, lease.path)
		held.Unlock()
		_ = os.Remove(lease.path)
	})
}

func (lease *Lease) Path() string { return lease.path }

func lockHolder(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return 0, false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	err = process.Signal(syscall.Signal(0))
	return pid, err == nil || errors.Is(err, syscall.EPERM)
}
