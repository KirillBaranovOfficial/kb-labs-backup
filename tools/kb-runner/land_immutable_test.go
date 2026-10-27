//go:build darwin || linux

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// case: S-237.136
func TestLandingCommitObjectsExcludePostVerificationWorktreeEdits(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*testing.T, string)
	}{
		{name: "tracked", edit: func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, "src.go"), []byte("after verification\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "untracked", edit: func(t *testing.T, repo string) {
			if err := os.WriteFile(filepath.Join(repo, "late.txt"), []byte("after verification\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo, git := newImmutableLandRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "src.go"), []byte("verified change\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			verifiedSHA, _, err := snapshotWorktree(repo, "immutable-"+test.name)
			if err != nil {
				t.Fatal(err)
			}
			verifiedTree, err := treeSHAForCommit(repo, verifiedSHA)
			if err != nil {
				t.Fatal(err)
			}
			test.edit(t, repo)
			commits := []proposedCommit{{Kind: "case", Subject: "docs(qa): case"}, {Kind: "tests", Subject: "test(kb-runner): tests"}, {Kind: "code", Subject: "fix(kb-runner): code"}}
			object, err := buildLandingObjects(repo, "immutable-"+test.name, verifiedTree, git("rev-parse", "HEAD"), commits, t.TempDir())
			if err != nil || object.TreeSHA != verifiedTree {
				t.Fatalf("landing object included post-verification %s edit: commit=%s tree=%s verified tree=%s err=%v", test.name, object.CommitSHA, object.TreeSHA, verifiedTree, err)
			}
		})
	}
}

// case: S-237.136
func TestSchedulerMergeUsesImmutableLandingCommitAfterBranchAmendment(t *testing.T) {
	repo, git := newImmutableLandRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "kb-runner", "scheduler-policy.json"), []byte(`{"integration_branch":"agent/integration","risk_zones":["tools/kb-runner/**"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tools/kb-runner/scheduler-policy.json")
	git("commit", "-m", "chore: scheduler policy")
	git("checkout", "-b", "agent/immutable-merge")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("verified feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-report.md"), []byte("Status: DONE\n"+r2CommitReport), 0o600); err != nil {
		t.Fatal(err)
	}
	expectedOld := git("rev-parse", "HEAD")
	verifiedSHA, _, err := snapshotWorktree(repo, "immutable-merge")
	if err != nil {
		t.Fatal(err)
	}
	verifiedTree, err := treeSHAForCommit(repo, verifiedSHA)
	if err != nil {
		t.Fatal(err)
	}
	git("checkout", "-b", "fixture-intruder")
	if err := os.WriteFile(filepath.Join(repo, "intruder.txt"), []byte("must not merge\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "intruder.txt")
	git("commit", "-m", "chore: concurrent amendment")
	intruderSHA := git("rev-parse", "HEAD")
	git("checkout", "agent/immutable-merge")
	if err := os.Remove(filepath.Join(repo, "intruder.txt")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	bin := t.TempDir()
	pushCalls := filepath.Join(t.TempDir(), "push.calls")
	shim := `#!/bin/sh
case " $* " in
  *" merge-tree --write-tree "*) "$REAL_GIT" -C "$LAND_REPO" update-ref refs/heads/agent/immutable-merge "$INTRUDER_SHA" ;;
  *" push backup "*) printf '%s\n' "$*" >> "$LAND_PUSH_CALLS"; exit 0 ;;
