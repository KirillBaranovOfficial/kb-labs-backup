//go:build darwin || linux

package proc

import (
	"fmt"
	"time"

	"github.com/kb-labs/clikit/proc/child"
)

type childProcessObserver struct{}

// ChildProcessObserver supplies child lifecycle code with identity data from
// the process-table owner.
func ChildProcessObserver() child.ProcessObserver { return childProcessObserver{} }

func (childProcessObserver) RootIdentity(pid int) (child.ProcessIdentity, error) {
	startedAt, identity, _, err := readProcessStartIdentity(pid)
	if err != nil {
		return child.ProcessIdentity{}, err
	}
	if identity == "" || startedAt.IsZero() {
		return child.ProcessIdentity{}, fmt.Errorf("started process %d has no live start identity", pid)
	}
	return child.ProcessIdentity{PID: pid, Identity: identity, StartedAt: startedAt}, nil
}

func (childProcessObserver) LiveMembers(groupID int, root child.ProcessIdentity) ([]child.ProcessMember, error) {
	processes, err := (osProcessSource{}).Snapshot("")
	if err != nil {
		return liveOwnedGroupMembers(processes, groupID, root.StartedAt), err
	}
	return liveOwnedGroupMembers(processes, groupID, root.StartedAt), nil
}

func liveOwnedGroupMembers(processes []Process, groupID int, rootStartedAt time.Time) []child.ProcessMember {
	var members []child.ProcessMember
	for _, process := range processes {
		if process.processGroupID != groupID || process.StartedAt.Before(rootStartedAt) || process.Identity == "" {
			continue
		}
		members = append(members, child.ProcessMember{PID: process.PID, Identity: process.Identity})
	}
	return members
}
