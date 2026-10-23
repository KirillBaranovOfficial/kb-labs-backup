//go:build darwin || linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type JobPhase string

const (
	PhaseWorktree  JobPhase = "worktree"
	PhaseInstall   JobPhase = "install"
	PhaseBuild     JobPhase = "build"
	PhaseAdmission JobPhase = "admission"
	PhaseRun       JobPhase = "run"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobOK        JobState = "ok"
	JobFailed    JobState = "failed"
	JobStalled   JobState = "stalled"
	JobLost      JobState = "lost"
	JobCancelled JobState = "cancelled"
	JobPaused    JobState = "paused"
	JobQuotaWait JobState = "quota-wait"
)

func (s JobState) Terminal() bool {
	return s == JobOK || s == JobFailed || s == JobStalled || s == JobLost || s == JobCancelled
}
func (s JobState) Attention() bool { return s == JobPaused || s == JobQuotaWait }

func (j Job) eventTime() time.Time {
	if j.Final != nil && !j.Final.FinishedAt.IsZero() {
		return j.Final.FinishedAt
	}
	return j.UpdatedAt
}
func (j Job) cursor() string { return fmt.Sprintf("%020d:%s", j.eventTime().UnixNano(), j.ID) }

type JobReason string

const (
	ReasonNone                JobReason = ""
	ReasonSetup               JobReason = "setup_failed"
	ReasonQuota               JobReason = "quota_limit"
	ReasonProviderUnreachable JobReason = "provider_unreachable"
	ReasonRateLimited         JobReason = "rate_limited"
	ReasonAuthExpired         JobReason = "auth_expired"
	ReasonVendorError         JobReason = "vendor_error"
	ReasonStall               JobReason = "no_activity"
	ReasonProcessLost         JobReason = "process_lost"
)

type JobTerminal struct {
	State       JobState  `json:"state"`
	Phase       JobPhase  `json:"phase"`
	Reason      JobReason `json:"reason,omitempty"`
	Next        string    `json:"next"`
	MessagePath string    `json:"messagePath,omitempty"`
	SessionID   string    `json:"sessionId,omitempty"`
	FinishedAt  time.Time `json:"finishedAt"`
}
type ProviderAttempt struct {
	Number     int       `json:"number"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	SessionID  string    `json:"sessionId,omitempty"`
	Reason     JobReason `json:"reason,omitempty"`
	Outcome    string    `json:"outcome"`
}
type Job struct {
	ID           string            `json:"id"`
	Stream       string            `json:"stream"`
	Vendor       string            `json:"vendor"`
	Model        string            `json:"model,omitempty"`
	State        JobState          `json:"state"`
	Phase        JobPhase          `json:"phase"`
	Reason       JobReason         `json:"reason,omitempty"`
	StartedAt    time.Time         `json:"startedAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	LastActivity time.Time         `json:"lastActivity,omitempty"`
	SessionID    string            `json:"sessionId,omitempty"`
	MessagePath  string            `json:"messagePath,omitempty"`
	Tokens       uint64            `json:"tokens,omitempty"`
	SlotGroup    string            `json:"slotGroup,omitempty"`
	SlotCeiling  int               `json:"slotCeiling,omitempty"`
	Attempts     []ProviderAttempt `json:"attempts,omitempty"`
	Final        *JobTerminal      `json:"terminal,omitempty"`
}

type jobStore struct {
	dir string
	mu  sync.Mutex
}

