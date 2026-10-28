//go:build darwin || linux

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kb-labs/clikit/diag"
)

type schedulerPolicy struct {
	IntegrationBranch string   `json:"integration_branch"`
	RiskZones         []string `json:"risk_zones"`
}

type proposedCommit struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

var errGeminiCooldown = errors.New("Gemini reviewers are cooling down")

func (options schedulerRunOptions) policy(task SchedulerTask) (schedulerPolicy, error) {
	path := filepath.Join(task.RepoRoot, "tools", "kb-runner", "scheduler-policy.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return schedulerPolicy{}, fmt.Errorf("read scheduler policy %s: %w", path, err)
	}
	var policy schedulerPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return schedulerPolicy{}, fmt.Errorf("decode scheduler policy %s: %w", path, err)
	}
	if policy.IntegrationBranch == "" || len(policy.RiskZones) == 0 {
		return schedulerPolicy{}, fmt.Errorf("scheduler policy %s requires integration_branch and risk_zones", path)
	}
	return policy, nil
}

func (options schedulerRunOptions) reviewPolicy(task SchedulerTask, stream SchedulerStream) (string, []string, error) {
	if task.Review == "cross" || stream.RejectRounds >= 1 {
		return "cross", normalizeSchedulerReviewers(task), nil
	}
	policy, err := options.policy(task)
	if err != nil {
		return "", nil, err
	}
	changed, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "diff", "--name-only", task.Base+"..."+stream.Submission.SHA)
	if err != nil {
		return "", nil, fmt.Errorf("read changed paths for review policy: %w", err)
	}
	for _, name := range strings.Split(changed, "\n") {
		if name == "" {
			continue
		}
		if containsSchedulerPath(policy.RiskZones, name) {
			return "cross", normalizeSchedulerReviewers(task), nil
		}
	}
	return "single", task.Reviewers[:1], nil
}

func normalizeSchedulerReviewers(task SchedulerTask) []string {
	reviewers := append([]string(nil), task.Reviewers...)
	if len(reviewers) < 2 {
		vendor := task.Vendor
		if len(reviewers) == 1 {
			vendor = reviewers[0]
		}
		reviewers = []string{vendor, vendor}
	}
	return reviewers
}

func containsSchedulerPath(zones []string, name string) bool {
	for _, zone := range zones {
		prefix := strings.TrimSuffix(zone, "**")
		if prefix != zone && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func parseProposedCommits(report []byte) ([]proposedCommit, error) {
	text := string(report)
	start := strings.Index(text, "## Proposed commits\n```json\n")
	if start < 0 {
		return nil, errors.New("no-proposed-commits")
	}
	dataStart := start + len("## Proposed commits\n```json\n")
	end := strings.Index(text[dataStart:], "\n```")
	if end < 0 {
		return nil, errors.New("no-proposed-commits")
	}
	var commits []proposedCommit
	if err := json.Unmarshal([]byte(text[dataStart:dataStart+end]), &commits); err != nil {
		return nil, errors.New("no-proposed-commits")
	}
	if len(commits) != 3 {
		return nil, errors.New("no-proposed-commits")
	}
	for i, expected := range []string{"case", "tests", "code"} {
		if commits[i].Kind != expected || strings.TrimSpace(commits[i].Subject) == "" {
			return nil, errors.New("no-proposed-commits")
		}
	}
	return commits, nil
}

func (options schedulerRunOptions) landAccepted(task SchedulerTask, stream SchedulerStream) (bool, string, error) {
	gate := schedulerReviewGate(task, stream)
	if !gate.allowsLand() {
		return false, "", fmt.Errorf("land refused for %s: %s; every required reviewer must ACCEPT", task.Stream, gate.refusal())
	}
	if stream.Submission == nil || stream.Submission.TreeSHA == "" || stream.Submission.ExpectedRefSHA == "" {
		return false, "", fmt.Errorf("land refused for %s: the passing remote submission has no verified tree and expected branch commit; next: %s sched ps --state %q", task.Stream, options.root.InvocationCommand(), options.state)
	}
	verifiedTree := stream.Submission.TreeSHA
	verifiedReport, err := readVerifiedWorkerReport(task, verifiedTree)
	if err != nil {
		return false, "", fmt.Errorf("read worker report from verified tree: %w", err)
	}
	status, _, err := readReportBytes(verifiedReport)
	if err != nil || status != "Status: DONE" {
		stream.Cause = "plan-question"
		if transitionErr := options.store.transition(stream, SchedulerAwaitingOrchestrator, "verified worker report", "inspect the worker report included in the verified tree"); transitionErr != nil {
			return false, "", errors.Join(err, transitionErr)
		}
		return false, "", nil
	}
	commits, err := parseProposedCommits(verifiedReport)
	if err != nil {
		stream.Cause = "no-proposed-commits"
		return false, "", options.store.transition(stream, SchedulerAwaitingOrchestrator, "land proposals", "add a parseable Proposed commits section, then resume")
	}
	nextCommand := fmt.Sprintf("%s land %s --state %q", options.root.InvocationCommand(), task.Stream, options.state)
	object, err := buildLandingObjects(task.RepoRoot, task.Stream, verifiedTree, stream.Submission.ExpectedRefSHA, commits, filepath.Join(options.state, "jobs", task.ID, "tmp"), nextCommand)
	if err != nil {
		return false, "", fmt.Errorf("build verified landing objects for %s: %w; next: %s land %s --state %q", task.Stream, err, options.root.InvocationCommand(), task.Stream, options.state)
	}
	if err := moveLandingRef(task.RepoRoot, task.Stream, object, stream.Submission.ExpectedRefSHA); err != nil {
		var conflict *landingRefConflict
		if errors.As(err, &conflict) {
			if resubmitErr := options.resubmitLandingRefConflict(task, stream, conflict); resubmitErr != nil {
				return false, "", errors.Join(conflict, resubmitErr)
			}
			return false, "", nil
		}
		return false, "", err
	}
	if err := refreshLandingWorktree(task.RepoRoot, task.Workdir, object.CommitSHA); err != nil {
		return false, "", err
	}
	policy, err := options.policy(task)
	if err != nil {
		return false, "", err
	}
	integration := filepath.Join(task.WorktreeRoot, strings.TrimPrefix(policy.IntegrationBranch, "agent/"))
	if _, err := os.Stat(integration); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(integration), 0o700); err != nil {
			return false, "", err
		}
		if _, err := schedulerCommandOutput("", "git", "-C", task.RepoRoot, "worktree", "add", "-B", policy.IntegrationBranch, integration, task.Base); err != nil {
			return false, "", fmt.Errorf("create integration worktree: %w", err)
		}
	}
	ref := "refs/heads/" + policy.IntegrationBranch
	integrationSHA, err := schedulerCommandOutput(integration, "git", "-C", integration, "rev-parse", ref)
	if err != nil {
		return false, "", fmt.Errorf("resolve integration branch at landing start: %w", err)
	}
	integrationSHA = strings.TrimSpace(integrationSHA)
	if !fullGitSHA.MatchString(integrationSHA) {
		return false, "", fmt.Errorf("integration branch returned invalid SHA %q", integrationSHA)
	}
	mergeTreeOutput, mergeErr := schedulerCommandOutput(integration, "git", "-C", integration, "merge-tree", "--write-tree", integrationSHA, object.CommitSHA)
	if mergeErr != nil {
		// Keep the existing conflict resolver flow for conflicted trees. The branch
		// remains at integrationSHA because merge-tree did not move any ref.
		_, mergeErr = schedulerCommandOutput(integration, "git", "-C", integration, "merge", "--no-ff", "--no-edit", object.CommitSHA)
		if mergeErr == nil {
			return false, "", errors.New("merge-tree reported a conflict but the integration worktree merge completed")
		}
		integrationConflicts, diffErr := schedulerCommandOutput(integration, "git", "-C", integration, "diff", "--name-only", "--diff-filter=U")
		if diffErr != nil {
			return false, "", errors.Join(mergeErr, fmt.Errorf("inspect integration merge conflicts: %w", diffErr))
		}
		if strings.TrimSpace(integrationConflicts) == "" {
			return false, "", mergeErr
		}
		if _, err := schedulerCommandOutput(integration, "git", "-C", integration, "merge", "--abort"); err != nil {
			return false, "", errors.Join(mergeErr, fmt.Errorf("abort integration merge: %w", err))
		}
		if _, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "merge", policy.IntegrationBranch); err != nil {
			return false, "", errors.Join(mergeErr, fmt.Errorf("merge integration branch into stream worktree: %w", err))
		}
		unmerged, diffErr := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "diff", "--name-only", "--diff-filter=U")
		if diffErr != nil || strings.TrimSpace(unmerged) == "" {
			return false, "", fmt.Errorf("integration merge conflicted but stream worktree has no unresolved files: %w", mergeErr)
		}
		return false, "", options.queueMergeResolver(task, stream)
	}
	mergeTreeSHA := strings.TrimSpace(strings.SplitN(mergeTreeOutput, "\n", 2)[0])
	if !fullGitSHA.MatchString(mergeTreeSHA) {
		return false, "", fmt.Errorf("merge-tree returned invalid tree SHA %q", mergeTreeSHA)
	}
	mergeSHA, err := createCommitObject(integration, mergeTreeSHA, []string{integrationSHA, object.CommitSHA}, "Merge "+object.CommitSHA+" into "+policy.IntegrationBranch)
	if err != nil {
		return false, "", fmt.Errorf("create immutable integration merge commit: %w", err)
	}
	mergeInputTree, err := treeSHAForCommit(task.RepoRoot, object.CommitSHA)
	if err != nil {
		return false, "", fmt.Errorf("verify immutable integration merge input: %w", err)
	}
	if mergeInputTree != object.TreeSHA {
		return false, "", fmt.Errorf("integration merge input tree %s differs from sanitized landing tree %s", mergeInputTree, object.TreeSHA)
	}
	mergeCommitTree, err := treeSHAForCommit(task.RepoRoot, mergeSHA)
	if err != nil {
		return false, "", fmt.Errorf("verify created integration merge commit: %w", err)
	}
	if mergeCommitTree != mergeTreeSHA {
		return false, "", fmt.Errorf("integration merge commit tree %s differs from merge result %s", mergeCommitTree, mergeTreeSHA)
	}
	if _, err := schedulerCommandOutput(integration, "git", "-C", integration, "update-ref", ref, mergeSHA, integrationSHA); err != nil {
		stream.Cause = "integration-branch-advanced"
		conflict := &integrationRefConflict{Ref: ref, Expected: integrationSHA}
		if transitionErr := options.store.transition(stream, SchedulerReady, "integration branch advanced during landing", "rerun the accepted land against the current integration branch"); transitionErr != nil {
			return false, "", errors.Join(conflict, transitionErr)
		}
		return false, "", conflict
	}
	if _, err := schedulerCommandOutput(integration, "git", "-C", integration, "reset", "--mixed", mergeSHA); err != nil {
		return false, "", fmt.Errorf("refresh integration worktree after compare-and-swap: %w", err)
	}
	if err := options.store.recordIntegrationMerge(SchedulerMergeRecord{CommitSHA: mergeSHA, ParentSHA: integrationSHA, Stream: task.Stream}); err != nil {
		return false, "", fmt.Errorf("record integration merge: %w", err)
	}
	if err := options.submitCandidate(task, stream, mergeSHA); err != nil {
		return false, "", err
	}
	return true, mergeSHA, nil
}

