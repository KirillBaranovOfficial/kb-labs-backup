//go:build darwin || linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	schedulerWorkerRole  = "worker"
	schedulerReviewRole  = "review"
	schedulerMergeRole   = "merge"
	schedulerRemoteWait  = 45 * time.Second
	schedulerRemoteLimit = 90 * time.Minute
	schedulerOutageLimit = 2
)

type SchedulerTask struct {
	ID             string    `json:"id"`
	Stream         string    `json:"stream"`
	Prompt         string    `json:"prompt_path,omitempty"`
	PromptTemplate string    `json:"prompt_template,omitempty"`
	Workdir        string    `json:"workdir"`
	RepoRoot       string    `json:"repo_root"`
	Base           string    `json:"base_branch"`
	Priority       int       `json:"priority"`
	DependsOn      []string  `json:"depends_on,omitempty"`
	Vendor         string    `json:"vendor"`
	Effort         string    `json:"effort"`
	Role           string    `json:"role"`
	Review         string    `json:"review"`
	Reviewers      []string  `json:"reviewers"`
	WorktreeRoot   string    `json:"worktree_root"`
	ReviewerKey    string    `json:"reviewer_key,omitempty"`
	ReviewerFocus  string    `json:"reviewer_focus,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

type SchedulerStreamState string

const (
	SchedulerReady                SchedulerStreamState = "ready"
	SchedulerRunning              SchedulerStreamState = "running"
	SchedulerAwaitingRemote       SchedulerStreamState = "awaiting-remote"
	SchedulerAwaitingReview       SchedulerStreamState = "awaiting-review"
	SchedulerAwaitingOrchestrator SchedulerStreamState = "awaiting-orchestrator"
	SchedulerDone                 SchedulerStreamState = "done"
	SchedulerFailed               SchedulerStreamState = "failed"
)

type SchedulerStream struct {
	Stream           string               `json:"stream"`
	TaskID           string               `json:"task_id"`
	State            SchedulerStreamState `json:"state"`
	ChangedAt        time.Time            `json:"changed_at"`
	Age              string               `json:"age,omitempty"`
	WaitingFor       string               `json:"waiting_for,omitempty"`
	NextStep         string               `json:"next_step,omitempty"`
	ThreadID         string               `json:"thread_id,omitempty"`
	Submission       *SchedulerSubmission `json:"submission,omitempty"`
	Cause            string               `json:"cause,omitempty"`
	ReviewResults    map[string]string    `json:"review_results,omitempty"`
	ReviewReports    []string             `json:"review_reports,omitempty"`
	RejectRounds     int                  `json:"reject_rounds,omitempty"`
	ReviewGeneration int                  `json:"review_generation,omitempty"`
}

type SchedulerSubmission struct {
	SHA            string    `json:"sha"`
	TreeSHA        string    `json:"tree_sha"`
	ExpectedRefSHA string    `json:"expected_ref_sha,omitempty"`
	Ref            string    `json:"ref"`
	Purpose        string    `json:"purpose,omitempty"`
	RunID          string    `json:"run_id,omitempty"`
	Status         string    `json:"status,omitempty"`
	Conclusion     string    `json:"conclusion,omitempty"`
	DeadlineAt     time.Time `json:"deadline_at"`
	Attempts       int       `json:"attempts"`
}

type SchedulerMergeRecord struct {
	CommitSHA string `json:"commit_sha"`
	ParentSHA string `json:"parent_sha"`
	Stream    string `json:"stream"`
}

type SchedulerBisectPrefix struct {
	MergeSHA   string    `json:"merge_sha"`
	Stream     string    `json:"stream"`
	SHA        string    `json:"sha"`
	Ref        string    `json:"ref"`
	RunID      string    `json:"run_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Conclusion string    `json:"conclusion,omitempty"`
	DeadlineAt time.Time `json:"deadline_at"`
	Attempts   int       `json:"attempts"`
	Failures   []string  `json:"failures,omitempty"`
}

type SchedulerBisect struct {
	CandidateStream string                  `json:"candidate_stream"`
	CandidateTaskID string                  `json:"candidate_task_id"`
	CandidateSHA    string                  `json:"candidate_sha"`
	CandidateFails  []string                `json:"candidate_failures,omitempty"`
	Prefixes        []SchedulerBisectPrefix `json:"prefixes"`
}