func newJobStore(dir string) *jobStore    { return &jobStore{dir: filepath.Join(dir, "jobs")} }
func (s *jobStore) path(id string) string { return filepath.Join(s.dir, id+".json") }
func (s *jobStore) Create(job Job) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(job.ID) == "" || strings.ContainsAny(job.ID, "/\\") {
		return Job{}, errors.New("job ID must be a non-empty filename-safe value")
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return Job{}, err
	}
	if _, err := os.Stat(s.path(job.ID)); err == nil {
		return Job{}, fmt.Errorf("job %q already exists", job.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Job{}, err
	}
	now := time.Now().UTC()
	if job.StartedAt.IsZero() {
		job.StartedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.LastActivity.IsZero() {
		job.LastActivity = now
	}
	if job.State == "" {
		job.State = JobQueued
	}
	return job, s.write(job)
}
func (s *jobStore) Get(id string) (Job, error) {
	b, err := os.ReadFile(s.path(id))
	if err != nil {
		return Job{}, err
	}
	var j Job
	if err = json.Unmarshal(b, &j); err != nil {
		return Job{}, err
	}
	return j, nil
}
func (s *jobStore) write(j Job) error {
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	tmp := s.path(j.ID) + ".tmp"
	if err = os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(j.ID))
}
func (s *jobStore) Terminate(id string, terminal JobTerminal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if j.Final != nil {
		return fmt.Errorf("job %q already has a terminal record", id)
	}
	if !terminal.State.Terminal() {
		return fmt.Errorf("state %q is not terminal", terminal.State)
	}
	terminal.FinishedAt = time.Now().UTC()
	j.State = terminal.State
	j.Phase = terminal.Phase
	j.Reason = terminal.Reason
	j.UpdatedAt = terminal.FinishedAt
	j.Final = &terminal
	return s.write(j)
}
func (s *jobStore) Update(id string, fn func(*Job) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	if j.Final != nil {
		return fmt.Errorf("job %q already has a terminal record", id)
	}
	if err := fn(&j); err != nil {
		return err
	}
	j.UpdatedAt = time.Now().UTC()
	return s.write(j)
}
func (s *jobStore) List() ([]Job, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Job{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		j, e := s.Get(strings.TrimSuffix(entry.Name(), ".json"))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out, nil
}
func (s *jobStore) Wait(ids []string, all bool, since string) ([]Job, string, error) {
	if since != "" {
		if _, _, ok := strings.Cut(since, ":"); !ok {
			return nil, "", fmt.Errorf("invalid job cursor %q", since)
		}
		if _, err := strconv.ParseInt(strings.SplitN(since, ":", 2)[0], 10, 64); err != nil {
			return nil, "", fmt.Errorf("invalid job cursor %q", since)
		}
	}
	jobs, err := s.List()
	if err != nil {
		return nil, "", err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var matched []Job
	for _, j := range jobs {
		if !wanted[j.ID] && !wanted[j.Stream] {
			continue
		}
		if !j.State.Terminal() && !j.State.Attention() {
			continue
		}
		if since != "" && j.cursor() <= since {
			continue
		}
		matched = append(matched, j)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].eventTime().Equal(matched[j].eventTime()) {
			return matched[i].ID < matched[j].ID
		}
		return matched[i].eventTime().Before(matched[j].eventTime())
	})
	if all && len(matched) < len(ids) {
		return nil, "", nil
	}
	if len(matched) == 0 {
		return nil, "", nil
	}
	if !all {
		matched = matched[:1]
	}
	return matched, matched[len(matched)-1].cursor(), nil
}

type VendorLimit struct {
	Vendor      string         `json:"vendor"`
	Window      string         `json:"window"`
	UsedPercent float64        `json:"usedPercent"`
	Blocked     bool           `json:"blocked,omitempty"`
	ResetsAt    *time.Time     `json:"resetsAt,omitempty"`
	Confidence  string         `json:"confidence"`
	Source      string         `json:"source"`
	Error       string         `json:"error,omitempty"`
	FetchedAt   time.Time      `json:"fetchedAt"`
	Attempts    []LimitAttempt `json:"attempts,omitempty"`
}
type AgentStatus struct {
	Jobs        []Job         `json:"jobs"`
	Limits      []VendorLimit `json:"limits"`
	DailyTokens []DailyTokens `json:"dailyTokens"`
}

type DailyTokens struct {
	Vendor string `json:"vendor"`
	Day    string `json:"day"`
	Tokens uint64 `json:"tokens"`
}

func dailyTokenTotals(jobs []Job) []DailyTokens {
	totals := map[string]uint64{}
	for _, job := range jobs {
		if job.Tokens == 0 || job.StartedAt.IsZero() {
			continue
		}
		day := job.StartedAt.UTC().Format("2006-01-02")
		key := job.Vendor + "\x00" + day
		totals[key] += job.Tokens
	}
	out := make([]DailyTokens, 0, len(totals))
	for key, tokens := range totals {
		parts := strings.SplitN(key, "\x00", 2)
		out = append(out, DailyTokens{Vendor: parts[0], Day: parts[1], Tokens: tokens})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Day == out[j].Day {
			return out[i].Vendor < out[j].Vendor
		}
		return out[i].Day < out[j].Day
	})
	return out
}