func (options schedulerRunOptions) resubmitLandingRefConflict(task SchedulerTask, stream SchedulerStream, conflict *landingRefConflict) error {
	if !fullGitSHA.MatchString(conflict.Current) {
		return fmt.Errorf("landing ref conflict returned invalid current commit %q", conflict.Current)
	}
	tree, err := treeSHAForCommit(task.RepoRoot, conflict.Current)
	if err != nil {
		return fmt.Errorf("resolve amended landing ref tree: %w", err)
	}
	ref, err := pushVerificationCommit(task.RepoRoot, verificationPush{kind: verificationPushWorker, stream: task.Stream, sha: conflict.Current})
	if err != nil {
		return fmt.Errorf("submit amended immutable commit for fresh verification: %w", err)
	}
	stream.Submission = &SchedulerSubmission{SHA: conflict.Current, TreeSHA: tree, ExpectedRefSHA: conflict.Current, Ref: ref, DeadlineAt: time.Now().UTC().Add(schedulerRemoteLimit)}
	stream.ReviewGeneration++
	stream.ReviewResults = nil
	stream.ReviewReports = nil
	stream.Cause = "tree-changed-after-verify"
	return options.store.transition(stream, SchedulerAwaitingRemote, "landing ref compare-and-swap refused: "+conflict.Error(), "wait for fresh remote verification and review")
}

func (options schedulerRunOptions) submitCandidate(task SchedulerTask, stream SchedulerStream, sha string) error {
	if !fullGitSHA.MatchString(sha) {
		return fmt.Errorf("integration candidate returned invalid immutable merge SHA %q", sha)
	}
	treeSHA, err := treeSHAForCommit(task.RepoRoot, sha)
	if err != nil {
		return err
	}
	ref, err := pushVerificationCommit(task.RepoRoot, verificationPush{kind: verificationPushCandidate, stream: task.Stream, sha: sha})
	if err != nil {
		return fmt.Errorf("push integration candidate: %w", err)
	}
	stream.Submission = &SchedulerSubmission{SHA: sha, TreeSHA: treeSHA, Ref: ref, Purpose: "candidate", DeadlineAt: time.Now().UTC().Add(schedulerRemoteLimit)}
	stream.Cause = ""
	return options.store.transition(stream, SchedulerAwaitingRemote, "candidate verification "+ref, "wait for the shared remote watcher")
}

func (options schedulerRunOptions) queueMergeResolver(task SchedulerTask, stream SchedulerStream) error {
	stream.Cause = "integration merge conflict"
	stream.WaitingFor = "merge resolver"
	stream.NextStep = "run the READY merge task"
	if err := options.store.writeStreamObserved(stream); err != nil {
		return err
	}
	mergeTask := SchedulerTask{ID: task.Stream + "-merge", Stream: task.Stream + "-merge", PromptTemplate: runnerMergeTemplate, Workdir: task.Workdir, WorktreeRoot: task.WorktreeRoot, RepoRoot: task.RepoRoot, Base: task.Base, Priority: task.Priority, DependsOn: []string{task.ID}, Vendor: task.Vendor, Effort: task.Effort, Role: schedulerMergeRole}
	if _, err := options.store.add(mergeTask); err != nil {
		return fmt.Errorf("queue merge resolver: %w", err)
	}
	return nil
}

type schedulerWorkerResult struct {
	task   SchedulerTask
	jobID  string
	err    error
	output string
}

type schedulerRunOptions struct {
	store    *schedulerStore
	root     runnerRoot
	state    string
	slots    int
	selected map[string]bool
}

func runScheduler(ctx context.Context, root runnerRoot, state string, slots int, once bool) (code int, runErr error) {
	store := newSchedulerStore(state)
	lease, err := store.acquireRunLease()
	if err != nil {
		if errors.Is(err, errSchedulerAlreadyRunning) {
			return 2, schedulerAlreadyRunningDiagnostic(root, state)
		}
		return 1, fmt.Errorf("acquire scheduler run lease: %w; next: %s sched ps --state %q", err, root.InvocationCommand(), state)
	}
	defer func() {
		runErr = errors.Join(runErr, releaseRunLease(lease))
	}()
	options := schedulerRunOptions{store: store, root: root, state: state, slots: slots}
	return options.run(ctx, once)
}

func schedulerAlreadyRunningDiagnostic(root runnerRoot, state string) error {
	command := root.InvocationCommand() + " sched run --state " + shellQuote(state)
	diagnostic, err := diag.FromCatalog("KB_RUNNER_SCHEDULER_ALREADY_RUNNING", map[string]string{"nextCommand": command}, diag.WithBinary(root.InvocationCommand()))
	if err != nil {
		return fmt.Errorf("scheduler lease diagnostic is missing from the platform catalog: %w", err)
	}
	return diagnostic
}

func (options schedulerRunOptions) run(ctx context.Context, once bool) (int, error) {
	if err := options.store.replayHandoff(); err != nil {
		return 1, err
	}
	results := make(chan schedulerWorkerResult, options.slots)
	active := 0
	var visited map[string]bool
	if once {
		visited = map[string]bool{}
	}
	watchDone := false
	ticker := time.NewTicker(schedulerRemoteWait)
	defer ticker.Stop()
	for {
		started, err := options.dispatch(ctx, results, options.slots-active, visited)
		if err != nil {
			return 1, err
		}
		active += started
		if once && active == 0 && watchDone {
			return 0, nil
		}
		if once && active == 0 && !watchDone {
			if err := options.watchRemote(ctx); err != nil {
				return 1, err
			}
			watchDone = true
			continue
		}
		if !once && active == 0 && !watchDone {
			if err := options.watchRemote(ctx); err != nil {
				return 1, err
			}
			watchDone = true
			continue
		}
		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case result := <-results:
			active--
			if err := options.finishWorker(result); err != nil {
				return 1, err
			}
			if once {
				visited[result.task.ID] = true
			}
		case <-ticker.C:
			if err := options.watchRemote(ctx); err != nil {
				return 1, err
			}
			watchDone = true
		}
		if once && watchDone && active == 0 {
			// A pass may have queued a review task. The next dispatch loop starts it,
			// while the visited set prevents a rejected worker from looping here.
			if tasks, err := options.readyTasks(visited); err == nil && len(tasks) == 0 {
				return 0, nil
			} else if err != nil {
				return 1, err
			}
		}
	}
}