type SchedulerIntegration struct {
	LastGreenSHA string                 `json:"last_green_sha,omitempty"`
	Merges       []SchedulerMergeRecord `json:"merges,omitempty"`
	Bisect       *SchedulerBisect       `json:"bisect,omitempty"`
	LastBisect   *SchedulerBisect       `json:"last_bisect,omitempty"`
}

type schedulerStore struct {
	dir string
	mu  sync.Mutex
}

var errSchedulerAlreadyRunning = errors.New("scheduler already owns this state directory")

type schedulerDependencyFlag []string

func (values *schedulerDependencyFlag) String() string { return strings.Join(*values, ",") }
func (values *schedulerDependencyFlag) Set(value string) error {
	if value == "" {
		return errors.New("dependency task ID cannot be empty")
	}
	*values = append(*values, value)
	return nil
}

func newSchedulerStore(state string) *schedulerStore {
	return &schedulerStore{dir: filepath.Join(state, "sched")}
}

func (s *schedulerStore) tasksDir() string   { return filepath.Join(s.dir, "tasks") }
func (s *schedulerStore) streamsDir() string { return filepath.Join(s.dir, "streams") }
func (s *schedulerStore) taskPath(id string) string {
	return filepath.Join(s.tasksDir(), id+".json")
}
func (s *schedulerStore) streamPath(id string) string {
	return filepath.Join(s.streamsDir(), id+".json")
}
func (s *schedulerStore) integrationPath() string { return filepath.Join(s.dir, "integration.json") }
func (s *schedulerStore) repositoryPath() string  { return filepath.Join(s.dir, "repository.json") }

func (s *schedulerStore) integration() (SchedulerIntegration, error) {
	var state SchedulerIntegration
	if err := readSchedulerJSON(s.integrationPath(), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SchedulerIntegration{}, nil
		}
		return SchedulerIntegration{}, fmt.Errorf("read scheduler integration state: %w", err)
	}
	return state, nil
}

func (s *schedulerStore) writeIntegration(state SchedulerIntegration) error {
	return s.withLock(func() error { return s.writeJSON(s.integrationPath(), state) })
}

func (s *schedulerStore) recordIntegrationMerge(merge SchedulerMergeRecord) error {
	if !fullGitSHA.MatchString(merge.CommitSHA) || !fullGitSHA.MatchString(merge.ParentSHA) {
		return fmt.Errorf("integration merge for %s must have full parent and commit SHAs", merge.Stream)
	}
	if err := validateSchedulerKey(merge.Stream, "integration merge stream"); err != nil {
		return err
	}
	return s.withLock(func() error {
		var state SchedulerIntegration
		if err := readSchedulerJSON(s.integrationPath(), &state); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read integration state before recording merge: %w", err)
		}
		if state.Bisect != nil {
			return errors.New("cannot record an integration merge while candidate bisect is active")
		}
		if len(state.Merges) == 0 && state.LastGreenSHA == "" {
			state.LastGreenSHA = merge.ParentSHA
		}
		if len(state.Merges) > 0 {
			last := state.Merges[len(state.Merges)-1]
			if last.CommitSHA != merge.ParentSHA {
				return fmt.Errorf("integration merge parent %s does not follow recorded merge %s", merge.ParentSHA, last.CommitSHA)
			}
		} else if state.LastGreenSHA != merge.ParentSHA {
			return fmt.Errorf("integration merge parent %s does not match last green candidate %s", merge.ParentSHA, state.LastGreenSHA)
		}
		for _, previous := range state.Merges {
			if previous.CommitSHA == merge.CommitSHA {
				return fmt.Errorf("integration merge %s is already recorded", merge.CommitSHA)
			}
		}
		state.Merges = append(state.Merges, merge)
		return s.writeJSON(s.integrationPath(), state)
	})
}

func (s *schedulerStore) markIntegrationGreen(sha string) error {
	if !fullGitSHA.MatchString(sha) {
		return fmt.Errorf("green candidate must have a full commit SHA, got %q", sha)
	}
	return s.withLock(func() error {
		var state SchedulerIntegration
		if err := readSchedulerJSON(s.integrationPath(), &state); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("read integration state before candidate pass: %w", err)
		}
		cut := -1
		for index, merge := range state.Merges {
			if merge.CommitSHA == sha {
				cut = index
				break
			}
		}
		if cut < 0 && len(state.Merges) > 0 {
			return fmt.Errorf("passing candidate %s is not a recorded integration prefix", sha)
		}
		state.LastGreenSHA = sha
		if cut >= 0 {
			state.Merges = append([]SchedulerMergeRecord(nil), state.Merges[cut+1:]...)
		}
		state.Bisect = nil
		return s.writeJSON(s.integrationPath(), state)
	})
}

