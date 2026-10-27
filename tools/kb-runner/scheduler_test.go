//go:build darwin || linux

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const schedulerTestSHA = "0123456789abcdef0123456789abcdef01234567"

type schedulerFixture struct {
	home        string
	state       string
	repo        string
	worktrees   string
	bin         string
	runList     string
	gitCalls    string
	ghCalls     string
	launchCalls string
	verdict     string
}

func newSchedulerFixture(t *testing.T, runList, verdict string) schedulerFixture {
	t.Helper()
	fixture := schedulerFixture{
		home:        t.TempDir(),
		state:       filepath.Join(t.TempDir(), "state"),
		repo:        t.TempDir(),
		bin:         t.TempDir(),
		runList:     filepath.Join(t.TempDir(), "runs.json"),
		gitCalls:    filepath.Join(t.TempDir(), "git.calls"),
		ghCalls:     filepath.Join(t.TempDir(), "gh.calls"),
		launchCalls: filepath.Join(t.TempDir(), "agent.calls"),
		verdict:     filepath.Join(t.TempDir(), "verdict.json"),
	}
	fixture.worktrees = filepath.Join(fixture.home, "Desktop", "work", "kb-labs-workspace-worktrees")
	if err := os.WriteFile(fixture.runList, []byte(runList), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.verdict, []byte(verdict), 0o600); err != nil {
		t.Fatal(err)
	}
	repoSource, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.repo, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.repo, "tools"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repoSource, "scripts", "ci"), filepath.Join(fixture.repo, "scripts", "ci")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repoSource, "tools", "kb-runner"), filepath.Join(fixture.repo, "tools", "kb-runner")); err != nil {
		t.Fatal(err)
	}
	fixture.writeCommand(t, "git", `#!/bin/sh
selected=""
if [ "$1" = "-C" ]; then selected="$2"; shift 2; fi
printf '%s|%s|%s\n' "$selected" "$PWD" "$*" >> "$FAKE_GIT_CALLS"
case "$1" in
  rev-parse) if [ "$2" = "--show-toplevel" ]; then if [ -n "$selected" ]; then printf '%s\n' "$selected"; else printf '%s\n' "$FAKE_REPO"; fi; else printf '%s\n' "$FAKE_SHA"; fi ;;
  remote) printf '%s\n' 'https://github.com/kb-labs/backup.git' ;;
  merge-base) printf '%s\n' "$FAKE_SHA" ;;
  worktree) if [ "$2" = "add" ]; then mkdir -p "$5"; fi ;;
  for-each-ref) ;;
  check-ref-format) ;;
  ls-files) if [ "$2" = "-u" ]; then printf '%s\n' "${FAKE_INDEX_UNMERGED:-}"; else printf '%s\n' "$FAKE_CHANGED_FILES"; fi ;;
  show) object_path="$2"; object_path="${object_path#*:}"; if [ -f "$selected/$object_path" ]; then cat "$selected/$object_path"; else for candidate in "$FAKE_WORKTREE_ROOT"/*/"$object_path"; do if [ -f "$candidate" ]; then cat "$candidate"; exit 0; fi; done; exit 1; fi ;;
  ls-tree|read-tree|reset|update-ref) ;;
  write-tree|commit-tree) printf '%s\n' "$FAKE_SHA" ;;
  merge-tree) if [ "${FAKE_MERGE_CONFLICT:-0}" = "1" ] && [ "$selected" = "$FAKE_INTEGRATION" ]; then printf '%s\n' 'CONFLICT (content): fake conflict' >&2; exit 1; else printf '%s\n' "$FAKE_SHA"; fi ;;
  status|add|rm|commit|merge|revert) if [ "$1" = "merge" ] && [ "$2" != "--abort" ] && [ "${FAKE_MERGE_CONFLICT:-0}" = "1" ] && [ "$selected" = "$FAKE_INTEGRATION" ]; then printf '%s\n' 'CONFLICT (content): fake conflict' >&2; exit 1; fi ;;
  diff) case "$*" in *--diff-filter=U*) printf '%s\n' "$FAKE_UNMERGED" ;; *--no-renames\ --name-only\ -z*) ;; *) printf '%s\n' "$FAKE_CHANGED_FILES" ;; esac ;;
  branch) printf '%s\n' "$FAKE_INTEGRATION_BRANCH" ;;
  push) ;;
  *) printf 'unexpected fake git command: %s\n' "$*" >&2; exit 91 ;;
esac
`)
	fixture.writeCommand(t, "gh", `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_GH_CALLS"
case "$1 $2" in
  "run list") if [ "${FAKE_GH_BLOCK:-0}" = "1" ]; then : > "$FAKE_GH_STARTED"; IFS= read -r _ < "$FAKE_GH_FIFO"; fi; cat "$FAKE_RUN_LIST" ;;
  "run view") [ "${FAKE_GH_VIEW_FAIL:-0}" = "1" ] && exit 97; printf '%s\n' '{"status":"completed","conclusion":"success","jobs":[]}' ;;
  "run download")
    run_id="$3"
    shift 2
    while [ "$#" -gt 0 ]; do
      if [ "$1" = "--dir" ]; then dir="$2"; shift 2; else shift; fi
    done
    mkdir -p "$dir"
    if [ -f "$FAKE_VERDICT_DIR/$run_id.json" ]; then cp "$FAKE_VERDICT_DIR/$run_id.json" "$dir/verdict.json"; else cp "$FAKE_VERDICT" "$dir/verdict.json"; fi
    ;;
  "run cancel"|"run rerun") ;;
  *) printf 'unexpected fake gh command: %s\n' "$*" >&2; exit 92 ;;
esac
`)
	fixture.writeCommand(t, "gem", `#!/bin/sh
model="flash"
while [ "$#" -gt 0 ]; do if [ "$1" = "-m" ]; then model="$2"; shift 2; else shift; fi; done
printf 'fake model %s completed\n' "$model" >&2
if [ "$model" = "pro" ]; then printf 'VERDICT: %s\n' "${FAKE_GEM_PRO_VERDICT:-ACCEPT}"; else printf 'VERDICT: %s\n' "${FAKE_GEM_FLASH_VERDICT:-REWORK}"; fi
`)
	fixture.writeCommand(t, "kb-runner", `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_AGENT_CALLS"
if [ "$1" = "run" ]; then
  shift
  while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
  [ "$#" -gt 0 ] && shift
  exec "$@"
fi
[ "$1" = "agent" ] && [ "$2" = "start" ] || exit 93
shift 2
while [ "$#" -gt 0 ]; do
  case "$1" in
    --state) state="$2"; shift 2 ;;
    --stream) stream="$2"; shift 2 ;;
    --vendor) vendor="$2"; shift 2 ;;
    --workdir) workdir="$2"; shift 2 ;;
    --prompt-file) prompt="$2"; shift 2 ;;
    --message) message="$2"; shift 2 ;;
    --job-id) job_id="$2"; shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "$workdir" "$state/jobs"
case "$stream" in
  *-2) report="$workdir/.review-report-$stream.md"; [ -z "$message" ] || report="$message"; mkdir -p "$(dirname "$report")"; if [ "$vendor" = "zcode" ] && [ "${FAKE_ZCODE_FAIL:-0}" = "1" ]; then exit 71; fi; printf '%s\n' "${FAKE_REVIEW_VERDICT_2:-${FAKE_REVIEW_VERDICT:-ACCEPT}}" > "$report" ;;
  *-review*) report="$workdir/.review-report-$stream.md"; [ -z "$message" ] || report="$message"; mkdir -p "$(dirname "$report")"; if [ "$vendor" = "zcode" ] && [ "${FAKE_ZCODE_FAIL:-0}" = "1" ]; then exit 71; fi; printf '%s\n' "${FAKE_REVIEW_VERDICT:-ACCEPT}" > "$report" ;;
  *) report="$workdir/.agent-report.md"; [ -z "$message" ] || report="$message"; mkdir -p "$(dirname "$report")"; printf '%s\n' "${FAKE_AGENT_STATUS:-Status: DONE}" > "$report"; printf '%s\n' "$FAKE_AGENT_PROPOSALS" >> "$report"; if [ -n "$FAKE_AGENT_QUESTION" ]; then printf '%s\n' "$FAKE_AGENT_QUESTION" >> "$report"; fi ;;
esac
printf '{"id":"%s","stream":"%s","sessionId":"fixture-thread"}\n' "$job_id" "$stream" > "$state/jobs/$job_id.json"
printf 'job %s completed\n' "$job_id"
`)
	if err := os.MkdirAll(filepath.Join(fixture.worktrees, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.worktrees, "bin", "kb-runner-zcode"), []byte(`#!/bin/sh
[ "${FAKE_ZCODE_FAIL:-0}" = "1" ] && exit 1
printf '%s\n' "${FAKE_ZCODE_VERDICT:-ACCEPT}" > "$FAKE_ZCODE_REPORT"
`), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.worktrees, "prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	projectPrompts := filepath.Join(filepath.Dir(repoSource), "prompts")
	for _, name := range []string{"gem-review.sh", "review-flags.sh", "limits.py", "00-rules.md", "run-zcode-agent.sh"} {
		if err := os.Symlink(filepath.Join(projectPrompts, name), filepath.Join(fixture.worktrees, "prompts", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(fixture.worktrees, "reports"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fixture.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HOME", fixture.home)
	t.Setenv("FAKE_REPO", fixture.repo)
	t.Setenv("FAKE_WORKTREE_ROOT", fixture.worktrees)
	t.Setenv("FAKE_CHANGED_FILES", "docs/qa/scenarios/S-237-process-tree-supervision.md README.md")
	t.Setenv("FAKE_SHA", schedulerTestSHA)
	t.Setenv("FAKE_INTEGRATION", filepath.Join(fixture.worktrees, "j90-integration"))
	t.Setenv("FAKE_INTEGRATION_BRANCH", "agent/j90-integration")
	t.Setenv("FAKE_GIT_CALLS", fixture.gitCalls)
	t.Setenv("FAKE_RUN_LIST", fixture.runList)
	t.Setenv("FAKE_GH_CALLS", fixture.ghCalls)
	t.Setenv("FAKE_AGENT_CALLS", fixture.launchCalls)
	t.Setenv("FAKE_VERDICT", fixture.verdict)
	t.Setenv("FAKE_VERDICT_DIR", fixture.bin)
	t.Setenv("FAKE_AGENT_PROPOSALS", "## Proposed commits\n```json\n[{\"kind\":\"case\",\"subject\":\"docs(qa): update scheduler case\",\"body\":\"\"},{\"kind\":\"tests\",\"subject\":\"test(kb-runner): cover scheduler lifecycle\",\"body\":\"\"},{\"kind\":\"code\",\"subject\":\"feat(kb-runner): schedule agent work\",\"body\":\"Scheduler implementation details.\"}]\n```")
	return fixture
}

// case: S-237.111
func TestSchedulerRenderingLeavesEveryPromptTemplateUnchanged(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	options := schedulerRunOptions{store: newSchedulerStore(f.state), state: f.state}
	for _, name := range []string{runnerReviewTemplate, runnerMergeTemplate, runnerDefinitionOfDone} {
		before, err := readRunnerTemplate(name)
		if err != nil {
			t.Fatal(err)
		}
		if name != runnerDefinitionOfDone {
			task := SchedulerTask{ID: name, Stream: "stream-a", PromptTemplate: name}
			rendered, err := options.renderReviewPrompt(task)
			if err != nil {
				t.Fatalf("render %s: %v", name, err)
			}
			if rendered == filepath.Join(f.repo, "prompts", name) {
				t.Fatalf("rendered %s into template path", name)
			}
		}
		after, err := readRunnerTemplate(name)
		if err != nil || string(after) != string(before) {
			t.Fatalf("rendering changed embedded %s: before=%q after=%q err=%v", name, before, after, err)
		}
	}
}

// case: S-237.108
func TestSnapshotUsesPrivateIndexAndLeavesWorktreeStateUntouched(t *testing.T) {
	worktree := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		runGit(args...)
	}
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	runGit("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(worktree, "tracked.txt"), []byte("working tree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked.txt")
	if err := os.WriteFile(filepath.Join(worktree, "new.txt"), []byte("untracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeHead := runGit("rev-parse", "HEAD")
	beforeTree := runGit("write-tree")
	beforeStatus := runGit("status", "--porcelain", "--untracked-files=all")
	sha, ref, err := snapshotWorktree(worktree, "fixture-stream")
	if err != nil {
		t.Fatal(err)
	}
	if len(sha) != 40 || !strings.HasPrefix(ref, "refs/snapshots/fixture-stream/") {
		t.Fatalf("snapshot identity sha=%q ref=%q", sha, ref)
	}
	if got := runGit("rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("snapshot moved HEAD: before=%s after=%s", beforeHead, got)
	}
	if got := runGit("write-tree"); got != beforeTree {
		t.Fatalf("snapshot changed the real index: before=%s after=%s", beforeTree, got)
	}
	if got := runGit("status", "--porcelain", "--untracked-files=all"); got != beforeStatus {
		t.Fatalf("snapshot changed worktree status: before=%q after=%q", beforeStatus, got)
	}
	if parent := runGit("rev-parse", sha+"^1"); parent != beforeHead {
		t.Fatalf("snapshot commit parent=%s want HEAD=%s", parent, beforeHead)
	}
	if got := runGit("show", sha+":new.txt"); got != "untracked" {
		t.Fatalf("snapshot omitted untracked file: %q", got)
	}
}

// case: S-237.108
func TestSnapshotRefusesUnmergedIndexWithoutChangingIt(t *testing.T) {
	worktree := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", worktree}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	for _, args := range [][]string{{"init", "-q", "--initial-branch=main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		runGit(args...)
	}
	if err := os.WriteFile(filepath.Join(worktree, "conflict.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "conflict.txt")
	runGit("commit", "-m", "base")
	runGit("checkout", "-b", "side")
	if err := os.WriteFile(filepath.Join(worktree, "conflict.txt"), []byte("side\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("commit", "-am", "side")
	runGit("checkout", "main")
	if err := os.WriteFile(filepath.Join(worktree, "conflict.txt"), []byte("main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("commit", "-am", "main")
	merge := exec.Command("git", "-C", worktree, "merge", "side")
	if output, err := merge.CombinedOutput(); err == nil {
		t.Fatalf("fixture merge did not conflict: %s", output)
	}
	beforeHead := runGit("rev-parse", "HEAD")
	beforeIndex := runGit("ls-files", "--stage")
	beforeStatus := runGit("status", "--porcelain")
	if _, _, err := snapshotWorktree(worktree, "conflict-stream"); err == nil {
		t.Fatal("snapshot accepted unresolved merge entries")
	}
	if got := runGit("rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("refusal moved HEAD: before=%s after=%s", beforeHead, got)
	}
	if got := runGit("ls-files", "--stage"); got != beforeIndex {
		t.Fatalf("refusal changed the merge index: before=%q after=%q", beforeIndex, got)
	}
	if got := runGit("status", "--porcelain"); got != beforeStatus {
		t.Fatalf("refusal changed worktree status: before=%q after=%q", beforeStatus, got)
	}
}

func (f schedulerFixture) writeCommand(t *testing.T, name, body string) {
	t.Helper()
	path := filepath.Join(f.bin, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
}

func (f schedulerFixture) addTask(t *testing.T, stream, role string, deps ...string) SchedulerTask {
	t.Helper()
	prompt := filepath.Join(f.repo, stream+".md")
	if err := os.WriteFile(prompt, []byte("task for "+stream), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"sched", "add", "--state", f.state, "--stream", stream, "--prompt", prompt, "--base", "main", "--priority", "4", "--vendor", "codex", "--effort", "high", "--worktree-root", f.worktrees, "--review", "single", "--reviewer", "codex"}
	if role == "review" {
		args = append(args, "--role", "review", "--prompt-template", runnerReviewTemplate, "--workdir", filepath.Join(f.worktrees, stream))
	}
	for _, dep := range deps {
		args = append(args, "--depends-on", dep)
	}
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", args, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("sched add %s exit=%d stdout=%s stderr=%s", stream, status.Code(), out.String(), errOut.String())
	}
	var task SchedulerTask
	if err := json.Unmarshal(out.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v; output=%s", err, out.String())
	}
	return task
}

func (f schedulerFixture) runOnce(t *testing.T, extra ...string) (int, string) {
	t.Helper()
	args := []string{"sched", "run", "--state", f.state, "--once", "--slots", "1"}
	args = append(args, extra...)
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", args, &out, &errOut)
	return status.Code(), out.String() + errOut.String()
}

func (f schedulerFixture) addAwaitingRemote(t *testing.T, task SchedulerTask, submission SchedulerSubmission) {
	t.Helper()
	store := newSchedulerStore(f.state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.Submission = &submission
	if err := store.transition(stream, SchedulerRunning, "agent round", "await remote submit"); err != nil {
		t.Fatal(err)
	}
	stream, err = store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(stream, SchedulerAwaitingRemote, "GitHub Actions", "wait for the remote watcher"); err != nil {
		t.Fatal(err)
	}
}

// case: S-237.74
func TestSchedulerTaskBacklogPersistsResolvedWorkdirAndRole(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	worker := f.addTask(t, "worker-a", "worker")
	review := f.addTask(t, "worker-b-review", "review", worker.ID)
	cancelled := f.addTask(t, "worker-c", "worker")
	if worker.Workdir != filepath.Join(f.worktrees, "worker-a") || !filepath.IsAbs(worker.Workdir) || worker.Role != "worker" || worker.RepoRoot != f.repo {
		t.Fatalf("worker task lacks persisted worktree contract: %+v", worker)
	}
	if review.Workdir != filepath.Join(f.worktrees, "worker-b-review") || review.Role != "review" || review.PromptTemplate != runnerReviewTemplate {
		t.Fatalf("review task lacks persisted role/template contract: %+v", review)
	}
	var cancelOutput bytes.Buffer
	status := executeInvocation("kb-runner", []string{"sched", "cancel", "--state", f.state, cancelled.ID}, &cancelOutput, &bytes.Buffer{})
	if status.Code() != 0 {
		t.Fatalf("sched cancel exit=%d output=%s", status.Code(), cancelOutput.String())
	}
	var listing bytes.Buffer
	status = executeInvocation("kb-runner", []string{"sched", "ls", "--state", f.state, "--json"}, &listing, &bytes.Buffer{})
	if status.Code() != 0 {
		t.Fatalf("sched ls exit=%d output=%s", status.Code(), listing.String())
	}
	var tasks []SchedulerTask
	if err := json.Unmarshal(listing.Bytes(), &tasks); err != nil || len(tasks) != 2 || tasks[1].DependsOn[0] != worker.ID {
		t.Fatalf("task records=%+v decode=%v output=%s", tasks, err, listing.String())
	}
	if _, err := newSchedulerStore(f.state).task(cancelled.ID); !os.IsNotExist(err) {
		t.Fatalf("cancelled task remains in the backlog: %v", err)
	}
}

func TestSchedulerRejectsTransitionsThatWouldLoseRemoteOwnership(t *testing.T) {
	if !validSchedulerTransition(SchedulerAwaitingRemote, SchedulerDone) {
		t.Fatal("a passing candidate verdict must complete an awaiting-remote stream")
	}
}

// case: S-237.86
func TestSchedulerTaskPersistsExplicitReviewPolicy(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "policy-worker", "worker")
	if task.Review != "single" || len(task.Reviewers) != 1 || task.Reviewers[0] != "codex" {
		t.Fatalf("task review policy=%+v", task)
	}
	var out bytes.Buffer
	var errOut bytes.Buffer
	if err := os.WriteFile(filepath.Join(f.repo, "cross-worker.md"), []byte("cross policy"), 0o600); err != nil {
		t.Fatal(err)
	}
	status := executeInvocation("kb-runner", []string{"sched", "add", "--state", f.state, "--stream", "cross-worker", "--prompt", filepath.Join(f.repo, "cross-worker.md"), "--base", "main", "--worktree-root", f.worktrees, "--review", "cross", "--reviewer", "codex", "--reviewer", "gemini"}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("cross review sched add exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	var cross SchedulerTask
	if err := json.Unmarshal(out.Bytes(), &cross); err != nil || cross.Review != "cross" || strings.Join(cross.Reviewers, ",") != "codex,gemini" {
		t.Fatalf("cross review policy=%+v decode=%v", cross, err)
	}
}

// case: S-237.85
func TestSchedulerInitialTransitionWritesOneHandoffLine(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	data, err := os.ReadFile(filepath.Join(f.worktrees, "HANDOFF.md"))
	if err != nil || strings.Count(string(data), "scheduler=") != 1 || !strings.Contains(string(data), "worker-a") {
		t.Fatalf("initial scheduler handoff=%q err=%v", data, err)
	}
}

// case: S-237.85, S-237.88
func TestSchedulerHandoffProjectionIsIdempotentAndDecisionCausesAreClosed(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "handoff-worker", "worker")
	store := newSchedulerStore(f.state)
	event := map[string]any{"kind": "scheduler.verdict", "stream": task.Stream, "task_id": task.ID, "status": "pass", "time": "stable-fixture-time"}
	if err := store.appendEvent(event); err != nil {
		t.Fatal(err)
	}
	if err := store.appendEvent(event); err != nil {
		t.Fatal(err)
	}
	handoff, err := os.ReadFile(filepath.Join(f.worktrees, "HANDOFF.md"))
	if err != nil || strings.Count(string(handoff), "stable-fixture-time") != 1 {
		t.Fatalf("duplicate event projection=%q err=%v", handoff, err)
	}
	for _, cause := range []string{"plan-question", "review-disagreement", "review-missing", "review-unavailable", "third-reject", "outage-after-retries", "no-proposed-commits", "bisect-inconclusive"} {
		if !validSchedulerDecisionCause(cause) {
			t.Fatalf("decision cause %q is not in the scheduler contract", cause)
		}
	}
	if validSchedulerDecisionCause("raw-provider-error") {
		t.Fatal("orchestrator queue accepted an unowned free-form cause")
	}
	ready := f.addTask(t, "aaa-ready", "worker")
	decision := f.addTask(t, "needs-decision", "worker")
	decisionStream, err := store.stream(decision.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(decisionStream, SchedulerRunning, "review", "resolve review"); err != nil {
		t.Fatal(err)
	}
	decisionStream, _ = store.stream(decision.Stream)
	decisionStream.Cause = "review-unavailable"
	if err := store.transition(decisionStream, SchedulerAwaitingOrchestrator, "reviewer", "choose fallback"); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	status := executeInvocation("kb-runner", []string{"sched", "ps", "--state", f.state, "--json"}, &output, &bytes.Buffer{})
	var listed []SchedulerStream
	if status.Code() != 0 || json.Unmarshal(output.Bytes(), &listed) != nil || len(listed) < 3 || listed[0].Stream != decision.Stream || listed[1].Stream != ready.Stream {
		t.Fatalf("ps did not sort decision-needed streams first: exit=%d streams=%+v output=%s", status.Code(), listed, output.String())
	}
}

// case: S-237.84
func TestSchedulerCandidateFailureBisectsPrefixesAndRevertsFirstFailingMerge(t *testing.T) {
	f, _, store := newCandidateBisectFixture(t, false)
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("candidate failure cycle exit=%d output=%s", code, output)
	}
	integration, err := store.integration()
	if err != nil || integration.Bisect == nil || len(integration.Bisect.Prefixes) != 2 {
		t.Fatalf("candidate did not persist both prefix submissions: integration=%+v err=%v", integration, err)
	}
	gitCalls, err := os.ReadFile(f.gitCalls)
	if err != nil || !strings.Contains(string(gitCalls), integration.Bisect.Prefixes[0].Ref) || !strings.Contains(string(gitCalls), integration.Bisect.Prefixes[1].Ref) {
		t.Fatalf("prefix verifications were not submitted together: calls=%q err=%v", gitCalls, err)
	}
	if err := f.completeBisectPrefixes(t, integration.Bisect.Prefixes, false); err != nil {
		t.Fatal(err)
	}
	if code, output := f.runOnce(t); code != 0 {
		calls, _ := os.ReadFile(f.gitCalls)
		t.Fatalf("prefix verdict cycle exit=%d output=%s git=%s", code, output, calls)
	}
	events, err := os.ReadFile(filepath.Join(store.dir, "events.jsonl"))
	if err != nil || !strings.Contains(string(events), `"to":"ready"`) || !strings.Contains(string(events), `"cause":"candidate failures: TestCandidateRegression"`) {
		t.Fatalf("first failing prefix did not return stream to READY with candidate failures: events=%s err=%v", events, err)
	}
	calls, err := os.ReadFile(f.gitCalls)
	if err != nil || !strings.Contains(string(calls), "revert -m 1 --no-edit "+integration.Bisect.Prefixes[0].MergeSHA) {
		t.Fatalf("scheduler did not revert the first failing merge: calls=%q err=%v", calls, err)
	}
	completed, err := store.integration()
	if err != nil || completed.Bisect != nil || completed.LastBisect == nil {
		t.Fatalf("completed bisect was not archived: integration=%+v err=%v", completed, err)
	}
}

// case: S-237.84
func TestSchedulerCandidateBisectUsesInconclusiveOnlyWhenFinalPrefixDisagrees(t *testing.T) {
	f, task, store := newCandidateBisectFixture(t, true)
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("candidate failure cycle exit=%d output=%s", code, output)
	}
	integration, err := store.integration()
	if err != nil || integration.Bisect == nil {
		t.Fatalf("candidate bisect record=%+v err=%v", integration, err)
	}
	if err := f.completeBisectPrefixes(t, integration.Bisect.Prefixes, true); err != nil {
		t.Fatal(err)
	}
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("flake prefix cycle exit=%d output=%s", code, output)
	}
	stream, err := store.stream(task.Stream)
	if err != nil || stream.State != SchedulerAwaitingOrchestrator || stream.Cause != "bisect-inconclusive" {
		t.Fatalf("candidate/prefix disagreement state=%+v err=%v", stream, err)
	}
	completed, err := store.integration()
	if err != nil || completed.Bisect != nil || completed.LastBisect == nil {
		t.Fatalf("inconclusive bisect remained active: integration=%+v err=%v", completed, err)
	}
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("scheduler restart after inconclusive result exit=%d output=%s", code, output)
	}
	calls, err := os.ReadFile(f.gitCalls)
	if err != nil || strings.Contains(string(calls), "revert -m 1") {
		t.Fatalf("inconclusive prefixes must not select a revert: calls=%q err=%v", calls, err)
	}
}

// case: S-237.84
func TestSchedulerCandidatePrefixOutageRerunsTwiceThenTerminates(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	options := schedulerRunOptions{}
	prefix := SchedulerBisectPrefix{Ref: "verify/bisect-worker-a-0123456", RunID: "21"}
	for attempt := 1; attempt <= schedulerOutageLimit; attempt++ {
		if err := options.retryBisectPrefix(&prefix, "kb-labs/backup", fmt.Errorf("temporary outage %d", attempt)); err != nil {
			t.Fatal(err)
		}
		if prefix.Attempts != attempt || prefix.Status != "pending" {
			t.Fatalf("prefix retry %d state=%+v", attempt, prefix)
		}
	}
	if err := options.retryBisectPrefix(&prefix, "kb-labs/backup", errors.New("outage after reruns")); err != nil {
		t.Fatal(err)
	}
	if prefix.Attempts != schedulerOutageLimit || prefix.Status != "outage" {
		t.Fatalf("prefix outage did not stop after bounded reruns: %+v", prefix)
	}
	calls, err := os.ReadFile(f.ghCalls)
	if err != nil || strings.Count(string(calls), "run rerun") != schedulerOutageLimit {
		t.Fatalf("prefix rerun count=%q err=%v", calls, err)
	}
}

func newCandidateBisectFixture(t *testing.T, finalPrefixPass bool) (schedulerFixture, SchedulerTask, *schedulerStore) {
	t.Helper()
	sha1 := strings.Repeat("1", 40)
	candidate := schedulerTestSHA
	runs := fmt.Sprintf(`[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/candidate-worker-a-%s","headSha":"%s","status":"completed","conclusion":"failure"}]`, candidate[:7], candidate)
	verdict := `{"schemaVersion":1,"sha":"` + candidate + `","status":"fail","jobs":[{"name":"unit","status":"fail","failures":["TestCandidateRegression"],"logTail":"candidate failure"}],"diagnostics":["candidate verification failed"]}`
	f := newSchedulerFixture(t, runs, verdict)
	if err := os.MkdirAll(filepath.Join(f.worktrees, "j90-integration"), 0o700); err != nil {
		t.Fatal(err)
	}
	task := f.addTask(t, "worker-a", "worker")
	store := newSchedulerStore(f.state)
	f.addAwaitingRemote(t, task, SchedulerSubmission{SHA: candidate, Ref: "verify/candidate-worker-a-" + candidate[:7], Purpose: "candidate", RunID: "11", DeadlineAt: time.Now().UTC().Add(time.Hour)})
	for _, merge := range []SchedulerMergeRecord{
		{CommitSHA: sha1, Stream: "worker-a", ParentSHA: strings.Repeat("0", 40)},
		{CommitSHA: candidate, Stream: "worker-b", ParentSHA: sha1},
	} {
		if err := store.recordIntegrationMerge(merge); err != nil {
			t.Fatal(err)
		}
	}
	return f, task, store
}

func (f schedulerFixture) completeBisectPrefixes(t *testing.T, prefixes []SchedulerBisectPrefix, finalPrefixPass bool) error {
	t.Helper()
	runs := make([]string, 0, len(prefixes))
	for index, prefix := range prefixes {
		runID := fmt.Sprint(21 + index)
		conclusion := "failure"
		status := "fail"
		if finalPrefixPass && index == len(prefixes)-1 {
			conclusion, status = "success", "pass"
		}
		runs = append(runs, fmt.Sprintf(`{"databaseId":%s,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":%q,"headSha":%q,"status":"completed","conclusion":%q}`, runID, prefix.Ref, prefix.SHA, conclusion))
		verdict := fmt.Sprintf(`{"schemaVersion":1,"sha":%q,"status":%q,"jobs":[],"diagnostics":[]}`, prefix.SHA, status)
		if err := os.WriteFile(filepath.Join(f.bin, runID+".json"), []byte(verdict), 0o600); err != nil {
			return err
		}
	}
	if err := os.WriteFile(f.runList, []byte("["+strings.Join(runs, ",")+"]"), 0o600); err != nil {
		return err
	}
	return nil
}

// case: S-237.113
func TestSchedulerRunReplaysMissingHandoffProjectionIdempotently(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "handoff-replay", "worker")
	store := newSchedulerStore(f.state)
	if err := store.cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	handoff := filepath.Join(f.worktrees, "HANDOFF.md")
	if err := os.Remove(handoff); err != nil {
		t.Fatal(err)
	}
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("scheduler recovery run exit=%d output=%s", code, output)
	}
	restored, err := os.ReadFile(handoff)
	if err != nil || strings.Count(string(restored), "scheduler=") != 2 {
		t.Fatalf("replayed HANDOFF=%q err=%v", restored, err)
	}
	if code, output := f.runOnce(t); code != 0 {
		t.Fatalf("second scheduler recovery run exit=%d output=%s", code, output)
	}
	second, err := os.ReadFile(handoff)
	if err != nil || string(second) != string(restored) {
		t.Fatalf("HANDOFF replay was not idempotent: before=%q after=%q err=%v", restored, second, err)
	}
}

// case: S-237.82
func TestSchedulerMissingCommitProposalsStopsWithNamedCause(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_AGENT_PROPOSALS", "")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("scheduler run exit=%d output=%s", code, output)
	}
	stream, err := newSchedulerStore(f.state).stream("worker-a")
	if err != nil || stream.State != SchedulerAwaitingOrchestrator || stream.Cause != "no-proposed-commits" {
		t.Fatalf("missing proposals state=%+v err=%v", stream, err)
	}
	if _, err := os.Stat(filepath.Join(f.worktrees, "prompts", "land.calls")); !os.IsNotExist(err) {
		t.Fatalf("land owner called without proposals: %v", err)
	}
}

// case: S-237.86
func TestSchedulerRiskPolicyEscalatesChangedRiskZone(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	t.Setenv("FAKE_CHANGED_FILES", "tools/kb-runner/scheduler.go")
	policy, err := os.ReadFile(filepath.Join(f.repo, "tools", "kb-runner", "scheduler-policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var configured schedulerPolicy
	if err := json.Unmarshal(policy, &configured); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if len(configured.RiskZones) == 0 || !containsSchedulerPath(configured.RiskZones, "tools/kb-runner/scheduler.go") {
		t.Fatalf("risk policy does not cover scheduler implementation: %s", policy)
	}
	if !strings.Contains(string(policy), "tools/kb-create/**") || !strings.Contains(string(policy), "scripts/ci/**") {
		t.Fatalf("policy omitted requested risk zones: %s", policy)
	}
	task := f.addTask(t, "risk-worker", "worker")
	if err := os.MkdirAll(task.Workdir, 0o700); err != nil {
		t.Fatal(err)
	}
	stream := SchedulerStream{Stream: task.Stream, TaskID: task.ID, Submission: &SchedulerSubmission{SHA: schedulerTestSHA}}
	options := schedulerRunOptions{}
	mode, reviewers, err := options.reviewPolicy(task, stream)
	if err != nil || mode != "cross" || len(reviewers) != 2 {
		t.Fatalf("risk-zone policy mode=%s reviewers=%v err=%v", mode, reviewers, err)
	}
}

// case: S-237.83
func TestSchedulerMergeConflictQueuesResolverOnlyWithUnmergedFiles(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_MERGE_CONFLICT", "1")
	t.Setenv("FAKE_UNMERGED", "tools/kb-runner/scheduler.go")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("conflicting land cycle exit=%d output=%s", code, output)
	}
	mergeTask, err := newSchedulerStore(f.state).task("worker-a-merge")
	if err != nil || mergeTask.Role != schedulerMergeRole || mergeTask.Vendor != "codex" || mergeTask.PromptTemplate != runnerMergeTemplate {
		t.Fatalf("conflict did not enqueue persisted merge resolver: task=%+v err=%v", mergeTask, err)
	}
}

// case: S-237.87, S-237.88
func TestSchedulerCrossReviewDisagreementNamesBothReports(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "worker-a", "worker")
	task.Review, task.Reviewers = "cross", []string{"codex", "gemini"}
	if err := newSchedulerStore(f.state).updateTask(task); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REVIEW_VERDICT", "ACCEPT")
	t.Setenv("FAKE_REVIEW_VERDICT_2", "REJECT")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("cross review cycle exit=%d output=%s", code, output)
	}
	stream, err := newSchedulerStore(f.state).stream(task.Stream)
	if err != nil || stream.State != SchedulerAwaitingOrchestrator || stream.Cause != "review-disagreement" || len(stream.ReviewReports) != 2 {
		tasks, _ := newSchedulerStore(f.state).tasks()
		launches, _ := os.ReadFile(f.launchCalls)
		second, secondErr := newSchedulerStore(f.state).stream("worker-a-review-2")
		reports, _ := filepath.Glob(filepath.Join(task.Workdir, ".review-report*"))
		t.Fatalf("disagreement was not routed with both reports: %+v second=%+v secondErr=%v reports=%v tasks=%+v launches=%s err=%v", stream, second, secondErr, reports, tasks, launches, err)
	}
	if stream.ReviewReports[0] == stream.ReviewReports[1] {
		t.Fatalf("cross reviewers shared a report: %v", stream.ReviewReports)
	}
}

// case: S-237.89
func TestSchedulerZCodeReviewUsesNativeAgentAndSkipsUnavailableAccount(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "worker-a", "worker")
	task.Review, task.Reviewers = "cross", []string{"codex", "zcode"}
	if err := newSchedulerStore(f.state).updateTask(task); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ZCODE_FAIL", "0")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("ZCode active review cycle exit=%d output=%s", code, output)
	}
	if _, err := os.Stat(filepath.Join(task.Workdir, ".review-report-worker-a-review-2.md")); err != nil {
		t.Fatalf("active native ZCode agent did not write its private report: %v", err)
	}
	activeCalls, err := os.ReadFile(f.launchCalls)
	if err != nil || !strings.Contains(string(activeCalls), "--vendor zcode") || !strings.Contains(string(activeCalls), "--slot-group agent-scheduler") {
		t.Fatalf("ZCode review bypassed native admission: calls=%q err=%v", activeCalls, err)
	}

	runsB := `[{"databaseId":12,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-b-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	f2 := newSchedulerFixture(t, runsB, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task2 := f2.addTask(t, "worker-b", "worker")
	task2.Review, task2.Reviewers = "cross", []string{"codex", "zcode"}
	if err := newSchedulerStore(f2.state).updateTask(task2); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_ZCODE_FAIL", "1")
	code, output = f2.runOnce(t)
	if code != 0 {
		t.Fatalf("ZCode unavailable review cycle exit=%d output=%s", code, output)
	}
	stream, err := newSchedulerStore(f2.state).stream(task2.Stream)
	if err != nil || stream.State != SchedulerAwaitingRemote || stream.ReviewResults["zcode-2"] != "ACCEPT" {
		t.Fatalf("independent configured reviewer did not replace the skipped ZCode vote: %+v err=%v", stream, err)
	}
	zcodeState, err := newSchedulerStore(f2.state).stream("worker-b-review-2")
	if err != nil || zcodeState.State != SchedulerDone || !strings.Contains(zcodeState.Cause, "ZCode unavailable") {
		t.Fatalf("unavailable ZCode cause was not recorded: %+v err=%v", zcodeState, err)
	}
	fallback, err := newSchedulerStore(f2.state).task("worker-b-review-fallback-codex-zcode-2")
	if err != nil || fallback.ReviewerKey != "zcode-2" || fallback.Vendor != "codex" {
		t.Fatalf("skipped ZCode slot did not receive an independent Codex verdict: %+v err=%v", fallback, err)
	}
}

// case: S-237.75
func TestSchedulerPartialReportStopsAtOrchestrator(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_AGENT_STATUS", "Status: PARTIAL")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("sched run exit=%d output=%s", code, output)
	}
	data, err := os.ReadFile(filepath.Join(f.state, "sched", "streams", "worker-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stream SchedulerStream
	if err := json.Unmarshal(data, &stream); err != nil || stream.State != SchedulerAwaitingOrchestrator {
		t.Fatalf("partial worker report state=%+v decode=%v", stream, err)
	}
	if _, err := os.Stat(f.ghCalls); !os.IsNotExist(err) {
		t.Fatalf("partial report submitted remote verification: %v", err)
	}
}

// case: S-237.75
func TestSchedulerPlanQuestionStopsAtOrchestrator(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_AGENT_QUESTION", "PLAN-QUESTION: confirm the intended boundary")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("sched run exit=%d output=%s", code, output)
	}
	stream, err := newSchedulerStore(f.state).stream("worker-a")
	if err != nil || stream.State != SchedulerAwaitingOrchestrator {
		t.Fatalf("questioned report state=%+v err=%v", stream, err)
	}
	if _, err := os.Stat(f.ghCalls); !os.IsNotExist(err) {
		t.Fatalf("questioned report submitted remote verification: %v", err)
	}
}

// case: S-237.75, S-237.77
func TestSchedulerReleasesWorkerSlotAndOneWatcherCoversPendingRuns(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"queued","conclusion":null},{"databaseId":12,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-b-0123456","headSha":"` + schedulerTestSHA + `","status":"queued","conclusion":null}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "worker-a", "worker")
	f.addTask(t, "worker-b", "worker")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("sched run --once exit=%d output=%s", code, output)
	}
	launches, err := os.ReadFile(f.launchCalls)
	if err != nil || strings.Count(string(launches), "agent start") != 2 {
		t.Fatalf("worker launches=%q err=%v", launches, err)
	}
	ghCalls, err := os.ReadFile(f.ghCalls)
	if err != nil || strings.Count(string(ghCalls), "run list") != 1 || strings.Contains(string(ghCalls), "run view") {
		t.Fatalf("watcher calls=%q err=%v", ghCalls, err)
	}
	gitCalls, err := os.ReadFile(f.gitCalls)
	if err != nil || !strings.Contains(string(gitCalls), "worktree add -B agent/worker-a") || !strings.Contains(string(gitCalls), "push backup "+schedulerTestSHA+":refs/heads/verify/worker-a-"+schedulerTestSHA[:7]) {
		t.Fatalf("worktree or verification push arguments=%q err=%v", gitCalls, err)
	}
	for _, stream := range []string{"worker-a", "worker-b"} {
		data, err := os.ReadFile(filepath.Join(f.state, "sched", "streams", stream+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var got SchedulerStream
		if err := json.Unmarshal(data, &got); err != nil || got.State != SchedulerAwaitingRemote {
			t.Fatalf("stream %s=%+v decode=%v", stream, got, err)
		}
	}
}

// case: S-237.76
func TestSchedulerRejectedReviewArchivesReportAndResumesWorker(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	verdict := `{"schemaVersion":1,"sha":"` + schedulerTestSHA + `","status":"pass","jobs":[],"diagnostics":[]}`
	f := newSchedulerFixture(t, runs, verdict)
	f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_REVIEW_VERDICT", "REJECT")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("scheduler review cycle exit=%d output=%s", code, output)
	}
	worker, err := newSchedulerStore(f.state).stream("worker-a")
	if err != nil || worker.State != SchedulerReady || worker.ThreadID != "fixture-thread" {
		t.Fatalf("rejected review did not ready the original thread: %+v err=%v", worker, err)
	}
	task, err := newSchedulerStore(f.state).task(worker.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(task.Prompt)
	if err != nil || !strings.Contains(string(prompt), "archived review") {
		t.Fatalf("resume prompt does not point to archived review: %q err=%v", prompt, err)
	}
	archives, err := filepath.Glob(filepath.Join(task.Workdir, "reviews", "archive", "*.md"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archived review paths=%v err=%v", archives, err)
	}
	reviewState, err := newSchedulerStore(f.state).stream("worker-a-review")
	if err != nil || reviewState.State != SchedulerDone {
		t.Fatalf("rejected review task did not retain its completed verdict: %+v err=%v", reviewState, err)
	}
	t.Setenv("FAKE_REVIEW_VERDICT", "ACCEPT")
	code, output = f.runOnce(t)
	if code != 0 {
		t.Fatalf("second review cycle exit=%d output=%s", code, output)
	}
	worker, err = newSchedulerStore(f.state).stream("worker-a")
	if err != nil || worker.State != SchedulerAwaitingRemote {
		t.Fatalf("accepted review did not submit an integration candidate: %+v err=%v", worker, err)
	}
}

// case: S-237.78
func TestSchedulerDeadlinePersistsTimeoutVerdict(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "worker-a", "worker")
	f.addAwaitingRemote(t, task, SchedulerSubmission{SHA: schedulerTestSHA, Ref: "verify/worker-a-0123456", RunID: "11", DeadlineAt: time.Now().UTC().Add(-time.Second)})
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("deadline watcher exit=%d output=%s", code, output)
	}
	stream, err := newSchedulerStore(f.state).stream(task.Stream)
	if err != nil || stream.State != SchedulerAwaitingOrchestrator || stream.Cause != "outage-after-retries" || stream.WaitingFor != "remote deadline" {
		t.Fatalf("deadline stream=%+v err=%v", stream, err)
	}
	path := filepath.Join(filepath.Dir(task.RepoRoot), "reports", task.Stream+".remote."+schedulerTestSHA[:7]+".json")
	data, err := os.ReadFile(path)
	var verdict map[string]any
	if err != nil || json.Unmarshal(data, &verdict) != nil || verdict["status"] != "timeout" {
		t.Fatalf("timeout verdict=%s err=%v", data, err)
	}
}

// case: S-237.79
func TestSchedulerOutageRerunsAtMostTwice(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"failure"}]`
	verdict := `{"schemaVersion":1,"sha":"` + schedulerTestSHA + `","status":"outage","jobs":[],"diagnostics":["remote outage"]}`
	f := newSchedulerFixture(t, runs, verdict)
	task := f.addTask(t, "worker-a", "worker")
	f.addAwaitingRemote(t, task, SchedulerSubmission{SHA: schedulerTestSHA, Ref: "verify/worker-a-0123456", RunID: "11", DeadlineAt: time.Now().UTC().Add(time.Hour)})
	for range 3 {
		code, output := f.runOnce(t)
		if code != 0 {
			t.Fatalf("outage watcher exit=%d output=%s", code, output)
		}
	}
	stream, err := newSchedulerStore(f.state).stream(task.Stream)
	if err != nil || stream.State != SchedulerAwaitingOrchestrator || stream.Submission.Attempts != 2 {
		t.Fatalf("outage attempts were not bounded: stream=%+v err=%v", stream, err)
	}
	calls, err := os.ReadFile(f.ghCalls)
	if err != nil || strings.Count(string(calls), "run rerun") != 2 {
		t.Fatalf("remote rerun calls=%q err=%v", calls, err)
	}
}

// case: S-237.80
func TestSchedulerSupersedeCancelsOlderRunAndUsesNewSHA(t *testing.T) {
	oldSHA := strings.Repeat("a", 40)
	runs := `[{"databaseId":23,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-` + oldSHA[:7] + `","headSha":"` + oldSHA + `","status":"queued","conclusion":null}]`
	f := newSchedulerFixture(t, runs, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "worker-a", "worker")
	t.Setenv("FAKE_SHA", schedulerTestSHA)
	stream, err := newSchedulerStore(f.state).stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.Submission = &SchedulerSubmission{SHA: oldSHA, Ref: "verify/worker-a-" + oldSHA[:7], RunID: "23", DeadlineAt: time.Now().UTC().Add(time.Hour)}
	if err := newSchedulerStore(f.state).writeJSON(newSchedulerStore(f.state).streamPath(task.Stream), stream); err != nil {
		t.Fatal(err)
	}
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("superseding scheduler run exit=%d output=%s", code, output)
	}
	stream, err = newSchedulerStore(f.state).stream(task.Stream)
	if err != nil || stream.State != SchedulerAwaitingRemote || stream.Submission.SHA != schedulerTestSHA {
		t.Fatalf("new submission did not supersede old: stream=%+v err=%v", stream, err)
	}
	calls, err := os.ReadFile(f.ghCalls)
	if err != nil || !strings.Contains(string(calls), "run cancel 23") {
		t.Fatalf("old verification run was not cancelled: calls=%q err=%v", calls, err)
	}
}

// case: S-237.81
func TestSchedulerDependencyReleasesAfterOrchestratorCompletes(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"},{"databaseId":12,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/candidate-worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"success"}]`
	verdict := `{"schemaVersion":1,"sha":"` + schedulerTestSHA + `","status":"pass","jobs":[],"diagnostics":[]}`
	f := newSchedulerFixture(t, runs, verdict)
	parent := f.addTask(t, "worker-a", "worker")
	f.addTask(t, "worker-b", "worker", parent.ID)
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("parent verification and review exit=%d output=%s", code, output)
	}
	parentState, err := newSchedulerStore(f.state).stream(parent.Stream)
	if err != nil || parentState.State != SchedulerAwaitingRemote {
		t.Fatalf("accepted parent candidate=%+v err=%v", parentState, err)
	}
	reviewTask, err := newSchedulerStore(f.state).task("worker-a-review")
	if err != nil || reviewTask.Role != schedulerReviewRole || reviewTask.Vendor != "codex" || reviewTask.PromptTemplate != runnerReviewTemplate {
		t.Fatalf("passing verdict did not create the configured review task: %+v err=%v", reviewTask, err)
	}
	reviewPrompt, err := os.ReadFile(filepath.Join(f.state, "sched", "prompts", "worker-a-review.md"))
	if err != nil || !strings.Contains(string(reviewPrompt), "Review the completed stream worker-a") || !strings.Contains(string(reviewPrompt), "Reviewer focus: independent review") {
		t.Fatalf("review template was not rendered with the stream: %q err=%v", reviewPrompt, err)
	}
	templateBefore, err := readRunnerTemplate(runnerReviewTemplate)
	if err != nil {
		t.Fatal(err)
	}
	templateAfter, err := readRunnerTemplate(runnerReviewTemplate)
	if err != nil || string(templateAfter) != string(templateBefore) {
		t.Fatalf("template changed during rendering: %q err=%v", templateAfter, err)
	}
	launches, err := os.ReadFile(f.launchCalls)
	if err != nil || strings.Contains(string(launches), "--stream worker-b ") {
		t.Fatalf("dependent task started before orchestrator completion: %q err=%v", launches, err)
	}
	code, output = f.runOnce(t)
	if code != 0 {
		t.Fatalf("dependent dispatch exit=%d output=%s", code, output)
	}
	launches, err = os.ReadFile(f.launchCalls)
	if err != nil || !strings.Contains(string(launches), "--stream worker-b ") {
		t.Fatalf("dependent task did not dispatch after completion: %q err=%v", launches, err)
	}
}

// case: S-237.76
func TestSchedulerFailureVerdictResumesOriginalThreadWithNamedFailures(t *testing.T) {
	runs := `[{"databaseId":11,"event":"push","path":".github/workflows/remote-verify.yml","headBranch":"verify/worker-a-0123456","headSha":"` + schedulerTestSHA + `","status":"completed","conclusion":"failure"}]`
	verdict := `{"schemaVersion":1,"sha":"` + schedulerTestSHA + `","status":"fail","jobs":[{"name":"unit","status":"fail","failures":["TestSchedulerRegression"],"logTail":"assertion failed"}],"diagnostics":["verification failed"]}`
	f := newSchedulerFixture(t, runs, verdict)
	task := f.addTask(t, "worker-a", "worker")
	code, output := f.runOnce(t)
	if code != 0 {
		t.Fatalf("initial scheduler cycle exit=%d output=%s", code, output)
	}
	data, err := os.ReadFile(filepath.Join(f.state, "sched", "streams", "worker-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	var failed SchedulerStream
	if err := json.Unmarshal(data, &failed); err != nil || failed.State != SchedulerReady || failed.ThreadID != "fixture-thread" {
		t.Fatalf("failure did not route to original READY stream: %+v decode=%v", failed, err)
	}
	savedTask, err := newSchedulerStore(f.state).task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	prompt, err := os.ReadFile(savedTask.Prompt)
	if err != nil || !strings.Contains(string(prompt), "TestSchedulerRegression") || !strings.Contains(string(prompt), "assertion failed") {
		t.Fatalf("failure message was not retained in resume prompt: %q err=%v", prompt, err)
	}
	code, output = f.runOnce(t)
	if code != 0 {
		t.Fatalf("resume cycle exit=%d output=%s", code, output)
	}
	launches, err := os.ReadFile(f.launchCalls)
	if err != nil || !strings.Contains(string(launches), "--session fixture-thread") {
		t.Fatalf("resume did not use original thread: %q err=%v", launches, err)
	}
}

// case: S-237.114
func TestRoundPromptCombinesMessageAndDefinitionOfDone(t *testing.T) {
	message := "continue the task\n"
	definition := "report every proposed commit\n"
	got := composeRoundPrompt(message, definition)
	if got != message+"\n"+definition {
		t.Fatalf("round prompt=%q, want message followed by definition of done", got)
	}
}

// case: S-237.115
func TestReviewVerdictFallsBackToRunnerLastMessage(t *testing.T) {
	dir := t.TempDir()
	last := filepath.Join(dir, "job.last.md")
	if err := os.WriteFile(last, []byte("ACCEPT\nreviewed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, path, err := resolveReviewVerdict([]string{filepath.Join(dir, ".review-report.md"), last})
	if err != nil || got != "ACCEPT" || path != last {
		t.Fatalf("resolveReviewVerdict=%q,%q,%v", got, path, err)
	}
}

// case: S-237.120
func TestProposedCommitGroupsRejectMissingPopulatedSubjectBeforeCommit(t *testing.T) {
	commits, err := parseProposedCommits([]byte("## Proposed commits\n```json\n[{\"kind\":\"case\",\"subject\":\"-\"},{\"kind\":\"tests\",\"subject\":\"test: focused\"},{\"kind\":\"code\",\"subject\":\"fix: code\"}]\n```\n"))
	if err != nil {
		t.Fatalf("empty group marker should parse: %v", err)
	}
	if err := validateCommitGroups(commits, [][]string{{"docs/qa/scenarios/case.md"}, {"tools/kb-runner/main_test.go"}, {"tools/kb-runner/main.go"}}); err == nil {
		t.Fatal("populated case group accepted a missing commit subject")
	}
}

// case: S-237.120
func TestProposedCommitGroupsAllowMissingSubjectOnlyForEmptyGroup(t *testing.T) {
	commits := []proposedCommit{{Kind: "case", Subject: "-"}, {Kind: "tests", Subject: "test(runner): cover case"}, {Kind: "code", Subject: "fix(runner): implement behavior"}}
	groups := [][]string{{}, {"tools/kb-runner/main_test.go"}, {"tools/kb-runner/main.go"}}
	if err := validateCommitGroups(commits, groups); err != nil {
		t.Fatalf("empty case group must be skipped: %v", err)
	}
}

// case: S-237.119
func TestSchedulerAgentArgsUsesGovernorAndZCodeReportLocation(t *testing.T) {
	task := SchedulerTask{Stream: "stream-a", Vendor: "zcode", Effort: "high", Workdir: "/work/stream-a", Role: schedulerWorkerRole, Prompt: "/tmp/prompt"}
	args := schedulerAgentArgs(task, "/tmp/state", "job-a", 2, "")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--slot-group agent-scheduler --slots 2") {
		t.Fatalf("scheduler agent args omit governor admission: %v", args)
	}
	if got := schedulerMessagePath(task); got != filepath.Join(task.Workdir, "reports", task.Stream+".md") {
		t.Fatalf("ZCode report=%q", got)
	}
	for _, arg := range args {
		if strings.Contains(arg, "bypass") {
			t.Fatalf("scheduler passed a bypass input: %q", arg)
		}
	}
}

// case: S-237.114, S-237.119
func TestAgentLaunchUsesRenderedRoundPromptAndGovernor(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "launch-a", "worker")
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"agent", "launch", task.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("agent launch exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	calls, err := os.ReadFile(f.launchCalls)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "--slot-group agent-scheduler --slots") {
		t.Fatalf("agent launch skipped scheduler admission: %s", calls)
	}
	rendered := filepath.Join(f.state, "sched", "prompts", task.ID+"-round.md")
	prompt, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(prompt), "task for launch-a\n\n# Definition of done") {
		t.Fatalf("rendered round prompt=%q", prompt)
	}
}

// case: S-237.115
func TestResumeRefusesToLaunchWhenSnapshotGateFindsUnmergedIndex(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "resume-a", "worker")
	store := newSchedulerStore(f.state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.ThreadID = "fixture-thread"
	if err := store.writeStreamObserved(stream); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_INDEX_UNMERGED", "100644 1 deadbeef 1\tconflict.txt")
	t.Setenv("FAKE_AGENT_CALLS", f.gitCalls)
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"agent", "resume", task.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() == 0 || !strings.Contains(errOut.String(), "cannot snapshot a worktree with unresolved merge entries") {
		t.Fatalf("resume snapshot refusal exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	calls, err := os.ReadFile(f.gitCalls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "agent start") {
		t.Fatalf("resume launched before snapshot gate: %s", calls)
	}
}

// case: S-237.116, S-237.117
func TestFinishReviewReadsRunnerLastMessageAndArchivesItForRework(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	worker := f.addTask(t, "review-fallback", "worker")
	review := f.addTask(t, worker.Stream+"-review", "review", worker.ID)
	store := newSchedulerStore(f.state)
	worker.Review = "cross"
	worker.Reviewers = []string{"codex", "codex"}
	if err := store.updateTask(worker); err != nil {
		t.Fatal(err)
	}
	review.Workdir = worker.Workdir
	review.ReviewerKey = "codex-1"
	if err := store.updateTask(review); err != nil {
		t.Fatal(err)
	}
	workerState, err := store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	workerState.Submission = &SchedulerSubmission{SHA: schedulerTestSHA}
	workerState.ReviewResults = map[string]string{"codex-2": "REJECT"}
	if err := store.writeStreamObserved(workerState); err != nil {
		t.Fatal(err)
	}
	workerState, err = store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(workerState, SchedulerRunning, "agent round", "finish worker"); err != nil {
		t.Fatal(err)
	}
	workerState, err = store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(workerState, SchedulerAwaitingReview, "review task", "run review"); err != nil {
		t.Fatal(err)
	}
	reviewState, err := store.stream(review.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(reviewState, SchedulerRunning, "agent round", "finish review"); err != nil {
		t.Fatal(err)
	}
	reviewState, err = store.stream(review.Stream)
	if err != nil {
		t.Fatal(err)
	}
	lastMessage := filepath.Join(f.state, "jobs", "review.last.md")
	if err := os.MkdirAll(filepath.Dir(lastMessage), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lastMessage, []byte("REJECT\nplease fix the report path\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := schedulerRunOptions{store: store, state: f.state}
	if err := options.finishReview(review, reviewState, lastMessage); err != nil {
		t.Fatal(err)
	}
	updated, err := store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if updated.State != SchedulerReady || updated.RejectRounds != 1 || len(updated.ReviewReports) != 0 {
		t.Fatalf("review rejection did not return worker to READY: %+v", updated)
	}
	archives, err := filepath.Glob(filepath.Join(worker.Workdir, "reviews", "archive", "*.md"))
	if err != nil || len(archives) != 1 {
		t.Fatalf("archived last message paths=%v err=%v", archives, err)
	}
	archived, err := os.ReadFile(archives[0])
	if err != nil || string(archived) != "REJECT\nplease fix the report path\n" {
		t.Fatalf("archived review=%q err=%v", archived, err)
	}
}

// case: S-237.118
func TestZCodeWorkerReportComesFromReportsStreamPath(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "zcode-report", "worker")
	task.Vendor = "zcode"
	store := newSchedulerStore(f.state)
	if err := store.updateTask(task); err != nil {
		t.Fatal(err)
	}
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(stream, SchedulerRunning, "agent round", "wait for report"); err != nil {
		t.Fatal(err)
	}
	path := schedulerMessagePath(task)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Status: PARTIAL\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := Job{ID: "zcode-job", Stream: task.Stream, Vendor: "zcode", State: JobOK, Phase: PhaseRun, SessionID: "zcode-session", MessagePath: path}
	if _, err := newJobStore(f.state).Create(job); err != nil {
		t.Fatal(err)
	}
	options := schedulerRunOptions{store: store, state: f.state}
	if err := options.finishWorker(schedulerWorkerResult{task: task, jobID: job.ID}); err != nil {
		t.Fatal(err)
	}
	updated, err := store.stream(task.Stream)
	if err != nil || updated.State != SchedulerAwaitingOrchestrator {
		t.Fatalf("ZCode reports/<stream>.md did not control worker outcome: stream=%+v err=%v", updated, err)
	}
}

// case: S-237.122
func TestZCodeLifecycleUsesPerStreamReportAndResumesSessionAfterSnapshot(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "zcode-lifecycle", "worker")
	task.Vendor = "zcode"
	store := newSchedulerStore(f.state)
	if err := store.updateTask(task); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENT_STATUS", "Status: PARTIAL")
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"agent", "launch", "--vendor", "zcode", task.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("ZCode launch exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	message := filepath.Join(task.Workdir, "reports", task.Stream+".md")
	if _, err := os.Stat(message); err != nil {
		t.Fatalf("ZCode worker message was not written at %s: %v", message, err)
	}
	stream, err := store.stream(task.Stream)
	if err != nil || stream.ThreadID != "fixture-thread" {
		t.Fatalf("ZCode session was not persisted: %+v err=%v", stream, err)
	}
	if err := store.transition(stream, SchedulerReady, "test resume", "run resume"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CHANGED_FILES", "")
	if err := os.WriteFile(f.gitCalls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_AGENT_CALLS", f.gitCalls)
	t.Setenv("FAKE_GIT_CALLS", f.gitCalls)
	status = executeInvocation("kb-runner", []string{"agent", "resume", "--vendor", "zcode", task.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("ZCode resume exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	calls, err := os.ReadFile(f.launchCalls)
	roundPrompt := filepath.Join(f.state, "sched", "prompts", task.ID+"-round.md")
	resumeSequence, sequenceErr := os.ReadFile(f.gitCalls)
	if err != nil || strings.Count(string(calls), "--vendor zcode") != 1 || !strings.Contains(string(calls), "--prompt-file "+roundPrompt) || !strings.Contains(string(calls), "--message "+message) || sequenceErr != nil || !strings.Contains(string(resumeSequence), "--vendor zcode") || !strings.Contains(string(resumeSequence), "--prompt-file "+roundPrompt) || !strings.Contains(string(resumeSequence), "--session fixture-thread") {
		t.Fatalf("ZCode launch/resume arguments=%q err=%v", calls, err)
	}
	round, err := os.ReadFile(roundPrompt)
	if err != nil || !strings.Contains(string(round), "task for "+task.Stream) || !strings.Contains(string(round), "Definition of done") {
		t.Fatalf("ZCode per-stream task message=%q err=%v", round, err)
	}
	if strings.Index(string(resumeSequence), "status --porcelain") < 0 || strings.Index(string(resumeSequence), "status --porcelain") > strings.Index(string(resumeSequence), "agent start") {
		t.Fatalf("ZCode resume did not snapshot before agent launch: sequence=%q", resumeSequence)
	}
}

// case: S-237.116
func TestAgentReviewDispatchesPersistedReviewTask(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	worker := f.addTask(t, "review-command", "worker")
	review := f.addTask(t, worker.Stream+"-review", "review", worker.ID)
	store := newSchedulerStore(f.state)
	worker.Review = "cross"
	worker.Reviewers = []string{"codex", "codex"}
	if err := store.updateTask(worker); err != nil {
		t.Fatal(err)
	}
	review.Workdir = worker.Workdir
	review.ReviewerKey = "codex-1"
	if err := store.updateTask(review); err != nil {
		t.Fatal(err)
	}
	workerState, err := store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	workerState.Submission = &SchedulerSubmission{SHA: schedulerTestSHA}
	workerState.ReviewResults = map[string]string{"codex-2": "REJECT"}
	if err := store.writeStreamObserved(workerState); err != nil {
		t.Fatal(err)
	}
	workerState, err = store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(workerState, SchedulerRunning, "agent round", "finish worker"); err != nil {
		t.Fatal(err)
	}
	workerState, err = store.stream(worker.Stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.transition(workerState, SchedulerAwaitingReview, "review task", "run review"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_REVIEW_VERDICT", "REJECT")
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"agent", "review", worker.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("agent review exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	calls, err := os.ReadFile(f.launchCalls)
	if err != nil || !strings.Contains(string(calls), "--stream "+review.Stream) {
		t.Fatalf("review task was not dispatched through agent start: calls=%q err=%v", calls, err)
	}
	updated, err := store.stream(worker.Stream)
	if err != nil || updated.State != SchedulerReady || updated.RejectRounds != 1 {
		reviewOutcome, reviewErr := store.stream(review.Stream)
		report, readErr := os.ReadFile(privateReviewReportPath(review))
		calls, callsErr := os.ReadFile(f.launchCalls)
		t.Fatalf("review did not schedule rework: stream=%+v err=%v review=%+v reviewErr=%v report=%q readErr=%v calls=%q callsErr=%v", updated, err, reviewOutcome, reviewErr, report, readErr, calls, callsErr)
	}
}

// case: S-237.117
func TestAgentReworkResumesRejectedWorkerThread(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	task := f.addTask(t, "rework-command", "worker")
	store := newSchedulerStore(f.state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.ThreadID = "original-thread"
	stream.RejectRounds = 1
	stream.State = SchedulerReady
	if err := store.writeStreamObserved(stream); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"agent", "rework", task.Stream, "--state", f.state}, &out, &errOut)
	if status.Code() != 0 {
		t.Fatalf("agent rework exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
	calls, err := os.ReadFile(f.launchCalls)
	if err != nil || !strings.Contains(string(calls), "--session original-thread") {
		t.Fatalf("rework did not resume original thread: calls=%q err=%v", calls, err)
	}
}

// case: S-237.120, S-237.131
func TestLandCreatesOrderedCommitGroupsInNativeGit(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	t.Setenv("PATH", filepath.Dir(gitPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	run := func(args ...string) string {
		t.Helper()
		command := exec.Command(gitPath, append([]string{"-C", repo}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, output, err)
		}
		return strings.TrimSpace(string(output))
	}
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.test"}} {
		run(args...)
	}
	for name, body := range map[string]string{"base.txt": "base\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run("add", "-A")
	run("commit", "-m", "chore: base")
	run("checkout", "-b", "agent/land-a")
	for name, body := range map[string]string{"docs/qa/case.md": "case\n", "tools/runner/runner_test.go": "test\n", "tools/runner/runner.go": "code\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	verifiedSHA, _, err := snapshotWorktree(repo, "land-a")
	if err != nil {
		t.Fatal(err)
	}
	verifiedTree, err := treeSHAForCommit(repo, verifiedSHA)
	if err != nil {
		t.Fatal(err)
	}
	task := SchedulerTask{Stream: "land-a", Workdir: repo}
	expectedOld := run("rev-parse", "HEAD")
	commits := []proposedCommit{{Kind: "case", Subject: "docs(qa): add case"}, {Kind: "tests", Subject: "test(runner): cover case"}, {Kind: "code", Subject: "fix(runner): implement behavior", Body: "details"}}
	object, err := buildLandingObjects(repo, task.Stream, verifiedTree, expectedOld, commits, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := moveLandingRef(repo, task.Stream, object, expectedOld); err != nil {
		t.Fatal(err)
	}
	if err := refreshLandingWorktree(repo, repo, object.CommitSHA); err != nil {
		t.Fatal(err)
	}
	log := run("log", "-3", "--format=%s")
	want := "fix(runner): implement behavior\ntest(runner): cover case\ndocs(qa): add case"
	if log != want {
		t.Fatalf("commit order newest-first=%q want %q", log, want)
	}
	committedTree, err := treeSHAForCommit(repo, run("rev-parse", "HEAD"))
	if err != nil || committedTree != verifiedTree {
		t.Fatalf("grouped commits tree=%s want verified tree=%s err=%v", committedTree, verifiedTree, err)
	}
}

// case: S-237.120
func TestLandCommandRequiresPassingRemoteAndAcceptedReview(t *testing.T) {
	f := newSchedulerFixture(t, `[]`, `{"schemaVersion":1,"sha":"`+schedulerTestSHA+`","status":"pass","jobs":[],"diagnostics":[]}`)
	f.addTask(t, "land-gate", "worker")
	var out, errOut bytes.Buffer
	status := executeInvocation("kb-runner", []string{"land", "land-gate", "--state", f.state}, &out, &errOut)
	if status.Code() != 2 || !strings.Contains(errOut.String(), "requires accepted review and a passing latest remote verdict") {
		t.Fatalf("land readiness gate exit=%d stdout=%s stderr=%s", status.Code(), out.String(), errOut.String())
	}
}
