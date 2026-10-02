package stream

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/kb-labs/clikit/diag"
	"github.com/kb-labs/clikit/result"
)

// case: S-203.7
func TestStreamWritesEnvelopeEventsAndTerminalRecord(t *testing.T) {
	var output bytes.Buffer
	writer := New(&output)
	if err := writer.Event(map[string]any{"event": "alive"}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Fail(diag.New("KB_RUNTIME_UNEXPECTED", "The operation failed.",
		diag.WithCorrelationID("stream-correlation"))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(diag.ExitError); err != nil {
		t.Fatal(err)
	}

	encoded := append([]byte(nil), output.Bytes()...)
	scanner := bufio.NewScanner(bytes.NewReader(encoded))
	var records []map[string]json.RawMessage
	for scanner.Scan() {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSONL record %q: %v", scanner.Text(), err)
		}
		if err := result.ValidateMachineResult(scanner.Bytes()); err != nil {
			t.Fatalf("record doesn't use machine envelope: %v", err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("got %d records, want event, failure, and terminal", len(records))
	}
	if records[0]["data"] == nil || records[0]["ok"] == nil {
		t.Fatalf("event record = %v", records[0])
	}
	var failure struct {
		Error struct {
			Code          string `json:"code"`
			CorrelationID string `json:"correlationId"`
		} `json:"error"`
	}
	lines := bytes.Split(bytes.TrimSpace(encoded), []byte("\n"))
	if err := json.Unmarshal(lines[1], &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "KB_RUNTIME_UNEXPECTED" || failure.Error.CorrelationID != "stream-correlation" {
		t.Fatalf("failure record = %+v", failure)
	}
	var terminal struct {
		Meta struct {
			Stream struct {
				Terminal bool `json:"terminal"`
				ExitCode int  `json:"exitCode"`
			} `json:"stream"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(lines[2], &terminal); err != nil {
		t.Fatal(err)
	}
	if !terminal.Meta.Stream.Terminal || terminal.Meta.Stream.ExitCode != diag.ExitError {
		t.Fatalf("terminal record = %+v", terminal)
	}
}