func (options schedulerRunOptions) dispatch(ctx context.Context, results chan<- schedulerWorkerResult, capacity int, visited map[string]bool) (int, error) {
	if capacity <= 0 {
		return 0, nil
	}
	tasks, err := options.readyTasks(visited)
	if err != nil {
		return 0, err
	}
	started := 0
	for _, task := range tasks {
		if len(options.selected) > 0 && !options.selected[task.ID] {
			continue
		}
		if started >= capacity {
			break
		}
		stream, err := options.store.stream(task.Stream)
		if err != nil {
			return started, err
		}
		if err := options.ensureWorktree(task); err != nil {
			stream.Cause = err.Error()
			transitionErr := options.store.transition(stream, SchedulerFailed, "worktree setup", "inspect the task's worktree and base branch")
			return started, errors.Join(fmt.Errorf("scheduler worktree for %s: %w", task.Stream, err), transitionErr)
		}
		if stream.ThreadID != "" && task.Role == schedulerWorkerRole {
			if _, _, err := snapshotWorktree(task.Workdir, task.Stream); err != nil {
				return started, fmt.Errorf("snapshot before resuming %s: %w", task.Stream, err)
			}
		}
		if task.Role == schedulerReviewRole || task.Role == schedulerMergeRole {
			if _, err := options.renderReviewPrompt(task); err != nil {
				stream.Cause = err.Error()
				transitionErr := options.store.transition(stream, SchedulerFailed, "review prompt", "inspect the persisted template path")
				return started, errors.Join(err, transitionErr)
			}
		}
		stream, claimed, err := options.store.claimReady(task.Stream, task.ID)
		if err != nil {
			return started, err
		}
		if !claimed {
			continue
		}
		jobID, err := schedulerJobID(task.Stream)
		if err != nil {
			return started, err
		}
		started++
		go func(task SchedulerTask, jobID, threadID string) {
			var output []byte
			var err error
			if task.Role == schedulerReviewRole && task.Vendor == "gemini" {
				output, err = options.runGemReview(ctx, task)
			} else {
				if task.Role == schedulerWorkerRole {
					task.Prompt, err = prepareRoundPrompt(task, options.state)
					if err != nil {
						results <- schedulerWorkerResult{task: task, jobID: jobID, err: err}
						return
					}
				}
				command := exec.CommandContext(ctx, options.root.Invocation(), schedulerAgentArgs(task, options.state, jobID, options.slots, threadID)...)
				command.Dir = task.RepoRoot
				output, err = command.CombinedOutput()
			}
			results <- schedulerWorkerResult{task: task, jobID: jobID, err: err, output: string(output)}
		}(task, jobID, stream.ThreadID)
		if visited != nil {
			visited[task.ID] = true
		}
	}
	return started, nil
}

func (options schedulerRunOptions) runGemReview(ctx context.Context, task SchedulerTask) ([]byte, error) {
	if len(task.DependsOn) != 1 {
		return nil, fmt.Errorf("Gemini review task %s must depend on one worker", task.ID)
	}
	worker, err := options.store.task(task.DependsOn[0])
	if err != nil {
		return nil, err
	}
	root := task.WorktreeRoot
	for _, name := range []string{"gem-review.sh", "review-flags.sh", "limits.py", "00-rules.md"} {
		if err := requireRegularFile(filepath.Join(root, "prompts", name), "Gemini review owner"); err != nil {
			return nil, err
		}
	}
	prompt := filepath.Join(root, "prompts", worker.Stream+".md")
	if err := writeAtomicFile(prompt, []byte("Review stream "+worker.Stream+". Write verdict to "+filepath.Base(privateReviewReportPath(task))+".\n")); err != nil {
		return nil, err
	}
	report := filepath.Join(task.Workdir, ".agent-report.md")
	_, reportBody, err := readReport(report)
	if err != nil {
		return nil, fmt.Errorf("read worker report for Gemini review: %w", err)
	}
	if err := writeAtomicFile(filepath.Join(root, "reports", worker.Stream+".md"), reportBody); err != nil {
		return nil, fmt.Errorf("stage worker report for Gemini review: %w", err)
	}
	stdoutPath := filepath.Join(options.state, "jobs", task.ID+".stdout.log")
	stderrPath := filepath.Join(options.state, "jobs", task.ID+".stderr.log")
	if err := os.MkdirAll(filepath.Dir(stdoutPath), 0o700); err != nil {
		return nil, fmt.Errorf("create Gemini review logs directory: %w", err)
	}
	command := exec.CommandContext(ctx, options.root.Invocation(), "run", "--tag", "gem-review-"+task.ID, "--state", options.state, "--stall", "30m", "--stdout", stdoutPath, "--stderr", stderrPath, "--slot-group", "agent-scheduler", "--slots", fmt.Sprint(options.slots), "--env", "KBLABS_WORKTREES_ROOT="+root, "--", "sh", filepath.Join(root, "prompts", "gem-review.sh"), worker.Stream)
	command.Dir = root
	command.Env = os.Environ()
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("Gemini review helper failed: %s: %s", exitSummary(err), strings.TrimSpace(string(output)))
	}
	data, err := os.ReadFile(filepath.Join(root, "reports", worker.Stream+".gem-review.md"))
	if err != nil {
		return output, fmt.Errorf("read Gemini review result: %w", err)
	}
	line := ""
	for _, candidate := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(candidate, "VERDICT: ") {
			line = candidate
			break
		}
	}
	verdict := "REJECT"
	if strings.Contains(line, "VERDICT: ACCEPT") {
		verdict = "ACCEPT"
	} else if !strings.Contains(line, "VERDICT: REWORK") {
		if strings.Contains(line, "VERDICT: NONE") {
			return output, errGeminiCooldown
		}
		return output, fmt.Errorf("Gemini review helper returned no ACCEPT or REWORK verdict")
	}
	if err := writeAtomicFile(privateReviewReportPath(task), []byte(verdict+"\n\n"+string(data))); err != nil {
		return output, fmt.Errorf("persist Gemini review verdict: %w", err)
	}
	return output, nil
}

func schedulerAgentArgs(task SchedulerTask, state, jobID string, slots int, threadID string) []string {
	prompt := task.Prompt
	if task.Role == schedulerReviewRole || task.Role == schedulerMergeRole {
		prompt = filepath.Join(state, "sched", "prompts", task.ID+".md")
	}
	args := []string{"agent", "start", "--state", state, "--stream", task.Stream, "--vendor", task.Vendor, "--effort", task.Effort, "--workdir", task.Workdir, "--prompt-file", prompt, "--slot-group", "agent-scheduler", "--slots", fmt.Sprint(slots), "--job-id", jobID}
	if task.Vendor == "zcode" && (task.Role == schedulerWorkerRole || task.Role == schedulerReviewRole) {
		message := schedulerMessagePath(task)
		if task.Role == schedulerReviewRole {
			message = privateReviewReportPath(task)
		}
		args = append(args, "--message", message)
	}
	if threadID != "" {
		args = append(args, "--session", threadID)
	}
	return args
}

func (options schedulerRunOptions) readyTasks(visited map[string]bool) ([]SchedulerTask, error) {
	tasks, err := options.store.tasks()
	if err != nil {
		return nil, err
	}
	ready := make([]SchedulerTask, 0, len(tasks))
	for _, task := range tasks {
		if visited[task.ID] {
			continue
		}
		stream, err := options.store.stream(task.Stream)
		if err != nil {
			return nil, err
		}
		if stream.State != SchedulerReady {
			continue
		}
		blocked, reason, err := options.dependencyWait(task)
		if err != nil {
			return nil, err
		}
		if blocked {
			stream.WaitingFor = reason
			stream.NextStep = "wait for dependency completion"
			if err := options.store.writeStreamObserved(stream); err != nil {
				return nil, err
			}
			continue
		}
		ready = append(ready, task)
	}
	return ready, nil
}

func (options schedulerRunOptions) dependencyWait(task SchedulerTask) (bool, string, error) {
	for _, dependencyID := range task.DependsOn {
		dependency, err := options.store.task(dependencyID)
		if err != nil {
			return false, "", fmt.Errorf("read dependency task %s for %s: %w", dependencyID, task.ID, err)
		}
		stream, err := options.store.stream(dependency.Stream)
		if err != nil {
			return false, "", err
		}
		if task.Role == schedulerReviewRole || task.Role == schedulerMergeRole {
			if stream.State == SchedulerAwaitingReview {
				continue
			}
			return true, dependency.Stream + " (" + string(stream.State) + ")", nil
		}
		if stream.State != SchedulerDone {
			return true, dependency.Stream + " (" + string(stream.State) + ")", nil
		}
	}
	return false, "", nil
}

func (s *schedulerStore) writeStreamObserved(stream SchedulerStream) error {
	return s.withLock(func() error {
		stream.ChangedAt = time.Now().UTC()
		if err := s.writeJSON(s.streamPath(stream.Stream), stream); err != nil {
			return err
		}
		return s.appendEvent(map[string]any{"kind": "scheduler.waiting", "stream": stream.Stream, "waiting_for": stream.WaitingFor, "next_step": stream.NextStep, "time": stream.ChangedAt})
	})
}

func (options schedulerRunOptions) ensureWorktree(task SchedulerTask) error {
	return ensureGitWorktree(task.RepoRoot, task.Workdir, task.Stream, task.Base)
}

