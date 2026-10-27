//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// case: S-237.111, S-237.129
func TestSchedulerRendersProductionEmbeddedReviewMergeAndRoundTemplates(t *testing.T) {
	state := t.TempDir()
	options := schedulerRunOptions{store: newSchedulerStore(state), state: state}
	for _, name := range []string{runnerReviewTemplate, runnerMergeTemplate} {
		template, err := readRunnerTemplate(name)
		if err != nil || len(template) == 0 {
			t.Fatalf("embedded template %s: bytes=%q err=%v", name, template, err)
		}
		task := SchedulerTask{ID: strings.TrimSuffix(name, ".md"), Stream: "embedded-stream", PromptTemplate: name}
		path, err := options.renderReviewPrompt(task)
		if err != nil {
			t.Fatalf("render embedded template %s: %v", name, err)
		}
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "embedded-stream") || strings.Contains(string(content), "{stream}") {
			t.Fatalf("rendered %s=%q err=%v", name, content, err)
		}
	}
	message := filepath.Join(state, "message.md")
	if err := os.WriteFile(message, []byte("Do this work.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	round, err := prepareRoundPrompt(SchedulerTask{ID: "round", Stream: "embedded-round", Prompt: message}, state)
	if err != nil {
		t.Fatalf("render embedded definition of done: %v", err)
	}
	content, err := os.ReadFile(round)
	if err != nil || !strings.Contains(string(content), "Do this work.") || !strings.Contains(string(content), "Definition of done") {
		t.Fatalf("rendered worker prompt=%q err=%v", content, err)
	}
}
