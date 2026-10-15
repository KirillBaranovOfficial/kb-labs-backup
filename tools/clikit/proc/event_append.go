//go:build darwin || linux

package proc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func appendEvent(dir string, event Event) error {
	file, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open event stream: %w", err)
	}
	unlock, err := lockFile(file)
	if err != nil {
		return errors.Join(fmt.Errorf("lock event stream: %w", err), file.Close())
	}
	data, marshalErr := json.Marshal(event)
	var writeErr error
	if marshalErr == nil {
		data = append(data, '\n')
		written, err := file.Write(data)
		writeErr = err
		if writeErr == nil && written != len(data) {
			writeErr = io.ErrShortWrite
		}
	}
	unlockErr := unlock()
	closeErr := file.Close()
	return errors.Join(marshalErr, writeErr, unlockErr, closeErr)
}
