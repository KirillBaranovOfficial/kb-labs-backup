//go:build darwin || linux

package proc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ParseEventFilter(value string) ([]EventType, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	allowed := map[EventType]bool{
		EventQueued: true, EventBlocked: true, EventIdle: true, EventStart: true,
		EventStall: true, EventExit: true, EventLost: true, EventTeardown: true,
		EventPressure: true, EventOverBudget: true,
	}
	parts := strings.Split(value, ",")
	filters := make([]EventType, 0, len(parts))
	seen := make(map[EventType]bool, len(parts))
	for _, part := range parts {
		kind := EventType(strings.TrimSpace(part))
		if !allowed[kind] {
			return nil, fmt.Errorf("unknown event type %q", part)
		}
		if !seen[kind] {
			seen[kind] = true
			filters = append(filters, kind)
		}
	}
	return filters, nil
}

// Events streams existing JSONL records and, when follow is true, later appends.
func Events(ctx context.Context, stateDir string, follow bool, filter []EventType, startOffset int64, output io.Writer) error {
	if startOffset < 0 {
		return errors.New("proc: event offset must not be negative")
	}
	info, err := os.Stat(stateDir)
	if err != nil {
		return fmt.Errorf("proc: inspect state directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("proc: state path %q is not a directory", stateDir)
	}
	path := filepath.Join(stateDir, "events.jsonl")
	offset := startOffset
	var pending []byte
	allowed := make(map[EventType]bool, len(filter))
	for _, kind := range filter {
		allowed[kind] = true
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		file, err := os.Open(path)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("proc: open events: %w", err)
		}
		if err == nil {
			info, statErr := file.Stat()
			if statErr != nil {
				return errors.Join(fmt.Errorf("proc: stat events: %w", statErr), file.Close())
			}
			remaining := info.Size() - offset
			if remaining < 0 {
				return errors.Join(errors.New("proc: append-only event file shrank while being followed"), file.Close())
			}
			var readErr error
			for remaining > 0 {
				chunkSize := min(remaining, int64(64*1024))
				chunk := make([]byte, int(chunkSize))
				n, chunkErr := file.ReadAt(chunk, offset)
				if chunkErr != nil && !errors.Is(chunkErr, io.EOF) {
					readErr = chunkErr
					break
				}
				offset += int64(n)
				remaining -= int64(n)
				data := append(pending, chunk[:n]...)
				for {
					lineEnd := bytes.IndexByte(data, '\n')
					if lineEnd < 0 {
						break
					}
					var event Event
					if err := json.Unmarshal(data[:lineEnd], &event); err != nil {
						_ = file.Close()
						return fmt.Errorf("proc: decode event: %w", err)
					}
					if len(allowed) == 0 || allowed[event.Type] {
						if _, err := output.Write(data[:lineEnd+1]); err != nil {
							_ = file.Close()
							return err
						}
					}
					data = data[lineEnd+1:]
				}
				pending = data
				if n == 0 {
					break
				}
			}
			closeErr := file.Close()
			if readErr != nil {
				return errors.Join(fmt.Errorf("proc: read events: %w", readErr), closeErr)
			}
			if closeErr != nil {
				return fmt.Errorf("proc: close events: %w", closeErr)
			}
		}
		if !follow {
			if len(pending) != 0 {
				return errors.New("proc: event stream ends with an incomplete JSONL record")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