func (options schedulerRunOptions) finishWorker(result schedulerWorkerResult) error {
	stream, err := options.store.stream(result.task.Stream)
	if err != nil {
		return err
	}
	if result.err != nil {
		if result.task.Role == schedulerReviewRole && result.task.Vendor == "gemini" && errors.Is(result.err, errGeminiCooldown) {
			return options.queueCodexFallback(result.task)
		}
		if result.task.Role == schedulerReviewRole && result.task.Vendor == "zcode" {
			return options.skipUnavailableZCode(result.task, result.err)
		}
		stream.Cause = fmt.Sprintf("agent command failed: %s", strings.TrimSpace(result.err.Error()))
		if err := options.store.transition(stream, SchedulerFailed, "agent process", "inspect the stream report and runner job record"); err != nil {
			return errors.Join(fmt.Errorf("%s", stream.Cause), err)
		}
		return nil
	}
	if result.task.Role == schedulerReviewRole && result.task.Vendor == "gemini" {
		return options.finishReview(result.task, stream, "")
	}
	job, err := newJobStore(options.state).Get(result.jobID)
	if err != nil {
		return fmt.Errorf("agent %s completed but its durable job record is unavailable: %w; output=%s", result.task.Stream, err, strings.TrimSpace(result.output))
	}
	stream.ThreadID = job.SessionID
	if result.task.Role == schedulerReviewRole {
		return options.finishReview(result.task, stream, job.MessagePath)
	}
	if result.task.Role == schedulerMergeRole {
		return options.finishMerge(result.task, stream)
	}
	reportPath := filepath.Join(result.task.Workdir, ".agent-report.md")
	if result.task.Vendor == "zcode" {
		reportPath = schedulerMessagePath(result.task)
	}
	report, reportBody, err := readReport(reportPath)
	if err != nil {
		stream.Cause = "plan-question"
		return options.store.transition(stream, SchedulerAwaitingOrchestrator, "worker report", "inspect .agent-report.md in the persisted workdir")
	}
	if report == "Status: PARTIAL" || strings.Contains(string(reportBody), "PLAN-QUESTION") {
		stream.Cause = "plan-question"
		return options.store.transition(stream, SchedulerAwaitingOrchestrator, "orchestrator decision", "resolve the worker report before resuming")
	}
	if report != "Status: DONE" {
		stream.Cause = "plan-question"
		return options.store.transition(stream, SchedulerAwaitingOrchestrator, "worker report", "correct .agent-report.md or decide the stream outcome")
	}
	submission, err := options.submit(result.task, stream)
	if err != nil {
		stream.Cause = "outage-after-retries"
		return options.store.transition(stream, SchedulerAwaitingOrchestrator, "remote submission", "inspect backup remote and retry after correcting the cause")
	}
	stream.Submission = &submission
	stream.Cause = ""
	return options.store.transition(stream, SchedulerAwaitingRemote, "GitHub Actions "+submission.Ref, "wait for the shared remote watcher")
}

func (options schedulerRunOptions) queueCodexFallback(task SchedulerTask) error {
	if len(task.DependsOn) != 1 {
		return fmt.Errorf("Gemini review task %s must depend on one worker", task.ID)
	}
	reviewState, err := options.store.stream(task.Stream)
	if err != nil {
		return err
	}
	reviewState.Cause = "Gemini reviewers are cooling down"
	reviewState.WaitingFor = reviewState.Cause
	if err := options.store.transition(reviewState, SchedulerDone, reviewState.WaitingFor, "queue independent Codex fallback reviewer"); err != nil {
		return err
	}
	worker, err := options.store.task(task.DependsOn[0])
	if err != nil {
		return err
	}
	stream, err := options.store.stream(worker.Stream)
	if err != nil {
		return err
	}
	if stream.ReviewResults == nil {
		stream.ReviewResults = map[string]string{}
	}
	stream.ReviewResults[task.ReviewerKey] = "SKIP"
	queued, err := options.queueSkippedReviewFallback(worker, stream, task.ReviewerKey, task.Vendor)
	if err != nil {
		return err
	}
	if !queued {
		mode, reviewers, err := options.reviewPolicy(worker, stream)
		if err != nil {
			return err
		}
		if mode == "cross" && len(stream.ReviewResults) >= len(reviewers) {
			return options.resolveReviewOutcome(worker, stream)
		}
	}
	return nil
}

func (options schedulerRunOptions) queueSkippedReviewFallback(worker SchedulerTask, stream SchedulerStream, reviewerKey, skippedVendor string) (bool, error) {
	if err := options.store.writeStreamObserved(stream); err != nil {
		return false, err
	}
	tasks, err := options.store.tasks()
	if err != nil {
		return false, err
	}
	for _, slot := range schedulerReviewerSlots(worker) {
		if slot.Vendor == skippedVendor {
			continue
		}
		fallbackID := worker.Stream + "-review-fallback-" + slot.Vendor + "-" + reviewerKey
		alreadyQueued := false
		for _, existing := range tasks {
			if existing.Role != schedulerReviewRole || existing.ReviewerKey != reviewerKey || existing.Vendor != slot.Vendor || len(existing.DependsOn) != 1 || existing.DependsOn[0] != worker.ID {
				continue
			}
			state, err := options.store.stream(existing.Stream)
			if err != nil {
				return false, err
			}
			if state.State == SchedulerReady || state.State == SchedulerRunning {
				return true, nil
			}
			alreadyQueued = true
		}
		if alreadyQueued {
			continue
		}
		fallback := SchedulerTask{ID: fallbackID, Stream: fallbackID, PromptTemplate: runnerReviewTemplate, Workdir: worker.Workdir, WorktreeRoot: worker.WorktreeRoot, RepoRoot: worker.RepoRoot, Base: worker.Base, Priority: worker.Priority, DependsOn: []string{worker.ID}, Vendor: slot.Vendor, Effort: worker.Effort, Role: schedulerReviewRole, ReviewerKey: reviewerKey, ReviewerFocus: "independent adversarial review: find a concrete failure scenario in the changed behavior"}
		if _, err := options.store.add(fallback); err != nil {
			return false, fmt.Errorf("queue independent %s reviewer for %s: %w", slot.Vendor, reviewerKey, err)
		}
		return true, nil
	}
	return false, nil
}

func (options schedulerRunOptions) skipUnavailableZCode(task SchedulerTask, cause error) error {
	reviewStream, err := options.store.stream(task.Stream)
	if err != nil {
		return err
	}
	reviewStream.Cause = "ZCode unavailable: " + cause.Error()
	reviewStream.WaitingFor = reviewStream.Cause
	if err := options.store.transition(reviewStream, SchedulerDone, reviewStream.WaitingFor, "skip unavailable ZCode reviewer"); err != nil {
		return err
	}
	if len(task.DependsOn) != 1 {
		return fmt.Errorf("ZCode review task %s must depend on one worker", task.ID)
	}
	worker, err := options.store.task(task.DependsOn[0])
	if err != nil {
		return err
	}
	stream, err := options.store.stream(worker.Stream)
	if err != nil {
		return err
	}
	if stream.ReviewResults == nil {
		stream.ReviewResults = map[string]string{}
	}
	stream.ReviewResults[task.ReviewerKey] = "SKIP"
	worker, err = options.store.task(stream.TaskID)
	if err != nil {
		return err
	}
	mode, reviewers, err := options.reviewPolicy(worker, stream)
	if err != nil {
		return err
	}
	if mode == "cross" && len(stream.ReviewResults) < len(reviewers) {
		return options.store.writeStreamObserved(stream)
	}
	return options.resolveReviewOutcome(worker, stream)
}

func readReport(path string) (string, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	return readReportBytes(data)
}

func readReportBytes(data []byte) (string, []byte, error) {
	line, _, _ := strings.Cut(string(data), "\n")
	first := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	return first, data, nil
}

func (options schedulerRunOptions) finishReview(task SchedulerTask, reviewStream SchedulerStream, lastMessage string) error {
	reportPath := privateReviewReportPath(task)
	verdict, foundPath, err := resolveReviewVerdict([]string{reportPath, lastMessage})
	if err != nil {
		reviewStream.Cause = "review-missing"
		return options.store.transition(reviewStream, SchedulerAwaitingOrchestrator, "private review verdict", "inspect this review task's private report or runner last-message")
	}
	reportPath = foundPath
	if len(task.DependsOn) != 1 {
		return fmt.Errorf("review task %s must depend on exactly one worker task", task.ID)
	}
	workerTask, err := options.store.task(task.DependsOn[0])
	if err != nil {
		return err
	}
	workerStream, err := options.store.stream(workerTask.Stream)
	if err != nil {
		return err
	}
	if err := options.store.transition(reviewStream, SchedulerDone, "", "review verdict recorded"); err != nil {
		return err
	}
	if workerStream.ReviewResults == nil {
		workerStream.ReviewResults = map[string]string{}
	}
	key := task.ReviewerKey
	if key == "" {
		key = task.Vendor
	}
	workerStream.ReviewResults[key] = verdict
	workerStream.ReviewReports = append(workerStream.ReviewReports, reportPath)
	workerTask, err = options.store.task(workerStream.TaskID)
	if err != nil {
		return err
	}
	mode, reviewers, err := options.reviewPolicy(workerTask, workerStream)
	if err != nil {
		return err
	}
	if mode == "cross" && len(workerStream.ReviewResults) < len(reviewers) {
		return options.store.writeStreamObserved(workerStream)
	}
	return options.resolveReviewOutcome(workerTask, workerStream)
}