esac
exec "$REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", gitPath)
	t.Setenv("LAND_REPO", repo)
	t.Setenv("INTRUDER_SHA", intruderSHA)
	t.Setenv("LAND_PUSH_CALLS", pushCalls)
	if _, err := exec.Command(gitPath, "-C", repo, "remote", "add", "backup", "https://github.com/kb-labs/backup.git").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	worktrees := t.TempDir()
	state := t.TempDir()
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("worker prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := newSchedulerStore(state).add(SchedulerTask{ID: "immutable-merge", Stream: "immutable-merge", Prompt: prompt, Workdir: repo, RepoRoot: repo, WorktreeRoot: worktrees, Base: "main", Vendor: "codex", Effort: "high", Role: schedulerWorkerRole, Review: "single", Reviewers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	store := newSchedulerStore(state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.Submission = &SchedulerSubmission{SHA: verifiedSHA, TreeSHA: verifiedTree, ExpectedRefSHA: expectedOld, Status: "completed", Conclusion: "success"}
	stream.ReviewResults = map[string]string{"codex": "ACCEPT"}
	if err := store.transition(stream, SchedulerRunning, "worker", "worker done"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingRemote, "remote", "remote passed"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingReview, "review", "review accepted"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	landed, mergeSHA, err := (schedulerRunOptions{store: store, root: runnerRoot{invocation: "kb-runner"}, state: state}).landAccepted(task, stream)
	if err != nil || !landed {
		t.Fatalf("immutable landing failed: landed=%t err=%v", landed, err)
	}
	integration := filepath.Join(worktrees, "integration")
	if _, err := os.Stat(integration); err != nil {
		t.Fatal(err)
	}
	mergedTree, err := treeSHAForCommit(integration, mergeSHA)
	if err != nil || mergedTree != verifiedTree {
		t.Fatalf("integration merged a mutable branch amendment: merged tree=%s verified tree=%s err=%v", mergedTree, verifiedTree, err)
	}
	stream, err = store.stream(task.Stream)
	if err != nil || stream.Submission == nil || stream.Submission.SHA != mergeSHA {
		t.Fatalf("candidate submission did not retain the exact immutable merge SHA: stream=%+v merge=%s err=%v", stream, mergeSHA, err)
	}
	pushed, err := os.ReadFile(pushCalls)
	if err != nil || !strings.Contains(string(pushed), "push backup "+mergeSHA+":refs/heads/verify/candidate-immutable-merge-") {
		t.Fatalf("candidate did not push the exact landing merge SHA: calls=%q merge=%s err=%v", pushed, mergeSHA, err)
	}
	if _, err := os.Stat(filepath.Join(integration, "intruder.txt")); !os.IsNotExist(err) {
		t.Fatalf("integration contains post-commit branch amendment: stat err=%v", err)
	}
	if current := git("rev-parse", "refs/heads/agent/immutable-merge"); current != intruderSHA {
		t.Fatalf("test interleave did not amend the worker ref; ref=%s want %s", current, intruderSHA)
	}
}

// case: S-237.136
func TestLandingRefCompareAndSwapRefusesAmendmentAndResubmits(t *testing.T) {
	repo, git := newImmutableLandRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "kb-runner", "scheduler-policy.json"), []byte(`{"integration_branch":"agent/integration","risk_zones":["tools/kb-runner/**"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tools/kb-runner/scheduler-policy.json")
	git("commit", "-m", "chore: scheduler policy")
	git("checkout", "-b", "agent/immutable-cas")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("verified feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-report.md"), []byte("Status: DONE\n"+r2CommitReport), 0o600); err != nil {
		t.Fatal(err)
	}
	expectedOld := git("rev-parse", "HEAD")
	verifiedSHA, _, err := snapshotWorktree(repo, "immutable-cas")
	if err != nil {
		t.Fatal(err)
	}
	verifiedTree, err := treeSHAForCommit(repo, verifiedSHA)
	if err != nil {
		t.Fatal(err)
	}
	git("checkout", "-b", "fixture-cas-intruder")
	if err := os.WriteFile(filepath.Join(repo, "intruder.txt"), []byte("concurrent ref change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "intruder.txt")
	git("commit", "-m", "chore: concurrent ref change")
	intruderSHA := git("rev-parse", "HEAD")
	git("checkout", "agent/immutable-cas")
	if err := os.Remove(filepath.Join(repo, "intruder.txt")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	pushCalls := filepath.Join(t.TempDir(), "push.calls")
	shim := `#!/bin/sh
case " $* " in
  *" update-ref refs/heads/agent/immutable-cas "*)
    if [ "${LAND_CAS_RACE_DONE:-0}" = 0 ]; then
      "$REAL_GIT" -C "$LAND_REPO" update-ref refs/heads/agent/immutable-cas "$INTRUDER_SHA"
      export LAND_CAS_RACE_DONE=1
    fi ;;
  *" push backup "*) printf '%s\n' "$*" >> "$LAND_PUSH_CALLS"; exit 0 ;;
