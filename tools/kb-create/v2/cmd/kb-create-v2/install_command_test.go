package v2cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func installResultFromJSONStream(t *testing.T, output string, processExitCode int) applyCommandResult {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) < 2 {
		t.Fatalf("install JSON stream omitted progress or terminal records: %s", output)
	}
	type streamRecord struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
		Meta struct {
			Stream struct {
				Result   bool `json:"result"`
				Terminal bool `json:"terminal"`
				ExitCode int  `json:"exitCode"`
			} `json:"stream"`
		} `json:"meta"`
	}
	var result *applyCommandResult
	for index, line := range lines {
		var record streamRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode install JSON stream record %d: %v: %s", index+1, err, line)
		}
		if !record.OK {
			t.Fatalf("install success stream record %d is not successful: %s", index+1, line)
		}
		if record.Meta.Stream.Terminal {
			if index != len(lines)-1 || record.Meta.Stream.ExitCode != processExitCode || record.Data == nil || string(record.Data) != "null" {
				t.Fatalf("install terminal record does not match process exit %d: %s", processExitCode, line)
			}
			continue
		}
		if index == len(lines)-1 {
			t.Fatalf("install stream omitted its terminal record: %s", line)
		}
		if record.Meta.Stream.Result {
			if result != nil {
				t.Fatalf("install stream contains multiple result records: %s", output)
			}
			var decoded applyCommandResult
			if err := json.Unmarshal(record.Data, &decoded); err != nil {
				t.Fatalf("decode install result record: %v: %s", err, line)
			}
			result = &decoded
		}
	}
	if result == nil {
		t.Fatalf("install stream omitted its result record: %s", output)
	}
	return *result
}