func (options schedulerRunOptions) resolveReviewOutcome(workerTask SchedulerTask, workerStream SchedulerStream) error {
	gate := schedulerReviewGate(workerTask, workerStream)
	if len(gate.Accepted) > 0 && len(gate.Rejected) > 0 {
		workerStream.Cause = "review-disagreement"
		return options.store.transition(workerStream, SchedulerAwaitingOrchestrator, strings.Join(workerStream.ReviewReports, ","), "decide the review disagreement")
	}
	if len(gate.Skipped) > 0 {
		for _, slot := range gate.Skipped {
			queued, err := options.queueSkippedReviewFallback(workerTask, workerStream, slot.Key, slot.Vendor)
			if err != nil {
				return err
			}
			if queued {
				return nil
			}
		}
		workerStream.Cause = "review-unavailable"
		return options.store.transition(workerStream, SchedulerAwaitingOrchestrator, gate.refusal(), "choose an available configured reviewer")
	}
	if len(gate.Missing) > 0 {
		workerStream.Cause = "review-missing"
		return options.store.transition(workerStream, SchedulerAwaitingOrchestrator, strings.Join(gate.Missing, ", "), "inspect missing required review verdicts")
	}
	if gate.allowsLand() {
		_, _, err := options.landAccepted(workerTask, workerStream)
		return err
	}
	if len(gate.Rejected) > 0 {
		workerStream.RejectRounds++
		if workerStream.RejectRounds >= 3 {
			workerStream.Cause = "third-reject"
			return options.store.transition(workerStream, SchedulerAwaitingOrchestrator, strings.Join(workerStream.ReviewReports, ","), "decide after three rejected review rounds")
		}
	}
	var reports []string
	var artifacts []reviewReportArtifact
	for _, reportPath := range workerStream.ReviewReports {
		report, err := os.ReadFile(reportPath)
		if err != nil {
			return fmt.Errorf("read review report %s for archive: %w", reportPath, err)
		}
		artifacts = append(artifacts, reviewReportArtifact{Path: reportPath, Contents: report})
	}
	reports, err := archiveReviewReports(workerTask.Workdir, workerTask.Stream, artifacts)
	if err != nil {
		return err
	}
	prompt := filepath.Join(options.state, "sched", "prompts", workerTask.ID+"-resume.md")
	promptBody := fmt.Sprintf("The review rejected stream %s. Read the archived reviews at %s, address their findings, and update .agent-report.md.\n", workerTask.Stream, strings.Join(reports, ", "))
	if err := writeAtomicFile(prompt, []byte(promptBody)); err != nil {
		return fmt.Errorf("write review resume prompt: %w", err)
	}
	workerTask.Prompt = prompt
	if err := options.store.updateTask(workerTask); err != nil {
		return err
	}
	workerStream.Cause = "review rejected; resume from " + strings.Join(reports, ", ")
	workerStream.ReviewResults = nil
	workerStream.ReviewReports = nil
	return options.store.transition(workerStream, SchedulerReady, "", "resume the original agent thread")
}

type reviewReportArtifact struct {
	Path     string
	Contents []byte
}

func archiveReviewReports(workdir, stream string, reports []reviewReportArtifact) ([]string, error) {
	if len(reports) == 0 {
		return nil, nil
	}
	archive := filepath.Join(workdir, "reviews", "archive", stream+"-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".md")
	if err := os.MkdirAll(filepath.Dir(archive), 0o700); err != nil {
		return nil, fmt.Errorf("create review archive: %w", err)
	}
	var paths []string
	for _, report := range reports {
		archived := strings.TrimSuffix(archive, ".md") + "-" + filepath.Base(report.Path)
		if err := writeAtomicFile(archived, report.Contents); err != nil {
			return nil, fmt.Errorf("archive report %s: %w", report.Path, err)
		}
		paths = append(paths, archived)
	}
	return paths, nil
}

func (options schedulerRunOptions) finishMerge(task SchedulerTask, mergeStream SchedulerStream) error {
	status, _, err := readReport(filepath.Join(task.Workdir, ".agent-report.md"))
	if err != nil || status != "Status: DONE" {
		mergeStream.Cause = "plan-question"
		return options.store.transition(mergeStream, SchedulerAwaitingOrchestrator, "merge resolver report", "inspect .agent-report.md in the merge worktree")
	}
	if len(task.DependsOn) != 1 {
		return fmt.Errorf("merge task %s must depend on exactly one worker task", task.ID)
	}
	worker, err := options.store.task(task.DependsOn[0])
	if err != nil {
		return err
	}
	workerState, err := options.store.stream(worker.Stream)
	if err != nil {
		return err
	}
	if _, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "add", "-A"); err != nil {
		return fmt.Errorf("stage merge resolver files: %w", err)
	}
	workerHead, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("capture worker commit before merge resolution: %w", err)
	}
	workerHead = strings.TrimSpace(workerHead)
	integrationSHA, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "rev-parse", "--verify", "MERGE_HEAD")
	if err != nil {
		return fmt.Errorf("capture integration commit before merge resolution: %w", err)
	}
	integrationSHA = strings.TrimSpace(integrationSHA)
	if !fullGitSHA.MatchString(workerHead) || !fullGitSHA.MatchString(integrationSHA) {
		return fmt.Errorf("merge resolver requires full worker and integration parent SHAs")
	}
	nextCommand := fmt.Sprintf("%s sched run --state %q", options.root.InvocationCommand(), options.state)
	if err := excludeLandingReportsFromIndex(task.Workdir, workerHead, nextCommand); err != nil {
		return fmt.Errorf("exclude merge-resolution report files: %w", err)
	}
	resolvedTree, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "write-tree")
	if err != nil {
		return fmt.Errorf("write merge-resolution tree: %w", err)
	}
	resolvedTree = strings.TrimSpace(resolvedTree)
	if !fullGitSHA.MatchString(resolvedTree) {
		return fmt.Errorf("merge resolver returned invalid tree SHA %q", resolvedTree)
	}
	resolvedSHA, err := createCommitObject(task.RepoRoot, resolvedTree, []string{workerHead, integrationSHA}, "merge: resolve integration conflicts")
	if err != nil {
		return fmt.Errorf("create merge-resolution commit object: %w", err)
	}
	if err := compareAndSwapLandingRef(task.RepoRoot, "refs/heads/agent/"+worker.Stream, resolvedSHA, workerHead); err != nil {
		var conflict *landingRefConflict
		if errors.As(err, &conflict) {
			return conflict
		}
		return fmt.Errorf("publish merge-resolution commit with worker-ref compare-and-swap: %w", err)
	}
	if _, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "reset", "--mixed", resolvedSHA); err != nil {
		return fmt.Errorf("refresh merge-resolution worktree before report snapshot: %w", err)
	}
	verifiedSHA, _, err := snapshotWorktree(task.Workdir, worker.Stream)
	if err != nil {
		return fmt.Errorf("snapshot merge-resolution worktree: %w", err)
	}
	submission, err := options.submitWorkerCommit(worker, verifiedSHA, resolvedSHA)
	if err != nil {
		return fmt.Errorf("submit immutable merge-resolution commit for remote verification: %w", err)
	}
	workerState.Submission = &submission
	workerState.ReviewResults = nil
	workerState.ReviewReports = nil
	workerState.ReviewGeneration++
	workerState.Cause = ""
	if err := options.store.transition(workerState, SchedulerAwaitingRemote, "merge-resolution verification", "wait for remote verification of the resolved worker commit"); err != nil {
		return err
	}
	mergeStream.Cause = ""
	return options.store.transition(mergeStream, SchedulerDone, "", "resolved commit submitted for worker verification")
}

func schedulerJobID(stream string) (string, error) {
	var random [6]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create scheduler job ID: %w", err)
	}
	return stream + "-" + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(random[:]), nil
}

func (options schedulerRunOptions) submit(task SchedulerTask, stream SchedulerStream) (SchedulerSubmission, error) {
	if stream.Submission != nil && stream.Submission.RunID != "" {
		repo, err := options.githubRepo(task.RepoRoot)
		if err != nil {
			return SchedulerSubmission{}, err
		}
		if _, err := schedulerCommandOutput("", "gh", "run", "cancel", stream.Submission.RunID, "--repo", repo); err != nil {
			return SchedulerSubmission{}, fmt.Errorf("cancel superseded verification run %s: %w", stream.Submission.RunID, err)
		}
	}
	branchRef := "refs/heads/agent/" + task.Stream
	expectedRefSHA, err := schedulerCommandOutput(task.RepoRoot, "git", "-C", task.RepoRoot, "rev-parse", "--verify", branchRef)
	if err != nil {
		return SchedulerSubmission{}, fmt.Errorf("resolve worker branch before verification: %w", err)
	}
	headSHA, err := schedulerCommandOutput(task.Workdir, "git", "-C", task.Workdir, "rev-parse", "HEAD")
	if err != nil {
		return SchedulerSubmission{}, fmt.Errorf("resolve worker HEAD before verification: %w", err)
	}
	if !fullGitSHA.MatchString(expectedRefSHA) || headSHA != expectedRefSHA {
		return SchedulerSubmission{}, fmt.Errorf("worker HEAD %s does not match expected branch %s at %s", headSHA, branchRef, expectedRefSHA)
	}
	sha, _, err := snapshotWorktree(task.Workdir, task.Stream)
	if err != nil {
		return SchedulerSubmission{}, fmt.Errorf("snapshot worktree: %w", err)
	}
	if !fullGitSHA.MatchString(sha) {
		return SchedulerSubmission{}, fmt.Errorf("snapshot returned invalid commit SHA %q", sha)
	}
	return options.submitWorkerCommit(task, sha, expectedRefSHA)
}