esac
exec "$REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", gitPath)
	t.Setenv("LAND_REPO", repo)
	t.Setenv("INTRUDER_SHA", intruderSHA)
	t.Setenv("LAND_PUSH_CALLS", pushCalls)
	if _, err := exec.Command(gitPath, "-C", repo, "remote", "add", "backup", "https://github.com/kb-labs/backup.git").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	state, worktrees := t.TempDir(), t.TempDir()
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("worker prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := newSchedulerStore(state).add(SchedulerTask{ID: "immutable-cas", Stream: "immutable-cas", Prompt: prompt, Workdir: repo, RepoRoot: repo, WorktreeRoot: worktrees, Base: "main", Vendor: "codex", Effort: "high", Role: schedulerWorkerRole, Review: "single", Reviewers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	store := newSchedulerStore(state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.Submission = &SchedulerSubmission{SHA: verifiedSHA, TreeSHA: verifiedTree, ExpectedRefSHA: expectedOld, Status: "completed", Conclusion: "success"}
	stream.ReviewResults = map[string]string{"codex": "ACCEPT"}
	if err := store.transition(stream, SchedulerRunning, "worker", "worker done"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingRemote, "remote", "remote passed"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingReview, "review", "review accepted"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	landed, _, err := (schedulerRunOptions{store: store, root: runnerRoot{invocation: "kb-runner"}, state: state}).landAccepted(task, stream)
	if err != nil || landed {
		t.Fatalf("compare-and-swap race should queue fresh verification: landed=%t err=%v", landed, err)
	}
	got, err := store.stream(task.Stream)
	if err != nil || got.State != SchedulerAwaitingRemote || got.Cause != "tree-changed-after-verify" || got.Submission.SHA != intruderSHA || len(got.ReviewResults) != 0 {
		t.Fatalf("CAS refusal did not start a fresh immutable verification: stream=%+v err=%v", got, err)
	}
	if branch := git("rev-parse", "refs/heads/agent/immutable-cas"); branch != intruderSHA {
		t.Fatalf("failed CAS partially moved the worker ref: %s != %s", branch, intruderSHA)
	}
	if _, err := os.Stat(filepath.Join(worktrees, "integration")); !os.IsNotExist(err) {
		t.Fatalf("CAS refusal proceeded to integration merge: %v", err)
	}
	pushed, err := os.ReadFile(pushCalls)
	if err != nil || !strings.Contains(string(pushed), intruderSHA) {
		t.Fatalf("CAS refusal did not submit the amended immutable commit: calls=%q err=%v", pushed, err)
	}
}

// case: S-237.136
func TestIntegrationCASRefusesConcurrentAdvanceWithoutPushingIt(t *testing.T) {
	repo, git := newImmutableLandRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "tools", "kb-runner", "scheduler-policy.json"), []byte(`{"integration_branch":"agent/integration","risk_zones":["tools/kb-runner/**"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "tools/kb-runner/scheduler-policy.json")
	git("commit", "-m", "chore: scheduler policy")
	git("checkout", "-b", "agent/integration-cas")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("verified feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".agent-report.md"), []byte("Status: DONE\n"+r2CommitReport), 0o600); err != nil {
		t.Fatal(err)
	}
	expectedWorker := git("rev-parse", "HEAD")
	verifiedSHA, _, err := snapshotWorktree(repo, "integration-cas")
	if err != nil {
		t.Fatal(err)
	}
	verifiedTree, err := treeSHAForCommit(repo, verifiedSHA)
	if err != nil {
		t.Fatal(err)
	}
	integrationOld := git("rev-parse", "HEAD")
	git("checkout", "-b", "fixture-integration-intruder")
	if err := os.WriteFile(filepath.Join(repo, "intruder.txt"), []byte("concurrent integration change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "intruder.txt")
	git("commit", "-m", "chore: concurrent integration commit")
	intruderSHA := git("rev-parse", "HEAD")
	git("checkout", "agent/integration-cas")
	if err := os.Remove(filepath.Join(repo, "intruder.txt")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	pushCalls := filepath.Join(t.TempDir(), "push.calls")
	shim := `#!/bin/sh
case " $* " in
  *" -C $LAND_INTEGRATION update-ref refs/heads/agent/integration "*)
    "$REAL_GIT" -C "$LAND_REPO" update-ref refs/heads/agent/integration "$INTRUDER_SHA"
    ;;
  *" push backup "*) printf '%s\n' "$*" >> "$LAND_PUSH_CALLS"; exit 0 ;;
