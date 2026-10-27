package proc

import (
	"testing"
	"time"
)

// case: S-261.6
func TestOwnedGroupMembersExcludeReusedGroupIdentity(t *testing.T) {
	rootStarted := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	processes := []Process{
		{PID: 5201, StartedAt: rootStarted.Add(-time.Second), Identity: "old-group-member", processGroupID: 5201},
		{PID: 5202, StartedAt: rootStarted.Add(time.Second), Identity: "owned-descendant", processGroupID: 5201},
		{PID: 5203, StartedAt: rootStarted.Add(time.Second), Identity: "other-group", processGroupID: 99},
	}
	members := liveOwnedGroupMembers(processes, 5201, rootStarted)
	if len(members) != 1 || members[0].PID != 5202 || members[0].Identity != "owned-descendant" {
		t.Fatalf("live owned group members = %+v, want only the post-start member", members)
	}
}