func validateSchedulerKey(value, label string) error {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.ContainsAny(value, "/\\\x00") {
		return fmt.Errorf("%s must be a non-empty filename-safe value", label)
	}
	return nil
}

func privateReviewReportPath(task SchedulerTask) string {
	return filepath.Join(task.Workdir, ".review-report-"+task.Stream+".md")
}

func (s *schedulerStore) withLock(operation func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create scheduler state directory: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(s.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open scheduler lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return errors.Join(fmt.Errorf("lock scheduler state: %w", err), lock.Close())
	}
	operationErr := operation()
	unlockErr := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	closeErr := lock.Close()
	return errors.Join(operationErr, unlockErr, closeErr)
}

func (s *schedulerStore) add(task SchedulerTask) (SchedulerTask, error) {
	if err := validateSchedulerKey(task.Stream, "stream"); err != nil {
		return SchedulerTask{}, err
	}
	if task.Base == "" || task.Vendor == "" || task.Effort == "" || task.Workdir == "" || task.RepoRoot == "" {
		return SchedulerTask{}, errors.New("base branch, vendor, effort, resolved workdir, and repository root are required")
	}
	if task.Role == "" {
		task.Role = schedulerWorkerRole
	}
	if task.Role != schedulerWorkerRole && task.Role != schedulerReviewRole && task.Role != schedulerMergeRole {
		return SchedulerTask{}, fmt.Errorf("role %q is unsupported", task.Role)
	}
	if task.Role == schedulerWorkerRole {
		if task.Prompt == "" {
			return SchedulerTask{}, errors.New("worker task requires a prompt file")
		}
		if err := requireRegularFile(task.Prompt, "prompt"); err != nil {
			return SchedulerTask{}, err
		}
		if task.Review == "" {
			task.Review = "single"
		}
		if task.Review != "single" && task.Review != "cross" {
			return SchedulerTask{}, fmt.Errorf("review policy %q is unsupported", task.Review)
		}
		if len(task.Reviewers) == 0 {
			task.Reviewers = []string{task.Vendor}
		}
		if task.Review == "cross" && len(task.Reviewers) < 2 {
			return SchedulerTask{}, errors.New("cross review requires at least two reviewers")
		}
	} else {
		if task.PromptTemplate == "" {
			return SchedulerTask{}, errors.New("review task requires a prompt template")
		}
		if _, err := readRunnerTemplate(task.PromptTemplate); err != nil {
			return SchedulerTask{}, err
		}
	}
	workdir, err := filepath.Abs(task.Workdir)
	if err != nil {
		return SchedulerTask{}, fmt.Errorf("resolve workdir: %w", err)
	}
	repoRoot, err := filepath.Abs(task.RepoRoot)
	if err != nil {
		return SchedulerTask{}, fmt.Errorf("resolve repository root: %w", err)
	}
	task.Workdir, task.RepoRoot = workdir, repoRoot
	if task.WorktreeRoot == "" {
		task.WorktreeRoot = filepath.Dir(workdir)
	}
	task.WorktreeRoot, err = filepath.Abs(task.WorktreeRoot)
	if err != nil {
		return SchedulerTask{}, fmt.Errorf("resolve worktree root: %w", err)
	}
	if task.Prompt != "" {
		task.Prompt, err = filepath.Abs(task.Prompt)
		if err != nil {
			return SchedulerTask{}, fmt.Errorf("resolve prompt path: %w", err)
		}
	}
	if task.ID == "" {
		task.ID = fmt.Sprintf("%s-%d", task.Stream, time.Now().UTC().UnixNano())
	}
	if err := validateSchedulerKey(task.ID, "task ID"); err != nil {
		return SchedulerTask{}, err
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	err = s.withLock(func() error {
		if _, err := os.Stat(s.taskPath(task.ID)); err == nil {
			return fmt.Errorf("task %q already exists", task.ID)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if _, err := os.Stat(s.streamPath(task.Stream)); err == nil {
			return fmt.Errorf("stream %q already has scheduler state", task.Stream)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, dependency := range task.DependsOn {
			if err := validateSchedulerKey(dependency, "dependency task ID"); err != nil {
				return err
			}
			if _, err := os.Stat(s.taskPath(dependency)); err != nil {
				return fmt.Errorf("dependency task %q does not exist: %w", dependency, err)
			}
		}
		if err := s.bindRepositoryLocked(repoRoot); err != nil {
			return err
		}
		if err := s.writeJSON(s.taskPath(task.ID), task); err != nil {
			return fmt.Errorf("persist scheduler task: %w", err)
		}
		stream := SchedulerStream{Stream: task.Stream, TaskID: task.ID}
		if err := s.transitionLocked(stream, SchedulerReady, "", "wait for dispatch admission"); err != nil {
			return errors.Join(fmt.Errorf("initialize scheduler stream: %w", err), os.Remove(s.taskPath(task.ID)))
		}
		return nil
	})
	if err != nil {
		return SchedulerTask{}, err
	}
	return task, nil
}

type schedulerRepositoryBinding struct {
	Root string `json:"root"`
}

func (s *schedulerStore) bindRepositoryLocked(root string) error {
	var binding schedulerRepositoryBinding
	err := readSchedulerJSON(s.repositoryPath(), &binding)
	if errors.Is(err, os.ErrNotExist) {
		return s.writeJSON(s.repositoryPath(), schedulerRepositoryBinding{Root: root})
	}
	if err != nil {
		return fmt.Errorf("read scheduler repository binding: %w", err)
	}
	if binding.Root != root {
		return fmt.Errorf("scheduler state repository is %s; cannot add task from repository %s", binding.Root, root)
	}
	return nil
}

func (s *schedulerStore) repositoryRoot() (string, error) {
	var binding schedulerRepositoryBinding
	if err := readSchedulerJSON(s.repositoryPath(), &binding); err != nil {
		return "", fmt.Errorf("read scheduler repository binding: %w", err)
	}
	if binding.Root == "" {
		return "", errors.New("scheduler repository binding has an empty root")
	}
	return binding.Root, nil
}

func requireRegularFile(path, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read %s file %q: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s path %q is not a regular file", label, path)
	}
	return nil
}

func (s *schedulerStore) writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(path, append(data, '\n'))
}

func readSchedulerJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (s *schedulerStore) task(id string) (SchedulerTask, error) {
	if err := validateSchedulerKey(id, "task ID"); err != nil {
		return SchedulerTask{}, err
	}
	data, err := os.ReadFile(s.taskPath(id))
	if err != nil {
		return SchedulerTask{}, err
	}
	var task SchedulerTask
	if err := json.Unmarshal(data, &task); err != nil {
		return SchedulerTask{}, fmt.Errorf("decode task %s: %w", id, err)
	}
	return task, nil
}

func (s *schedulerStore) stream(id string) (SchedulerStream, error) {
	if err := validateSchedulerKey(id, "stream"); err != nil {
		return SchedulerStream{}, err
	}
	data, err := os.ReadFile(s.streamPath(id))
	if err != nil {
		return SchedulerStream{}, err
	}
	var stream SchedulerStream
	if err := json.Unmarshal(data, &stream); err != nil {
		return SchedulerStream{}, fmt.Errorf("decode stream %s: %w", id, err)
	}
	return stream, nil
}

func (s *schedulerStore) tasks() ([]SchedulerTask, error) {
	entries, err := os.ReadDir(s.tasksDir())
	if errors.Is(err, os.ErrNotExist) {
		return []SchedulerTask{}, nil
	}
	if err != nil {
		return nil, err
	}
	tasks := make([]SchedulerTask, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		task, err := s.task(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		if !tasks[i].CreatedAt.Equal(tasks[j].CreatedAt) {
			return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
		}
		return tasks[i].ID < tasks[j].ID
	})
	return tasks, nil
}

func (s *schedulerStore) cancel(id string) error {
	return s.withLock(func() error {
		task, err := s.task(id)
		if err != nil {
			return err
		}
		stream, err := s.stream(task.Stream)
		if err != nil {
			return err
		}
		if stream.State != SchedulerReady {
			return fmt.Errorf("task %q is %s and cannot be cancelled from the backlog", id, stream.State)
		}
		if err := s.appendEvent(map[string]any{"kind": "scheduler.cancelled", "task_id": task.ID, "stream": task.Stream, "time": time.Now().UTC()}); err != nil {
			return err
		}
		if err := os.Remove(s.taskPath(id)); err != nil {
			return err
		}
		if err := os.Remove(s.streamPath(task.Stream)); err != nil {
			return fmt.Errorf("task cancelled but stream record could not be removed: %w", err)
		}
		return nil
	})
}

func (s *schedulerStore) updateTask(task SchedulerTask) error {
	return s.withLock(func() error {
		repoRoot, err := filepath.Abs(task.RepoRoot)
		if err != nil {
			return fmt.Errorf("resolve repository root: %w", err)
		}
		if err := s.bindRepositoryLocked(repoRoot); err != nil {
			return err
		}
		task.RepoRoot = repoRoot
		return s.writeJSON(s.taskPath(task.ID), task)
	})
}

func (s *schedulerStore) transition(stream SchedulerStream, next SchedulerStreamState, waiting, action string) error {
	return s.withLock(func() error { return s.transitionLocked(stream, next, waiting, action) })
}

func (s *schedulerStore) claimReady(streamName, taskID string) (SchedulerStream, bool, error) {
	var claimed SchedulerStream
	var didClaim bool
	err := s.withLock(func() error {
		current, err := s.stream(streamName)
		if err != nil {
			return err
		}
		if current.State != SchedulerReady || current.TaskID != taskID {
			return nil
		}
		if err := s.transitionLocked(current, SchedulerRunning, "agent round", "wait for agent report"); err != nil {
			return err
		}
		claimed, err = s.stream(streamName)
		if err != nil {
			return err
		}
		didClaim = true
		return nil
	})
	return claimed, didClaim, err
}

func (s *schedulerStore) acquireRunLease() (*os.File, error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, fmt.Errorf("create scheduler state directory: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(s.dir, ".run.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open scheduler run lease: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("close scheduler run lease after acquisition failure: %w", errors.Join(err, closeErr))
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, errSchedulerAlreadyRunning
		}
		return nil, fmt.Errorf("acquire scheduler run lease: %w", err)
	}
	return file, nil
}

