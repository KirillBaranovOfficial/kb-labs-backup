package host

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const startupDeadlineEnvironment = "KB_CREATE_INTERNAL_STARTUP_DEADLINE_UNIX_NANO"

// ProcessEnvironmentWithStartupDeadline extends the private supervise launch
// contract with the absolute deadline owned by the outer start command.
func ProcessEnvironmentWithStartupDeadline(env []string, deadline time.Time) []string {
	prefix := startupDeadlineEnvironment + "="
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+strconv.FormatInt(deadline.UnixNano(), 10))
}

// ConsumeStartupDeadlineEnvironment reads and removes the private deadline
// before the supervisor constructs the host's inherited environment.
func ConsumeStartupDeadlineEnvironment() (time.Time, bool, error) {
	value, ok := os.LookupEnv(startupDeadlineEnvironment)
	if !ok {
		return time.Time{}, false, nil
	}
	if err := os.Unsetenv(startupDeadlineEnvironment); err != nil {
		return time.Time{}, true, fmt.Errorf("could not consume internal startup deadline: %w", err)
	}
	nanoseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, true, fmt.Errorf("invalid internal startup deadline")
	}
	return time.Unix(0, nanoseconds), true, nil
}

// SetStartupDeadline makes the next host Start consume the deadline handed
// down by the detached start command. Later restarts derive their own budget.
func (r *ProcessRunner) SetStartupDeadline(deadline time.Time) {
	r.mu.Lock()
	r.startupDeadline = deadline
	r.mu.Unlock()
}