func (options schedulerRunOptions) submitWorkerCommit(task SchedulerTask, sha, expectedRefSHA string) (SchedulerSubmission, error) {
	if !fullGitSHA.MatchString(sha) || !fullGitSHA.MatchString(expectedRefSHA) {
		return SchedulerSubmission{}, fmt.Errorf("worker verification requires full commit and expected branch SHAs")
	}
	treeSHA, err := treeSHAForCommit(task.RepoRoot, sha)
	if err != nil {
		return SchedulerSubmission{}, err
	}
	ref, err := pushVerificationCommit(task.RepoRoot, verificationPush{kind: verificationPushWorker, stream: task.Stream, sha: sha})
	if err != nil {
		return SchedulerSubmission{}, err
	}
	return SchedulerSubmission{SHA: sha, TreeSHA: treeSHA, ExpectedRefSHA: expectedRefSHA, Ref: ref, DeadlineAt: time.Now().UTC().Add(schedulerRemoteLimit)}, nil
}

var fullGitSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (options schedulerRunOptions) githubRepo(repoRoot string) (string, error) {
	remote, err := schedulerCommandOutput(repoRoot, "git", "-C", repoRoot, "remote", "get-url", "backup")
	if err != nil {
		return "", fmt.Errorf("read backup remote: %w", err)
	}
	core, err := schedulerFileURL(filepath.Join(repoRoot, "scripts", "ci", "remote-verdict.mjs"))
	if err != nil {
		return "", fmt.Errorf("resolve remote verdict module: %w", err)
	}
	code := "const { repositoryFromRemote } = await import(process.argv[1]); process.stdout.write(repositoryFromRemote(process.argv[2]));"
	return schedulerCommandOutput(repoRoot, "node", "--input-type=module", "-e", code, core, remote)
}