func releaseRunLease(lease *os.File) error {
	unlockErr := syscall.Flock(int(lease.Fd()), syscall.LOCK_UN)
	closeErr := lease.Close()
	return errors.Join(unlockErr, closeErr)
}

func (s *schedulerStore) transitionLocked(stream SchedulerStream, next SchedulerStreamState, waiting, action string) error {
	if !validSchedulerTransition(stream.State, next) {
		return fmt.Errorf("invalid scheduler transition %q -> %q for stream %s", stream.State, next, stream.Stream)
	}
	if next == SchedulerAwaitingOrchestrator && !validSchedulerDecisionCause(stream.Cause) {
		return fmt.Errorf("stream %s cannot await the orchestrator with unsupported cause %q", stream.Stream, stream.Cause)
	}
	previous := stream.State
	stream.State, stream.WaitingFor, stream.NextStep = next, waiting, action
	stream.ChangedAt = time.Now().UTC()
	if err := s.writeJSON(s.streamPath(stream.Stream), stream); err != nil {
		return fmt.Errorf("persist stream transition: %w", err)
	}
	event := map[string]any{"kind": "scheduler.transition", "stream": stream.Stream, "from": previous, "to": next, "waiting_for": waiting, "next_step": action, "cause": stream.Cause, "time": stream.ChangedAt}
	return s.appendEvent(event)
}