esac
exec "$REAL_GIT" "$@"
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REAL_GIT", gitPath)
	t.Setenv("LAND_REPO", repo)
	t.Setenv("INTRUDER_SHA", intruderSHA)
	t.Setenv("LAND_PUSH_CALLS", pushCalls)
	if _, err := exec.Command(gitPath, "-C", repo, "remote", "add", "backup", "https://github.com/kb-labs/backup.git").CombinedOutput(); err != nil {
		t.Fatal(err)
	}

	worktrees, state := t.TempDir(), t.TempDir()
	t.Setenv("LAND_INTEGRATION", filepath.Join(worktrees, "integration"))
	prompt := filepath.Join(t.TempDir(), "prompt.md")
	if err := os.WriteFile(prompt, []byte("worker prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task, err := newSchedulerStore(state).add(SchedulerTask{ID: "integration-cas", Stream: "integration-cas", Prompt: prompt, Workdir: repo, RepoRoot: repo, WorktreeRoot: worktrees, Base: "main", Vendor: "codex", Effort: "high", Role: schedulerWorkerRole, Review: "single", Reviewers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	store := newSchedulerStore(state)
	stream, err := store.stream(task.Stream)
	if err != nil {
		t.Fatal(err)
	}
	stream.Submission = &SchedulerSubmission{SHA: verifiedSHA, TreeSHA: verifiedTree, ExpectedRefSHA: expectedWorker, Status: "completed", Conclusion: "success"}
	stream.ReviewResults = map[string]string{"codex": "ACCEPT"}
	if err := store.transition(stream, SchedulerRunning, "worker", "worker done"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingRemote, "remote", "remote passed"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	if err := store.transition(stream, SchedulerAwaitingReview, "review", "review accepted"); err != nil {
		t.Fatal(err)
	}
	stream, _ = store.stream(task.Stream)
	landed, mergeSHA, err := (schedulerRunOptions{store: store, root: runnerRoot{invocation: "kb-runner"}, state: state}).landAccepted(task, stream)
	var conflict *integrationRefConflict
	if !errors.As(err, &conflict) || landed {
		t.Fatalf("integration compare-and-swap race should refuse landing: landed=%t err=%v", landed, err)
	}
	if conflict.Expected != integrationOld || conflict.Ref != "refs/heads/agent/integration" {
		t.Fatalf("typed CAS refusal lost the captured integration identity: conflict=%+v expected old=%s", conflict, integrationOld)
	}
	if mergeSHA != "" {
		t.Fatalf("refused landing returned merge SHA %s", mergeSHA)
	}
	if branch := git("rev-parse", "refs/heads/agent/integration"); branch != intruderSHA {
		t.Fatalf("integration CAS overwrote concurrent commit: branch=%s want=%s", branch, intruderSHA)
	}
	if got, readErr := os.ReadFile(pushCalls); !os.IsNotExist(readErr) || len(got) != 0 {
		t.Fatalf("candidate or bisect push occurred after integration CAS refusal: calls=%q err=%v", got, readErr)
	}
	stream, err = store.stream(task.Stream)
	if err != nil || stream.State != SchedulerReady {
		t.Fatalf("integration CAS refusal did not return stream to READY: stream=%+v err=%v", stream, err)
	}
}

func newImmutableLandRepo(t *testing.T) (string, func(...string) string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
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
	if err := os.MkdirAll(filepath.Join(repo, "tools", "kb-runner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "src.go"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "chore: base")
	return repo, run
}

func gitFrom(t *testing.T, gitPath, repo string, args ...string) string {
	t.Helper()
	command := exec.Command(gitPath, append([]string{"-C", repo}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, output, err)
	}
	return strings.TrimSpace(string(output))
}
