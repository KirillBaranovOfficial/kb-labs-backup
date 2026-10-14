// Package operationlock owns the per-installation cross-process mutation lock.
// Exclusivity comes from the kernel lock on the open descriptor. The persistent
// pathname is only a rendezvous point and its contents are descriptive.
package operationlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/kb-labs/clikit/diag"
)

var ErrInProgress = errors.New("installation operation already in progress")

type Lease struct {
	path string
	file *os.File
	pid  int
	mu   sync.Mutex
	once sync.Once
}

var held = struct {
	sync.Mutex
	byPath map[string]*Lease
}{byPath: map[string]*Lease{}}

// Path returns the stable lock-file pathname for an installation state dir.
// The file stays in place so all processes lock the same inode.
func Path(stateDir string) string { return filepath.Join(stateDir, "operation.lock") }

func Acquire(path, operation string) (*Lease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errFileLocked) {
			pid, ok := Holder(path)
			process := "unknown"
			if ok {
				process = strconv.Itoa(pid)
			}
			return nil, diag.Wrap(ErrInProgress, "KB_INSTALL_OPERATION_IN_PROGRESS",
				fmt.Sprintf("Another launcher operation is already running (process %s).", process),
				diag.WithHint("Wait for it to finish, then rerun the command."))
		}
		return nil, err
	}
	if err := writeHolder(file, operation); err != nil {
		unlockFile(file)
		_ = file.Close()
		return nil, err
	}
	lease := &Lease{path: path, file: file, pid: os.Getpid()}
	held.Lock()
	held.byPath[path] = lease
	held.Unlock()
	return lease, nil
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

// Holder reads descriptive metadata only. It never decides whether a lock is
// held; only the kernel lock result does that.
func Holder(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, false
	}
	pid, err := strconv.Atoi(fields[0])
	return pid, err == nil && pid > 0
}

func (lease *Lease) With(fn func() error) error {
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.pid != os.Getpid() || lease.file == nil {
		return ErrInProgress
	}
	return fn()
}

func (lease *Lease) Release() {
	lease.once.Do(func() {
		held.Lock()
		delete(held.byPath, lease.path)
		held.Unlock()
		if lease.file != nil {
			unlockFile(lease.file)
			_ = lease.file.Close()
			lease.file = nil
		}
	})
}

func (lease *Lease) Path() string { return lease.path }

func writeHolder(file *os.File, operation string) error {
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(file, "%d %s\n", os.Getpid(), operation); err != nil {
		return err
	}
	return file.Sync()
}