func validSchedulerDecisionCause(cause string) bool {
	switch cause {
	case "plan-question", "review-disagreement", "review-missing", "review-unavailable", "third-reject", "outage-after-retries", "no-proposed-commits", "bisect-inconclusive":
		return true
	default:
		return false
	}
}

func validSchedulerTransition(from, to SchedulerStreamState) bool {
	if from == "" {
		return to == SchedulerReady
	}
	switch from {
	case SchedulerReady:
		return to == SchedulerRunning || to == SchedulerFailed
	case SchedulerRunning:
		return to == SchedulerReady || to == SchedulerAwaitingRemote || to == SchedulerAwaitingReview || to == SchedulerAwaitingOrchestrator || to == SchedulerDone || to == SchedulerFailed
	case SchedulerAwaitingRemote:
		return to == SchedulerReady || to == SchedulerAwaitingReview || to == SchedulerAwaitingOrchestrator || to == SchedulerDone || to == SchedulerFailed
	case SchedulerAwaitingReview:
		return to == SchedulerDone || to == SchedulerReady || to == SchedulerAwaitingRemote || to == SchedulerAwaitingOrchestrator || to == SchedulerFailed
	case SchedulerAwaitingOrchestrator:
		return to == SchedulerReady || to == SchedulerDone || to == SchedulerFailed
	default:
		return false
	}
}