func schedulerCommandOutput(dir, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	if dir != "" {
		command.Dir = dir
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func (options schedulerRunOptions) watchRemote(ctx context.Context) error {
	streams, err := options.store.streams()
	if err != nil {
		return err
	}
	var pending []SchedulerStream
	for _, stream := range streams {
		if stream.State == SchedulerAwaitingRemote && stream.Submission != nil {
			pending = append(pending, stream)
		}
	}
	integrationState, err := options.store.integration()
	if err != nil {
		return err
	}
	if len(pending) == 0 && integrationState.Bisect == nil {
		return nil
	}
	if len(pending) == 0 {
		return errors.New("active candidate bisect has no awaiting-remote candidate stream")
	}
	repoRoot, err := options.store.repositoryRoot()
	if err != nil {
		return err
	}
	repo, err := options.githubRepo(repoRoot)
	if err != nil {
		return options.remoteWatchFailure(pending, err)
	}
	listJSON, err := schedulerCommandOutput("", "gh", "run", "list", "--repo", repo, "--json", "databaseId,headBranch,headSha,status,conclusion,event,path", "--limit", "1000")
	if err != nil {
		return options.remoteWatchFailure(pending, fmt.Errorf("list remote runs: %w", err))
	}
	var runs []schedulerGitHubRun
	if err := json.Unmarshal([]byte(listJSON), &runs); err != nil {
		return fmt.Errorf("decode `gh run list` output: %w", err)
	}
	for _, stream := range pending {
		task, err := options.store.task(stream.TaskID)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if integrationState.Bisect != nil && integrationState.Bisect.CandidateTaskID == stream.TaskID && integrationState.Bisect.CandidateSHA == stream.Submission.SHA {
			continue
		}
		if time.Now().UTC().After(stream.Submission.DeadlineAt) {
			if err := options.writeTerminalVerdict(stream, task, "timeout", "remote verification deadline expired"); err != nil {
				return err
			}
			stream.Cause = "outage-after-retries"
			if err := options.store.transition(stream, SchedulerAwaitingOrchestrator, "remote deadline", "inspect the persisted timeout verdict and decide next action"); err != nil {
				return err
			}
			continue
		}
		run, found := selectSchedulerRun(runs, *stream.Submission)
		if !found {
			continue
		}
		previousStatus, previousConclusion := stream.Submission.Status, stream.Submission.Conclusion
		stream.Submission.RunID = fmt.Sprint(run.DatabaseID)
		stream.Submission.Status, stream.Submission.Conclusion = run.Status, run.Conclusion
		if previousStatus == run.Status && previousConclusion == run.Conclusion {
			if err := options.store.writeStreamObserved(stream); err != nil {
				return err
			}
			continue
		}
		if run.Status != "completed" {
			if err := options.store.writeStreamObserved(stream); err != nil {
				return err
			}
			continue
		}
		if _, err := schedulerCommandOutput("", "gh", "run", "view", fmt.Sprint(run.DatabaseID), "--repo", repo, "--json", "status,conclusion,jobs"); err != nil {
			if err := options.handleOutage(stream, task, err); err != nil {
				return err
			}
			continue
		}
		verdict, err := options.downloadAndValidateVerdict(repo, run, stream.Submission.SHA, repoRoot)
		if err != nil {
			if err := options.handleOutage(stream, task, err); err != nil {
				return err
			}
			continue
		}
		verdictStatus, _ := verdict["status"].(string)
		if run.Conclusion != "success" && verdictStatus == "pass" {
			verdictStatus = "fail"
		}
		if verdictStatus == "outage" {
			if err := options.handleOutage(stream, task, errors.New(verdictCause(verdict))); err != nil {
				return err
			}
			continue
		}
		if err := options.persistVerdict(stream, task, verdict); err != nil {
			return err
		}
		switch verdictStatus {
		case "pass":
			if stream.Submission.Purpose == "candidate" {
				if err := options.store.markIntegrationGreen(stream.Submission.SHA); err != nil {
					return err
				}
				stream.Cause = ""
				if err := options.store.transition(stream, SchedulerDone, "", "integration candidate passed; dependencies may dispatch"); err != nil {
					return err
				}
			} else if err := options.queueReview(task, stream); err != nil {
				return err
			}
		case "fail":
			if stream.Submission.Purpose == "candidate" {
				if err := options.startCandidateBisect(task, stream, verdict, repo); err != nil {
					return err
				}
			} else if err := options.resumeAfterRemoteFailure(task, stream, verdict); err != nil {
				return err
			}
		case "timeout":
			stream.Cause = "outage-after-retries"
			if err := options.store.transition(stream, SchedulerAwaitingOrchestrator, "remote timeout", "inspect the persisted timeout verdict"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("remote verifier returned unsupported status %q", verdictStatus)
		}
	}
	return options.watchCandidateBisect(ctx, runs, repo)
}

func (options schedulerRunOptions) startCandidateBisect(task SchedulerTask, stream SchedulerStream, verdict map[string]any, repo string) error {
	state, err := options.store.integration()
	if err != nil {
		return err
	}
	if state.Bisect != nil {
		if state.Bisect.CandidateSHA == stream.Submission.SHA {
			return nil
		}
		return errors.New("another candidate bisect is already active")
	}
	start := 0
	if state.LastGreenSHA != "" {
		for start < len(state.Merges) && state.Merges[start].ParentSHA != state.LastGreenSHA {
			start++
		}
	}
	if start >= len(state.Merges) {
		return fmt.Errorf("candidate %s has no recorded integration prefixes since green SHA %s", stream.Submission.SHA, state.LastGreenSHA)
	}
	var failures []string
	if jobs, ok := verdict["jobs"].([]any); ok {
		for _, item := range jobs {
			job, ok := item.(map[string]any)
			if !ok {
				continue
			}
			values, _ := job["failures"].([]any)
			for _, value := range values {
				if name, ok := value.(string); ok && name != "" {
					failures = append(failures, name)
				}
			}
		}
	}
	if len(failures) == 0 {
		return errors.New("candidate verdict failed without named failing tests")
	}
	bisect := &SchedulerBisect{CandidateStream: stream.Stream, CandidateTaskID: task.ID, CandidateSHA: stream.Submission.SHA, CandidateFails: failures}
	for _, merge := range state.Merges[start:] {
		ref, _, err := (verificationPush{kind: verificationPushBisect, stream: merge.Stream, sha: merge.CommitSHA}).ref()
		if err != nil {
			return err
		}
		prefix := SchedulerBisectPrefix{MergeSHA: merge.CommitSHA, Stream: merge.Stream, SHA: merge.CommitSHA, Ref: ref, DeadlineAt: time.Now().UTC().Add(schedulerRemoteLimit)}
		bisect.Prefixes = append(bisect.Prefixes, prefix)
	}
	state.Bisect = bisect
	if err := options.store.writeIntegration(state); err != nil {
		return err
	}
	for index := range state.Bisect.Prefixes {
		prefix := &state.Bisect.Prefixes[index]
		if _, err := pushVerificationCommit(task.RepoRoot, verificationPush{kind: verificationPushBisect, stream: prefix.Stream, sha: prefix.SHA}); err != nil {
			return fmt.Errorf("submit integration prefix %s: %w", prefix.Ref, err)
		}
	}
	return options.store.appendEvent(map[string]any{"kind": "scheduler.bisect.submitted", "stream": stream.Stream, "task_id": task.ID, "candidate_sha": bisect.CandidateSHA, "prefixes": bisect.Prefixes, "time": time.Now().UTC()})
}

func (options schedulerRunOptions) watchCandidateBisect(ctx context.Context, runs []schedulerGitHubRun, repo string) error {
	state, err := options.store.integration()
	if err != nil || state.Bisect == nil {
		return err
	}
	for index := range state.Bisect.Prefixes {
		prefix := &state.Bisect.Prefixes[index]
		if prefix.Status == "pass" || prefix.Status == "fail" || prefix.Status == "outage" {
			continue
		}
		task, err := options.store.task(state.Bisect.CandidateTaskID)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().UTC().After(prefix.DeadlineAt) {
			prefix.Status = "outage"
			prefix.Conclusion = "deadline expired"
			if err := options.store.writeIntegration(state); err != nil {
				return err
			}
			continue
		}
		run, found := selectSchedulerRun(runs, SchedulerSubmission{SHA: prefix.SHA, Ref: prefix.Ref})
		if !found {
			if _, err := pushVerificationCommit(task.RepoRoot, verificationPush{kind: verificationPushBisect, stream: prefix.Stream, sha: prefix.SHA}); err != nil {
				return fmt.Errorf("resubmit integration prefix %s: %w", prefix.Ref, err)
			}
			continue
		}
		prefix.RunID = fmt.Sprint(run.DatabaseID)
		prefix.Conclusion = run.Conclusion
		if run.Status != "completed" {
			prefix.Status = "pending"
			if err := options.store.writeIntegration(state); err != nil {
				return err
			}
			continue
		}
		if _, err := schedulerCommandOutput("", "gh", "run", "view", prefix.RunID, "--repo", repo, "--json", "status,conclusion,jobs"); err != nil {
			if retryErr := options.retryBisectPrefix(prefix, repo, err); retryErr != nil {
				return retryErr
			}
			if err := options.store.writeIntegration(state); err != nil {
				return err
			}
			continue
		}
		verdict, err := options.downloadAndValidateVerdict(repo, run, prefix.SHA, task.RepoRoot)
		if err != nil {
			if retryErr := options.retryBisectPrefix(prefix, repo, err); retryErr != nil {
				return retryErr
			}
		} else {
			prefix.Status, _ = verdict["status"].(string)
			prefix.Failures = schedulerVerdictFailures(verdict)
			if run.Conclusion != "success" && prefix.Status == "pass" {
				prefix.Status = "fail"
			}
			if prefix.Status == "outage" {
				if retryErr := options.retryBisectPrefix(prefix, repo, errors.New(verdictCause(verdict))); retryErr != nil {
					return retryErr
				}
			} else if prefix.Status != "pass" && prefix.Status != "fail" && prefix.Status != "pending" {
				return fmt.Errorf("prefix verifier returned unsupported status %q for %s", prefix.Status, prefix.Ref)
			}
		}
		if err := options.store.writeIntegration(state); err != nil {
			return err
		}
	}
	for _, prefix := range state.Bisect.Prefixes {
		if prefix.Status == "" || prefix.Status == "pending" {
			return nil
		}
	}
	for _, prefix := range state.Bisect.Prefixes {
		if prefix.Status == "outage" {
			stream, err := options.store.stream(state.Bisect.CandidateStream)
			if err != nil {
				return err
			}
			state.LastBisect, state.Bisect = state.Bisect, nil
			if err := options.store.writeIntegration(state); err != nil {
				return err
			}
			stream.Cause = "outage-after-retries"
			return options.store.transition(stream, SchedulerAwaitingOrchestrator, "candidate prefix outage", "kb-runner sched ps --json")
		}
	}
	var guilty *SchedulerBisectPrefix
	for index := range state.Bisect.Prefixes {
		if state.Bisect.Prefixes[index].Status == "fail" {
			guilty = &state.Bisect.Prefixes[index]
			break
		}
	}
	if len(state.Bisect.Prefixes) == 0 {
		return errors.New("candidate has no integration prefixes to verify")
	}
	last := state.Bisect.Prefixes[len(state.Bisect.Prefixes)-1]
	task, err := options.store.task(state.Bisect.CandidateTaskID)
	if err != nil {
		return err
	}
	stream, err := options.store.stream(state.Bisect.CandidateStream)
	if err != nil {
		return err
	}
	if last.Status == "pass" {
		state.LastBisect, state.Bisect = state.Bisect, nil
		if err := options.store.writeIntegration(state); err != nil {
			return err
		}
		stream.Cause = "bisect-inconclusive"
		return options.store.transition(stream, SchedulerAwaitingOrchestrator, "candidate and final prefix disagree", "kb-runner sched ps --json")
	}
	if guilty == nil {
		return errors.New("candidate failed but no integration prefix failed")
	}
	policy, err := options.policy(task)
	if err != nil {
		return err
	}
	integration := filepath.Join(task.WorktreeRoot, strings.TrimPrefix(policy.IntegrationBranch, "agent/"))
	if _, err := schedulerCommandOutput(integration, "git", "-C", integration, "revert", "-m", "1", "--no-edit", guilty.MergeSHA); err != nil {
		return fmt.Errorf("revert guilty integration merge %s: %w", guilty.MergeSHA, err)
	}
	head, err := schedulerCommandOutput(integration, "git", "-C", integration, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve integration revert HEAD: %w", err)
	}
	failures := append([]string(nil), state.Bisect.CandidateFails...)
	state.LastBisect, state.Bisect = state.Bisect, nil
	state.LastGreenSHA = head
	state.Merges = nil
	if err := options.store.writeIntegration(state); err != nil {
		return err
	}
	stream.Cause = "candidate failures: " + strings.Join(failures, ", ")
	return options.store.transition(stream, SchedulerReady, "guilty integration merge reverted", "kb-runner sched run")
}

func (options schedulerRunOptions) retryBisectPrefix(prefix *SchedulerBisectPrefix, repo string, cause error) error {
	if prefix.Attempts >= schedulerOutageLimit || prefix.RunID == "" {
		prefix.Status = "outage"
		prefix.Conclusion = cause.Error()
		return nil
	}
	if _, err := schedulerCommandOutput("", "gh", "run", "rerun", prefix.RunID, "--repo", repo); err != nil {
		return fmt.Errorf("rerun unavailable candidate prefix %s: %w", prefix.Ref, errors.Join(cause, err))
	}
	prefix.Attempts++
	prefix.Status = "pending"
	prefix.Conclusion = cause.Error()
	return nil
}

func schedulerVerdictFailures(verdict map[string]any) []string {
	var failures []string
	if jobs, ok := verdict["jobs"].([]any); ok {
		for _, item := range jobs {
			job, ok := item.(map[string]any)
			if !ok {
				continue
			}
			values, _ := job["failures"].([]any)
			for _, value := range values {
				if name, ok := value.(string); ok && name != "" {
					failures = append(failures, name)
				}
			}
		}
	}
	return failures
}

type schedulerGitHubRun struct {
	DatabaseID   int64  `json:"databaseId"`
	HeadBranch   string `json:"headBranch"`
	HeadSHA      string `json:"headSha"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	Event        string `json:"event"`
	WorkflowPath string `json:"path"`
}

func selectSchedulerRun(runs []schedulerGitHubRun, submission SchedulerSubmission) (schedulerGitHubRun, bool) {
	for _, run := range runs {
		if run.Event == "push" && run.WorkflowPath == ".github/workflows/remote-verify.yml" && run.HeadBranch == submission.Ref && run.HeadSHA == submission.SHA {
			return run, true
		}
	}
	return schedulerGitHubRun{}, false
}

func (options schedulerRunOptions) downloadAndValidateVerdict(repo string, run schedulerGitHubRun, sha, repoRoot string) (result map[string]any, returnErr error) {
	artifacts, err := os.MkdirTemp("", "kb-scheduler-verdict-")
	if err != nil {
		return nil, fmt.Errorf("create verdict download directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(artifacts); cleanupErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove remote verdict download directory: %w", cleanupErr))
		}
	}()
	if _, err := schedulerCommandOutput("", "gh", "run", "download", fmt.Sprint(run.DatabaseID), "--repo", repo, "--name", "remote-verdict", "--dir", artifacts); err != nil {
		return nil, fmt.Errorf("download remote verdict: %w", err)
	}
	verdictPath := filepath.Join(artifacts, "verdict.json")
	module, err := schedulerFileURL(filepath.Join(repoRoot, "scripts", "ci", "remote-verdict.mjs"))
	if err != nil {
		return nil, fmt.Errorf("resolve remote verdict module: %w", err)
	}
	code := "import { readFileSync } from 'node:fs'; const { parseVerdict } = await import(process.argv[1]); const value = JSON.parse(readFileSync(process.argv[2], 'utf8')); process.stdout.write(JSON.stringify(parseVerdict(value, process.argv[3])));"
	validated, err := schedulerCommandOutput(repoRoot, "node", "--input-type=module", "-e", code, module, verdictPath, sha)
	if err != nil {
		return nil, fmt.Errorf("validate verdict against remote-verify-core: %w", err)
	}
	var verdict map[string]any
	if err := json.Unmarshal([]byte(validated), &verdict); err != nil {
		return nil, fmt.Errorf("read validated remote verdict: %w", err)
	}
	return verdict, nil
}

func verdictCause(verdict map[string]any) string {
	if diagnostics, ok := verdict["diagnostics"].([]any); ok {
		var messages []string
		for _, value := range diagnostics {
			if message, ok := value.(string); ok && message != "" {
				messages = append(messages, message)
			}
		}
		return strings.Join(messages, "\n")
	}
	return "remote verification did not pass"
}

func (options schedulerRunOptions) writeTerminalVerdict(stream SchedulerStream, task SchedulerTask, status, message string) error {
	module, err := schedulerFileURL(filepath.Join(task.RepoRoot, "scripts", "ci", "remote-verdict.mjs"))
	if err != nil {
		return fmt.Errorf("resolve remote verdict module: %w", err)
	}
	code := "const { terminalVerdict } = await import(process.argv[1]); process.stdout.write(JSON.stringify(terminalVerdict(process.argv[2], process.argv[3], process.argv[4])));"
	body, err := schedulerCommandOutput(task.RepoRoot, "node", "--input-type=module", "-e", code, module, stream.Submission.SHA, status, message)
	if err != nil {
		return fmt.Errorf("create remote terminal verdict: %w", err)
	}
	return options.writeVerdictFile(task, stream, []byte(body+"\n"))
}

func (options schedulerRunOptions) persistVerdict(stream SchedulerStream, task SchedulerTask, verdict map[string]any) error {
	data, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	return options.writeVerdictFile(task, stream, append(data, '\n'))
}

func (options schedulerRunOptions) writeVerdictFile(task SchedulerTask, stream SchedulerStream, data []byte) error {
	name := task.Stream + ".remote." + stream.Submission.SHA[:7] + ".json"
	path := filepath.Join(filepath.Dir(task.RepoRoot), "reports", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create scheduler report directory: %w", err)
	}
	if err := writeAtomicFile(path, data); err != nil {
		return fmt.Errorf("persist remote verdict %s: %w", path, err)
	}
	return nil
}

func (options schedulerRunOptions) handleOutage(stream SchedulerStream, task SchedulerTask, cause error) error {
	if stream.Submission.Attempts < schedulerOutageLimit && stream.Submission.RunID != "" {
		repo, err := options.githubRepo(task.RepoRoot)
		if err != nil {
			return err
		}
		if _, err := schedulerCommandOutput("", "gh", "run", "rerun", stream.Submission.RunID, "--repo", repo); err == nil {
			stream.Submission.Attempts++
			stream.Submission.Status = "queued"
			stream.Submission.Conclusion = ""
			stream.Cause = cause.Error()
			return options.store.writeStreamObserved(stream)
		} else {
			cause = errors.Join(cause, fmt.Errorf("request remote rerun: %w", err))
		}
	}
	stream.Cause = "outage-after-retries"
	if err := options.writeTerminalVerdict(stream, task, "outage", stream.Cause); err != nil {
		return err
	}
	return options.store.transition(stream, SchedulerAwaitingOrchestrator, "remote outage", "inspect the persisted outage verdict and decide next action")
}

func (options schedulerRunOptions) remoteWatchFailure(streams []SchedulerStream, cause error) error {
	for _, stream := range streams {
		stream.Submission.Attempts++
		stream.Cause = "outage-after-retries"
		if stream.Submission.Attempts > schedulerOutageLimit {
			task, err := options.store.task(stream.TaskID)
			if err != nil {
				return err
			}
			if err := options.writeTerminalVerdict(stream, task, "outage", stream.Cause); err != nil {
				return err
			}
			if err := options.store.transition(stream, SchedulerAwaitingOrchestrator, "remote outage", "inspect the persisted outage verdict and decide next action"); err != nil {
				return err
			}
			continue
		}
		if err := options.store.writeStreamObserved(stream); err != nil {
			return err
		}
	}
	return nil
}

func (options schedulerRunOptions) queueReview(workerTask SchedulerTask, workerStream SchedulerStream) error {
	mode, reviewers, err := options.reviewPolicy(workerTask, workerStream)
	if err != nil {
		return err
	}
	workerTask.Review, workerTask.Reviewers = mode, append([]string(nil), reviewers...)
	if err := options.store.updateTask(workerTask); err != nil {
		return fmt.Errorf("persist effective review policy: %w", err)
	}
	workerStream.Cause = ""
	if err := options.store.transition(workerStream, SchedulerAwaitingReview, "review task", "run the READY review task"); err != nil {
		return err
	}
	for i, vendor := range reviewers {
		key := vendor
		streamName := workerTask.Stream + "-review"
		if workerStream.ReviewGeneration > 0 {
			streamName += fmt.Sprintf("-v%d", workerStream.ReviewGeneration)
		}
		if workerStream.RejectRounds > 0 {
			streamName += fmt.Sprintf("-r%d", workerStream.RejectRounds+1)
		}
		focus := "independent review"
		if len(reviewers) > 1 {
			key = fmt.Sprintf("%s-%d", vendor, i+1)
			streamName += fmt.Sprintf("-%d", i+1)
			if vendor == "codex" && i == 1 {
				focus = "adversarial: find a concrete failure scenario in the changed behavior"
			}
		}
		reviewTask := SchedulerTask{ID: streamName, Stream: streamName, PromptTemplate: runnerReviewTemplate, Workdir: workerTask.Workdir, WorktreeRoot: workerTask.WorktreeRoot, RepoRoot: workerTask.RepoRoot, Base: workerTask.Base, Priority: workerTask.Priority, DependsOn: []string{workerTask.ID}, Vendor: vendor, Effort: workerTask.Effort, Role: schedulerReviewRole, ReviewerKey: key, ReviewerFocus: focus}
		if _, err := options.store.add(reviewTask); err != nil && !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("queue review task %s: %w", reviewTask.ID, err)
		}
	}
	return nil
}

func (options schedulerRunOptions) resumeAfterRemoteFailure(task SchedulerTask, stream SchedulerStream, verdict map[string]any) error {
	prompt := filepath.Join(options.state, "sched", "prompts", task.ID+"-remote-resume.md")
	var details []string
	if jobs, ok := verdict["jobs"].([]any); ok {
		for _, item := range jobs {
			job, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := job["name"].(string)
			for _, failure := range anyStrings(job["failures"]) {
				details = append(details, fmt.Sprintf("%s: %s", name, failure))
			}
			if logTail, _ := job["logTail"].(string); logTail != "" {
				details = append(details, name+" log: "+logTail)
			}
		}
	}
	if cause := verdictCause(verdict); cause != "" {
		details = append(details, cause)
	}
	body := fmt.Sprintf("Remote verification failed for stream %s. Resume the existing agent thread, inspect the failing tests below, fix the failures, and update .agent-report.md.\n\n%s\n", task.Stream, strings.Join(details, "\n"))
	if err := writeAtomicFile(prompt, []byte(body)); err != nil {
		return fmt.Errorf("write remote failure resume prompt: %w", err)
	}
	task.Prompt = prompt
	if err := options.store.updateTask(task); err != nil {
		return err
	}
	stream.Cause = strings.Join(details, "\n")
	return options.store.transition(stream, SchedulerReady, "", "resume the original agent thread with the remote failures")
}

func anyStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func (options schedulerRunOptions) renderReviewPrompt(task SchedulerTask) (string, error) {
	data, err := readRunnerTemplate(task.PromptTemplate)
	if err != nil {
		return "", fmt.Errorf("read review prompt template: %w", err)
	}
	streamName := task.Stream
	if len(task.DependsOn) == 1 {
		parent, parentErr := options.store.task(task.DependsOn[0])
		if parentErr != nil {
			return "", parentErr
		}
		streamName = parent.Stream
	}
	content := strings.ReplaceAll(string(data), "{stream}", streamName)
	content = strings.ReplaceAll(content, "{review-report}", filepath.Base(privateReviewReportPath(task)))
	if task.ReviewerFocus != "" {
		content += "\nReviewer focus: " + task.ReviewerFocus + "\n"
	}
	path := filepath.Join(options.state, "sched", "prompts", task.ID+".md")
	if err := writeAtomicFile(path, []byte(content)); err != nil {
		return "", fmt.Errorf("render review prompt: %w", err)
	}
	return path, nil
}

func schedulerFileURL(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: absolute}).String(), nil
}