func (s *schedulerStore) appendEvent(value any) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	data, encodeErr := json.Marshal(value)
	if encodeErr != nil {
		return encodeErr
	}
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return err
	}
	streamName, _ := event["stream"].(string)
	if streamName != "" {
		taskID, _ := event["task_id"].(string)
		if taskID == "" {
			stream, err := s.stream(streamName)
			if err != nil {
				return fmt.Errorf("resolve HANDOFF stream owner: %w", err)
			}
			taskID = stream.TaskID
		}
		task, err := s.task(taskID)
		if err != nil {
			return fmt.Errorf("resolve HANDOFF task owner: %w", err)
		}
		event["task_id"] = task.ID
		event["handoff_path"] = filepath.Join(task.WorktreeRoot, "HANDOFF.md")
		data, err = json.Marshal(event)
		if err != nil {
			return err
		}
	}
	file, err := os.OpenFile(filepath.Join(s.dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if streamName == "" {
		return nil
	}
	return s.projectHandoff(data)
}

func (s *schedulerStore) projectHandoff(data []byte) error {
	var event map[string]any
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("decode scheduler event for HANDOFF: %w", err)
	}
	streamName, _ := event["stream"].(string)
	if streamName == "" {
		return nil
	}
	handoff, _ := event["handoff_path"].(string)
	if handoff == "" {
		taskID, _ := event["task_id"].(string)
		if taskID == "" {
			stream, err := s.stream(streamName)
			if err != nil {
				return fmt.Errorf("resolve historical HANDOFF stream owner: %w", err)
			}
			taskID = stream.TaskID
		}
		task, err := s.task(taskID)
		if err != nil {
			return fmt.Errorf("resolve historical HANDOFF task owner: %w", err)
		}
		handoff = filepath.Join(task.WorktreeRoot, "HANDOFF.md")
	}
	if !filepath.IsAbs(handoff) {
		return fmt.Errorf("scheduler event HANDOFF path %q is not absolute", handoff)
	}
	if err := os.MkdirAll(filepath.Dir(handoff), 0o700); err != nil {
		return fmt.Errorf("create HANDOFF parent: %w", err)
	}
	handoffFile, err := os.OpenFile(handoff, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open scheduler HANDOFF: %w", err)
	}
	line := fmt.Sprintf("scheduler=%s %s", streamName, strings.TrimSpace(string(data)))
	existing, readErr := os.ReadFile(handoff)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return errors.Join(fmt.Errorf("read scheduler HANDOFF for idempotency: %w", readErr), handoffFile.Close())
	}
	for _, previous := range strings.Split(string(existing), "\n") {
		if previous == line {
			return handoffFile.Close()
		}
	}
	_, writeErr := fmt.Fprintln(handoffFile, line)
	return errors.Join(writeErr, handoffFile.Close())
}

func (s *schedulerStore) replayHandoff() error {
	path := filepath.Join(s.dir, "events.jsonl")
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open scheduler event log for HANDOFF recovery: %w", err)
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if err := s.projectHandoff(scanner.Bytes()); err != nil {
			return errors.Join(fmt.Errorf("replay scheduler event %d to HANDOFF: %w", lineNumber, err), file.Close())
		}
	}
	scanErr := scanner.Err()
	closeErr := file.Close()
	return errors.Join(scanErr, closeErr)
}

func (s *schedulerStore) streams() ([]SchedulerStream, error) {
	entries, err := os.ReadDir(s.streamsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []SchedulerStream{}, nil
	}
	if err != nil {
		return nil, err
	}
	streams := make([]SchedulerStream, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		stream, err := s.stream(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		if !stream.ChangedAt.IsZero() {
			stream.Age = time.Since(stream.ChangedAt).Round(time.Second).String()
		}
		streams = append(streams, stream)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].Stream < streams[j].Stream })
	return streams, nil
}

func schedulerCommand(root runnerRoot, args []string, out, errOut io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("%s sched: an operation is required; next: %s sched --help", root.Name(), root.InvocationCommand())
	}
	if args[0] == "--help" || args[0] == "-h" {
		_, err := fmt.Fprintf(out, "Usage: %s sched add|ls|cancel|complete|ps|run [flags]\n", root.Name())
		return 0, err
	}
	switch args[0] {
	case "add":
		return schedulerAddCommand(root, args[1:], out)
	case "ls", "ps":
		return schedulerListCommand(root, args[0], args[1:], out)
	case "cancel":
		return schedulerCancelCommand(root, args[1:], out)
	case "complete":
		return schedulerCompleteCommand(root, args[1:], out)
	case "run":
		return schedulerRunCommand(root, args[1:])
	default:
		return 2, fmt.Errorf("%s sched: unknown operation %q; next: %s sched --help", root.Name(), args[0], root.InvocationCommand())
	}
}

func schedulerAddCommand(root runnerRoot, args []string, out io.Writer) (int, error) {
	fs := flag.NewFlagSet("sched add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	stream := fs.String("stream", "", "stable stream name")
	prompt := fs.String("prompt", "", "worker prompt file")
	promptTemplate := fs.String("prompt-template", runnerReviewTemplate, "embedded review prompt template")
	workdir := fs.String("workdir", "", "explicit task worktree path")
	worktreeRoot := fs.String("worktree-root", "", "root directory for default task worktrees")
	base := fs.String("base", "", "base branch")
	priority := fs.Int("priority", 0, "task priority")
	vendor := fs.String("vendor", defaultAgentVendor, "agent vendor")
	effort := fs.String("effort", "high", "agent reasoning effort")
	role := fs.String("role", schedulerWorkerRole, "worker or review")
	review := fs.String("review", "single", "single or cross review")
	var reviewers schedulerDependencyFlag
	fs.Var(&reviewers, "reviewer", "review vendor (repeatable)")
	var dependencies schedulerDependencyFlag
	fs.Var(&dependencies, "depends-on", "dependency task ID (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2, fmt.Errorf("%s sched add: %w; next: %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	if len(fs.Args()) != 0 {
		return 2, fmt.Errorf("%s sched add: unexpected arguments; next: %s sched add --help", root.Name(), root.InvocationCommand())
	}
	if *stream == "" || *base == "" {
		return 2, fmt.Errorf("%s sched add: --stream and --base are required; next: %s sched add --help", root.Name(), root.InvocationCommand())
	}
	repoRoot, err := schedulerCommandOutput("", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return 1, fmt.Errorf("%s sched add: resolve current repository: %w; next: cd <repository-root> && %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	repoRoot, err = filepath.Abs(repoRoot)
	if err != nil {
		return 1, fmt.Errorf("%s sched add: resolve repository root: %w; next: %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	if *worktreeRoot == "" {
		*worktreeRoot = filepath.Dir(repoRoot)
	}
	worktreeRootPath, err := filepath.Abs(*worktreeRoot)
	if err != nil {
		return 1, fmt.Errorf("%s sched add: resolve worktree root: %w; next: %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	resolvedWorkdir := *workdir
	if resolvedWorkdir == "" {
		resolvedWorkdir = filepath.Join(worktreeRootPath, *stream)
	}
	resolvedWorkdir, err = filepath.Abs(resolvedWorkdir)
	if err != nil {
		return 1, fmt.Errorf("%s sched add: resolve workdir: %w; next: %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	template := *promptTemplate
	if *role == schedulerReviewRole && template == "" {
		template = runnerReviewTemplate
	}
	task, err := newSchedulerStore(*state).add(SchedulerTask{Stream: *stream, Prompt: *prompt, PromptTemplate: template, Workdir: resolvedWorkdir, WorktreeRoot: worktreeRootPath, RepoRoot: repoRoot, Base: *base, Priority: *priority, DependsOn: dependencies, Vendor: *vendor, Effort: *effort, Role: *role, Review: *review, Reviewers: reviewers})
	if err != nil {
		return 1, fmt.Errorf("%s sched add: %w; next: %s sched add --help", root.Name(), err, root.InvocationCommand())
	}
	return 0, json.NewEncoder(out).Encode(task)
}

func schedulerListCommand(root runnerRoot, operation string, args []string, out io.Writer) (int, error) {
	fs := flag.NewFlagSet("sched "+operation, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	jsonOutput := fs.Bool("json", false, "write JSON")
	if err := fs.Parse(args); err != nil {
		return 2, fmt.Errorf("%s sched %s: %w; next: %s sched %s --help", root.Name(), operation, err, root.InvocationCommand(), operation)
	}
	store := newSchedulerStore(*state)
	if operation == "ls" {
		tasks, err := store.tasks()
		if err != nil {
			return 1, fmt.Errorf("%s sched ls: %w; next: %s sched ls --help", root.Name(), err, root.InvocationCommand())
		}
		if *jsonOutput {
			return 0, json.NewEncoder(out).Encode(tasks)
		}
		for _, task := range tasks {
			if _, err := fmt.Fprintf(out, "%-36s %-24s %-8s priority=%d vendor=%s effort=%s workdir=%s\n", task.ID, task.Stream, task.Role, task.Priority, task.Vendor, task.Effort, task.Workdir); err != nil {
				return 1, err
			}
		}
		return 0, nil
	}
	streams, err := store.streams()
	if err != nil {
		return 1, fmt.Errorf("%s sched ps: %w; next: %s sched ps --help", root.Name(), err, root.InvocationCommand())
	}
	sort.SliceStable(streams, func(i, j int) bool {
		first := streams[i].State == SchedulerAwaitingOrchestrator
		second := streams[j].State == SchedulerAwaitingOrchestrator
		if first != second {
			return first
		}
		return streams[i].Stream < streams[j].Stream
	})
	if *jsonOutput {
		return 0, json.NewEncoder(out).Encode(streams)
	}
	for _, stream := range streams {
		if _, err := fmt.Fprintf(out, "%-24s %-26s age=%-10s waiting=%-22s next=%s\n", stream.Stream, stream.State, stream.Age, stream.WaitingFor, stream.NextStep); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func schedulerCancelCommand(root runnerRoot, args []string, out io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("%s sched cancel: task ID is required; next: %s sched cancel --help", root.Name(), root.InvocationCommand())
	}
	id := args[len(args)-1]
	fs := flag.NewFlagSet("sched cancel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	if err := fs.Parse(args[:len(args)-1]); err != nil {
		return 2, fmt.Errorf("%s sched cancel: %w; next: %s sched cancel --help", root.Name(), err, root.InvocationCommand())
	}
	if err := newSchedulerStore(*state).cancel(id); err != nil {
		return 1, fmt.Errorf("%s sched cancel: %w; next: %s sched ls --state %s", root.Name(), err, root.InvocationCommand(), shellQuote(*state))
	}
	_, err := fmt.Fprintf(out, "cancelled task %s\n", id)
	return 0, err
}

func schedulerCompleteCommand(root runnerRoot, args []string, out io.Writer) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("%s sched complete: stream name is required; next: %s sched complete --help", root.Name(), root.InvocationCommand())
	}
	streamName := args[len(args)-1]
	fs := flag.NewFlagSet("sched complete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	if err := fs.Parse(args[:len(args)-1]); err != nil {
		return 2, fmt.Errorf("%s sched complete: %w; next: %s sched complete --help", root.Name(), err, root.InvocationCommand())
	}
	store := newSchedulerStore(*state)
	stream, err := store.stream(streamName)
	if err != nil {
		return 1, fmt.Errorf("%s sched complete: %w; next: %s sched ps --state %s", root.Name(), err, root.InvocationCommand(), shellQuote(*state))
	}
	if stream.State != SchedulerAwaitingOrchestrator {
		return 1, fmt.Errorf("%s sched complete: stream %s is %s; only an awaiting-orchestrator stream can be completed; next: %s sched ps --state %s --json", root.Name(), streamName, stream.State, root.InvocationCommand(), shellQuote(*state))
	}
	if err := store.transition(stream, SchedulerDone, "", "dependencies may now dispatch"); err != nil {
		return 1, err
	}
	_, err = fmt.Fprintf(out, "completed stream %s\n", streamName)
	return 0, err
}

func schedulerRunCommand(root runnerRoot, args []string) (int, error) {
	fs := flag.NewFlagSet("sched run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	state := fs.String("state", defaultRunnerState(), "runner state directory")
	once := fs.Bool("once", false, "dispatch available work and poll remote runs once")
	slots := fs.Int("slots", 5, "maximum concurrent local worker rounds")
	if err := fs.Parse(args); err != nil {
		return 2, fmt.Errorf("%s sched run: %w; next: %s sched run --help", root.Name(), err, root.InvocationCommand())
	}
	if len(fs.Args()) != 0 || *slots < 1 {
		return 2, fmt.Errorf("%s sched run: provide no positional arguments and a positive --slots value; next: %s sched run --help", root.Name(), root.InvocationCommand())
	}
	return runScheduler(context.Background(), root, *state, *slots, *once)
}
